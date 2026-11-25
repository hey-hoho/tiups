package syncer

import (
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"tiup-backup/internal/shell"
)

const pathMarker = "# tiup-backup:path"

// POSIX-style shells whose startup files accept `export VAR=...`.
var posixShells = map[string]bool{"sh": true, "bash": true, "zsh": true, "ksh": true, "dash": true, "ash": true}

// remoteShell returns the base name of the login shell of the node's user ("" if unknown).
func remoteShell(c *ssh.Client) string {
	s, err := c.NewSession()
	if err != nil {
		return ""
	}
	defer s.Close()
	out, err := s.Output("echo $SHELL")
	if err != nil {
		return ""
	}
	return pathpkg.Base(strings.TrimSpace(string(out)))
}

// profileCandidates mirrors the official tiup installer's choice of startup file.
func profileCandidates(shell string) []string {
	var c []string
	if shell != "" {
		c = append(c, "."+shell+"_profile", "."+shell+"_login", "."+shell+"rc")
	}
	return append(c, ".profile")
}

// pathConfigured reports whether the profile already puts tiup's bin dir on PATH.
func pathConfigured(content, home, root string) bool {
	if strings.Contains(content, pathMarker) || strings.Contains(content, root+"/bin") {
		return true
	}
	// the official installer writes the literal path; ~ and $HOME forms are common by hand
	if root == pathpkg.Join(home, ".tiup") {
		return strings.Contains(content, ".tiup/bin")
	}
	return false
}

func pathBlock(home, root string) string {
	var b strings.Builder
	b.WriteString("\n" + pathMarker + " (added by `tiup backup sync`)\n")
	if root != pathpkg.Join(home, ".tiup") {
		fmt.Fprintf(&b, "export TIUP_HOME=%s\n", shell.Quote(root))
	}
	fmt.Fprintf(&b, "export PATH=%s:$PATH\n", shell.Quote(root+"/bin"))
	return b.String()
}

// ensurePathAt makes sure `tiup` is on PATH for the node user's future shells.
// It only ever appends a small block; existing content is never rewritten.
func ensurePathAt(sc *sftp.Client, home, loginShell, root string, dryRun bool) (string, error) {
	if loginShell != "" && !posixShells[loginShell] {
		return fmt.Sprintf("skipped: login shell %q is not supported, add %s/bin to PATH manually", loginShell, root), nil
	}

	target := ""
	for _, name := range profileCandidates(loginShell) {
		p := pathpkg.Join(home, name)
		if _, err := sc.Stat(p); err == nil {
			target = p
			break
		}
	}
	if target == "" {
		target = pathpkg.Join(home, ".profile")
	}

	var content string
	if f, err := sc.Open(target); err == nil {
		data, rerr := io.ReadAll(f)
		f.Close()
		if rerr != nil {
			return "", fmt.Errorf("read %s: %w", target, rerr)
		}
		content = string(data)
	}
	if pathConfigured(content, home, root) {
		return "already configured in " + target, nil
	}
	if dryRun {
		return "would add " + root + "/bin to PATH in " + target, nil
	}

	// Not O_APPEND: sftp writes carry explicit offsets, so seek to the old end instead.
	// Existing bytes are never rewritten.
	f, err := sc.OpenFile(target, os.O_WRONLY|os.O_CREATE)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", target, err)
	}
	if _, err := f.Seek(int64(len(content)), io.SeekStart); err != nil {
		f.Close()
		return "", fmt.Errorf("seek %s: %w", target, err)
	}
	block := pathBlock(home, root)
	if content != "" && !strings.HasSuffix(content, "\n") {
		block = "\n" + block
	}
	if _, err := f.Write([]byte(block)); err != nil {
		f.Close()
		return "", fmt.Errorf("write %s: %w", target, err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return "added " + root + "/bin to PATH in " + target + " (takes effect in new login shells)", nil
}
