package syncer

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

// newTestClient wires an sftp client to an in-process sftp server (real filesystem).
func newTestClient(t *testing.T) *sftp.Client {
	t.Helper()
	cr, sw := io.Pipe() // server -> client
	sr, cw := io.Pipe() // client -> server
	srv, err := sftp.NewServer(struct {
		io.Reader
		io.WriteCloser
	}{sr, sw})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	c, err := sftp.NewClientPipe(cr, cw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cw.Close(); srv.Close(); c.Close() })
	return c
}

func write(t *testing.T, p, s string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
}

func TestLocalMirrorFrom(t *testing.T) {
	cases := []struct{ toml, want string }{
		{`mirror = "https://tiup-mirrors.pingcap.com"`, ""},
		{`mirror = "/home/tidb/tidb-community-server-v8.5.4-linux-amd64"`, "/home/tidb/tidb-community-server-v8.5.4-linux-amd64"},
		{"a = 1\n  mirror = \"/data/mirror/\"\n", "/data/mirror"},
		{`mirror = "/home/tidb/.tiup/mirror"`, ""}, // inside TIUP_HOME: synced with it
		{`mirror = "relative/dir"`, ""},
		{`mirror = ""`, ""},
		{`other = "/x"`, ""},
	}
	for _, c := range cases {
		if got := localMirrorFrom(c.toml, "/home/tidb/.tiup"); got != c.want {
			t.Errorf("localMirrorFrom(%q) = %q, want %q", c.toml, got, c.want)
		}
	}
}

func TestMirrorWarning(t *testing.T) {
	local := t.TempDir()
	sc := newTestClient(t)

	write(t, filepath.Join(local, "tiup.toml"), `mirror = "https://tiup-mirrors.pingcap.com"`, 0o644)
	if dir := LocalMirror(local); dir != "" || MirrorWarning(sc, dir) != "" {
		t.Errorf("URL mirror must not warn (dir=%q)", dir)
	}

	missing := "/tiup-backup-no-such-mirror-dir"
	write(t, filepath.Join(local, "tiup.toml"), `mirror = "`+missing+`"`, 0o644)
	dir := LocalMirror(local)
	if dir != missing {
		t.Fatalf("LocalMirror = %q, want %q", dir, missing)
	}
	w := MirrorWarning(sc, dir)
	if !strings.Contains(w, missing) || !strings.Contains(w, "--with-mirror") {
		t.Errorf("missing local mirror must warn and mention --with-mirror, got %q", w)
	}

	if filepath.Separator == '/' { // the in-process server shares this machine's filesystem
		if w := MirrorWarning(sc, t.TempDir()); w != "" {
			t.Errorf("existing mirror must not warn: %s", w)
		}
	}
}

