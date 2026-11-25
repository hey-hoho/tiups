package syncer

import (
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pkg/sftp"
)

var mirrorLine = regexp.MustCompile(`(?m)^\s*mirror\s*=\s*"([^"]*)"`)

// LocalMirror returns the directory that tiup.toml names as the mirror when it is a
// local path (an offline package set up with `tiup mirror set <dir>`), else "".
// A mirror inside localRoot is synced with everything else, so it is not reported.
func LocalMirror(localRoot string) string {
	b, err := os.ReadFile(filepath.Join(localRoot, "tiup.toml"))
	if err != nil {
		return ""
	}
	return localMirrorFrom(string(b), localRoot)
}

func localMirrorFrom(toml, localRoot string) string {
	m := mirrorLine.FindStringSubmatch(toml)
	if m == nil {
		return ""
	}
	p := strings.TrimSpace(m[1])
	if p == "" || strings.Contains(p, "://") || !strings.HasPrefix(p, "/") {
		return "" // URL, or not an absolute POSIX path
	}
	p = pathpkg.Clean(p)
	lr := pathpkg.Clean(filepath.ToSlash(localRoot))
	if p == lr || strings.HasPrefix(p, lr+"/") {
		return ""
	}
	return p
}

// MirrorWarning checks that the node has the offline mirror dir that the synced tiup.toml
// points to; without it every tiup command on the node fails with "stat <dir>: no such file".
// It returns "" when dir is empty or present on the node.
func MirrorWarning(sc *sftp.Client, dir string) string {
	if dir == "" {
		return ""
	}
	if fi, err := sc.Stat(dir); err == nil && fi.IsDir() {
		return ""
	}
	return fmt.Sprintf("tiup.toml points to the local mirror %s, which does not exist on this node, so tiup "+
		"fails there with \"stat %s: no such file or directory\". Run `tiup backup sync --with-mirror` to copy it "+
		"to the same path, or set up the node's own mirror (`tiup mirror set <dir>` on the node, see README)", dir, dir)
}
