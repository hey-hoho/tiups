// Package remote opens authenticated SSH + SFTP sessions to backup nodes.
package remote

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/term"

	"tiup-backup/internal/config"
)

// PasswordEnv lets non-interactive runs supply the SSH password.
const PasswordEnv = "TIUP_META_PASSWORD"

// Credentials caches one password so it is asked for at most once per run
// when all nodes share it; a node that rejects it is asked individually.
type Credentials struct {
	mu       sync.Mutex
	shared   string
	haveFlag bool
}

func readPassword(prompt string) (string, error) {
	if pw := os.Getenv(PasswordEnv); pw != "" {
		return pw, nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("no terminal available for password prompt; set %s or use -i", PasswordEnv)
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

func (c *Credentials) sharedPassword(first config.Node) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.haveFlag {
		return c.shared, nil
	}
	pw, err := readPassword(fmt.Sprintf("Password for %s@%s (reused for other nodes): ", first.User, first.Host))
	if err != nil {
		return "", err
	}
	c.shared, c.haveFlag = pw, true
	return pw, nil
}

func hostKeyCallback(khPath string, assumeYes bool) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if _, err := os.Stat(khPath); os.IsNotExist(err) {
			if err := os.WriteFile(khPath, nil, 0o600); err != nil {
				return err
			}
		}
		check, err := knownhosts.New(khPath)
		if err != nil {
			return err
		}
		err = check(hostname, remote, key)
		if err == nil {
			return nil
		}
		var ke *knownhosts.KeyError
		// A host we already have any key for must never fall back to
		// trust-on-first-use, even if the server now offers another key type.
		if !errors.As(err, &ke) || len(ke.Want) > 0 || HostRecorded(khPath, hostname) {
			// Key changed: refuse (possible MITM or reinstalled node).
			return fmt.Errorf("host key mismatch for %s, remove its line from %s if the change is expected: %w", hostname, khPath, err)
		}
		fp := ssh.FingerprintSHA256(key)
		if !assumeYes {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("unknown host %s (%s); rerun with --yes to trust it", hostname, fp)
			}
			fmt.Fprintf(os.Stderr, "The authenticity of host %s can't be established.\n%s\nTrust it and continue? [y/N] ", hostname, fp)
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				return errors.New("host key not trusted")
			}
		}
		f, err := os.OpenFile(khPath, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = fmt.Fprintln(f, knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key))
		return err
	}
}

// IsUnknownHost reports whether err is the "host key not trusted yet" refusal.
func IsUnknownHost(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unknown host")
}

// HostRecorded reports whether known_hosts has any (unhashed) entry for host.
func HostRecorded(khPath, host string) bool {
	data, err := os.ReadFile(khPath)
	if err != nil {
		return false
	}
	want := knownhosts.Normalize(host)
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || strings.HasPrefix(f[0], "#") {
			continue
		}
		for _, h := range strings.Split(f[0], ",") {
			if h == want {
				return true
			}
		}
	}
	return false
}

func publicKeyAuth(path string) (ssh.AuthMethod, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(pem)
	if err != nil {
		return nil, fmt.Errorf("%s: %w (passphrase-protected keys are not supported)", path, err)
	}
	return ssh.PublicKeys(signer), nil
}

// defaultAuthMethods returns what plain `ssh` would try without -i:
// the ssh-agent, then ~/.ssh/id_{ed25519,ecdsa,rsa} (unencrypted keys only).
func defaultAuthMethods() []ssh.AuthMethod {
	var methods []ssh.AuthMethod
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		var signers []ssh.Signer
		for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			pem, err := os.ReadFile(filepath.Join(home, ".ssh", name))
			if err != nil {
				continue
			}
			if s, err := ssh.ParsePrivateKey(pem); err == nil {
				signers = append(signers, s)
			}
		}
		if len(signers) > 0 {
			methods = append(methods, ssh.PublicKeys(signers...))
		}
	}
	return methods
}

func isAuthFailure(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unable to authenticate")
}

// Connect opens an SSH + SFTP session to node. Without an explicit key it tries
// the agent and default keys first, then a password. keyOnly forbids the
// password fallback (used by cron and by `job` to verify unattended access).
func Connect(node config.Node, creds *Credentials, khPath string, assumeYes, keyOnly bool) (*ssh.Client, *sftp.Client, error) {
	cfg := &ssh.ClientConfig{
		User:            node.User,
		HostKeyCallback: hostKeyCallback(khPath, assumeYes),
		Timeout:         15 * time.Second,
	}
	addr := net.JoinHostPort(node.Host, strconv.Itoa(node.Port))

	dial := func() (*ssh.Client, error) { return ssh.Dial("tcp", addr, cfg) }

	var client *ssh.Client
	var err error
	if node.IdentityFile != "" {
		m, kerr := publicKeyAuth(node.IdentityFile)
		if kerr != nil {
			return nil, nil, kerr
		}
		cfg.Auth = []ssh.AuthMethod{m}
		client, err = dial()
	} else {
		if methods := defaultAuthMethods(); len(methods) > 0 {
			cfg.Auth = methods
			client, err = dial()
			if err != nil && !isAuthFailure(err) {
				return nil, nil, err // network or host key problem: a password will not help
			}
		} else {
			err = errors.New("unable to authenticate: no ssh-agent identity or default key in ~/.ssh")
		}
		if client == nil {
			if keyOnly {
				return nil, nil, fmt.Errorf("key authentication to %s@%s failed (%v); install your public key on the node "+
					"(ssh-copy-id) or pass -i <private-key> to `tiup backup add`", node.User, node.Host, err)
			}
			pw, perr := creds.sharedPassword(node)
			if perr != nil {
				return nil, nil, perr
			}
			cfg.Auth = []ssh.AuthMethod{ssh.Password(pw)}
			client, err = dial()
			if isAuthFailure(err) {
				own, perr := readPassword(fmt.Sprintf("Shared password rejected. Password for %s@%s: ", node.User, node.Host))
				if perr != nil {
					return nil, nil, perr
				}
				cfg.Auth = []ssh.AuthMethod{ssh.Password(own)}
				client, err = dial()
			}
		}
	}
	if err != nil {
		return nil, nil, err
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("start sftp (is the sftp subsystem enabled on %s?): %w", node.Host, err)
	}
	return client, sc, nil
}