// The mirror is copied to the same absolute path on the node, so SyncNode must accept the
// local root as its own remote root and keep symlinks and modes intact.
func TestSyncMirrorSamePath(t *testing.T) {
	if filepath.Separator != '/' {
		t.Skip("needs POSIX paths")
	}
	mirror := t.TempDir()
	write(t, filepath.Join(mirror, "root.json"), "{}", 0o644)
	write(t, filepath.Join(mirror, "tiup-linux-amd64.tar.gz"), "tarball", 0o644)
	write(t, filepath.Join(mirror, "keys", "a.json"), "key", 0o600)

	entries, err := Scan(mirror, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The node is this same machine (in-process sftp server); pretend the mirror was
	// absent there by syncing into a different dir with the same layout.
	remote := filepath.Join(t.TempDir(), "mirror")
	st, err := SyncNode(newTestClient(t), entries, Options{LocalRoot: mirror, RemoteDir: remote}, io.Discard)
	if err != nil || st.Failed != 0 || st.Uploaded != 3 {
		t.Fatalf("sync: %+v %v", st, err)
	}
	if fi, err := os.Stat(filepath.Join(remote, "keys", "a.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("key file mode not kept: %v %v", fi, err)
	}
}

func TestSync(t *testing.T) {
	local, remote := t.TempDir(), filepath.Join(t.TempDir(), "dst")
	write(t, filepath.Join(local, "bin", "tiup"), "binary-v1", 0o755)
	write(t, filepath.Join(local, "components", "cluster", "v1", "x"), "x", 0o644)
	write(t, filepath.Join(local, "storage", "meta", "known_hosts"), "secret", 0o600)
	write(t, filepath.Join(local, "data", "big"), "skipme", 0o644)

	sc := newTestClient(t)
	patterns := append(append([]string{}, DefaultExcludes...), "data")
	opts := Options{LocalRoot: local, RemoteDir: filepath.ToSlash(remote), Patterns: patterns}

	scanAndSync := func() Stats {
		entries, err := Scan(local, patterns)
		if err != nil {
			t.Fatal(err)
		}
		st, err := SyncNode(sc, entries, opts, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	st := scanAndSync()
	if st.Uploaded != 2 || st.Failed != 0 {
		t.Fatalf("first sync: %+v", st)
	}
	got, err := os.ReadFile(filepath.Join(remote, "bin", "tiup"))
	if err != nil || string(got) != "binary-v1" {
		t.Fatalf("remote file: %q %v", got, err)
	}
	for _, p := range []string{"storage/meta/known_hosts", "data/big"} {
		if _, err := os.Stat(filepath.Join(remote, filepath.FromSlash(p))); err == nil {
			t.Errorf("%s should be excluded", p)
		}
	}
	if fi, _ := os.Stat(filepath.Join(remote, "bin", "tiup")); fi != nil && filepath.Separator == '/' && fi.Mode().Perm() != 0o755 {
		t.Errorf("mode not preserved: %v", fi.Mode())
	}

	// Unchanged tree: nothing uploaded.
	if st := scanAndSync(); st.Uploaded != 0 {
		t.Fatalf("second sync should be a no-op: %+v", st)
	}

	// Changed file is re-uploaded; leftover temp files must not remain.
	write(t, filepath.Join(local, "bin", "tiup"), "binary-v2-longer", 0o755)
	if st := scanAndSync(); st.Uploaded != 1 {
		t.Fatalf("changed sync: %+v", st)
	}
	got, _ = os.ReadFile(filepath.Join(remote, "bin", "tiup"))
	if string(got) != "binary-v2-longer" {
		t.Fatalf("not updated: %q", got)
	}
	if _, err := os.Stat(filepath.Join(remote, "bin", "tiup.tiup-backup.tmp")); err == nil {
		t.Error("temp file left behind")
	}

	// --delete removes stale remote files but not excluded ones.
	write(t, filepath.Join(remote, "stale", "old"), "old", 0o644)
	write(t, filepath.Join(remote, "data", "keep"), "keep", 0o644)
	opts.Delete = true
	if st := scanAndSync(); st.Deleted != 1 {
		t.Fatalf("delete sync: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(remote, "stale")); err == nil {
		t.Error("stale dir not deleted")
	}
	if _, err := os.Stat(filepath.Join(remote, "data", "keep")); err != nil {
		t.Error("excluded path was deleted")
	}
}

func TestEnsurePath(t *testing.T) {
	sc := newTestClient(t)
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }

	// Existing ~/.bash_profile without a trailing newline: block is appended, nothing else touched.
	home := t.TempDir()
	hs := filepath.ToSlash(home)
	root := hs + "/.tiup"
	prof := filepath.Join(home, ".bash_profile")
	write(t, prof, "export FOO=1", 0o644)
	write(t, filepath.Join(home, ".bashrc"), "# rc", 0o644) // lower priority than .bash_profile

	msg, err := ensurePathAt(sc, hs, "bash", root, true)
	if err != nil || !strings.HasPrefix(msg, "would add") || read(prof) != "export FOO=1" {
		t.Fatalf("dry-run: %q %v %q", msg, err, read(prof))
	}
	msg, err = ensurePathAt(sc, hs, "bash", root, false)
	if err != nil || !strings.HasPrefix(msg, "added") {
		t.Fatalf("first: %q %v", msg, err)
	}
	want := "export FOO=1\n\n" + pathMarker + " (added by `tiup backup sync`)\nexport PATH='" + root + "/bin':$PATH\n"
	if got := read(prof); got != want {
		t.Fatalf("profile:\n%q\nwant\n%q", got, want)
	}
	if read(filepath.Join(home, ".bashrc")) != "# rc" {
		t.Error(".bashrc must not be touched")
	}
	msg, _ = ensurePathAt(sc, hs, "bash", root, false)
	if !strings.HasPrefix(msg, "already") || read(prof) != want {
		t.Fatalf("second run must be a no-op: %q", msg)
	}

	// Installer-style line already present: no duplicate.
	home2 := t.TempDir()
	h2 := filepath.ToSlash(home2)
	write(t, filepath.Join(home2, ".zshrc"), "export PATH="+h2+"/.tiup/bin:$PATH\n", 0o644)
	if msg, _ := ensurePathAt(sc, h2, "zsh", h2+"/.tiup", false); !strings.HasPrefix(msg, "already") {
		t.Errorf("installer line not recognised: %q", msg)
	}

	// No profile at all -> ~/.profile; custom root also exports TIUP_HOME.
	home3 := t.TempDir()
	h3 := filepath.ToSlash(home3)
	if _, err := ensurePathAt(sc, h3, "", "/opt/tiup-copy", false); err != nil {
		t.Fatal(err)
	}
	got := read(filepath.Join(home3, ".profile"))
	if !strings.Contains(got, "export TIUP_HOME='/opt/tiup-copy'") || !strings.Contains(got, "export PATH='/opt/tiup-copy/bin':$PATH") {
		t.Errorf(".profile: %q", got)
	}

	// Unsupported shell is skipped without writing.
	if msg, _ := ensurePathAt(sc, h3, "fish", root, false); !strings.HasPrefix(msg, "skipped") {
		t.Errorf("fish: %q", msg)
	}
}

func TestRewriteLink(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/home/a/.tiup/components/x/y", "/srv/.tiup/components/x/y"},
		{"/home/a/.tiup", "/srv/.tiup"},
		{"/usr/bin/other", "/usr/bin/other"},
		{"/home/a/.tiup2/x", "/home/a/.tiup2/x"},
		{"relative/path", "relative/path"},
	}
	for _, c := range cases {
		if got := rewriteLink(c.in, "/home/a/.tiup", "/srv/.tiup"); got != c.want {
			t.Errorf("rewriteLink(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
