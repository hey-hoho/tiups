package remote

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHostRecorded(t *testing.T) {
	kh := filepath.Join(t.TempDir(), "known_hosts")
	if HostRecorded(kh, "10.0.0.2") {
		t.Fatal("missing file must mean not recorded")
	}
	content := "# comment\n" +
		"10.0.0.2 ssh-ed25519 AAAA\n" +
		"[10.0.0.3]:2222,node3 ecdsa-sha2-nistp256 BBBB\n" +
		"|1|hashed|entry ssh-rsa CCCC\n"
	if err := os.WriteFile(kh, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{
		"10.0.0.2":      true,
		"10.0.0.2:22":   true, // :22 is normalised away
		"10.0.0.3:2222": true,
		"node3":         true,
		"10.0.0.4":      false,
		"10.0.0.":       false, // no prefix matching
	} {
		if got := HostRecorded(kh, host); got != want {
			t.Errorf("HostRecorded(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestIsUnknownHost(t *testing.T) {
	if !IsUnknownHost(errors.New("ssh: handshake failed: unknown host 1.2.3.4:22 (SHA256:x); rerun with --yes")) {
		t.Error("should match the unknown host refusal")
	}
	if IsUnknownHost(nil) || IsUnknownHost(errors.New("host key mismatch for 1.2.3.4")) {
		t.Error("must not match other errors")
	}
}
