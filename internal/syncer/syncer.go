package syncer

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Options controls one node sync.
type Options struct {
	LocalRoot string
	RemoteDir string // empty: ~/.tiup on the node
	Patterns  []string
	Delete    bool
	DryRun    bool
	// SetPath puts the node's tiup bin dir on PATH; needs SSHClient to learn the login shell.
	SetPath   bool
	SSHClient *ssh.Client
}

// Stats summarises one node sync.
type Stats struct {
	Uploaded, Skipped, Deleted, Failed int
	Bytes                              int64
	Note                               string // PATH configuration result
}

// rewriteLink maps absolute symlinks that point into the local TIUP_HOME
// (e.g. bin/tiup-cluster -> components/...) onto the remote root.
func rewriteLink(target, localRoot, remoteRoot string) string {
	lr := filepath.ToSlash(localRoot)
	if t := filepath.ToSlash(target); t == lr || strings.HasPrefix(t, lr+"/") {
		return remoteRoot + strings.TrimPrefix(t, lr)
	}
	return target
}

// SyncNode makes the node's TiUP home match entries; progress is written to log.
func SyncNode(sc *sftp.Client, entries []Entry, o Options, log io.Writer) (Stats, error) {
	var st Stats
	home, err := sc.RealPath(".")
	if err != nil {
		return st, fmt.Errorf("resolve remote home: %w", err)
	}
	remoteRoot := pathpkg.Join(home, ".tiup")
	if o.RemoteDir != "" {
		remoteRoot = pathpkg.Clean(o.RemoteDir)
	}
	if !o.DryRun {
		if err := sc.MkdirAll(remoteRoot); err != nil {
			return st, fmt.Errorf("create %s: %w", remoteRoot, err)
		}
	}
	fail := func(rel string, err error) {
		st.Failed++
		fmt.Fprintf(log, "  FAIL %s: %v\n", rel, err)
	}

	keep := make(map[string]bool, len(entries))
	for _, e := range entries {
		keep[e.Rel] = true
		dst := pathpkg.Join(remoteRoot, e.Rel)
		ri, rerr := sc.Lstat(dst)
		exists := rerr == nil

		switch {
		case e.isDir():
			if exists && ri.IsDir() {
				st.Skipped++
				continue
			}
			if o.DryRun {
				fmt.Fprintf(log, "  mkdir %s\n", e.Rel)
				continue
			}
			if exists {
				_ = sc.RemoveAll(dst)
			}
			if err := sc.MkdirAll(dst); err != nil {
				fail(e.Rel, err)
				continue
			}
			_ = sc.Chmod(dst, e.Mode.Perm())

		case e.isLink():
			target := rewriteLink(e.Link, o.LocalRoot, remoteRoot)
			if exists && ri.Mode()&fs.ModeSymlink != 0 {
				if cur, err := sc.ReadLink(dst); err == nil && cur == target {
					st.Skipped++
					continue
				}
			}
			if o.DryRun {
				fmt.Fprintf(log, "  link  %s -> %s\n", e.Rel, target)
				st.Uploaded++
				continue
			}
			if exists {
				_ = sc.RemoveAll(dst)
			}
			if err := sc.Symlink(target, dst); err != nil {
				fail(e.Rel, err)
				continue
			}
			st.Uploaded++

		default:
			if exists && ri.Mode().IsRegular() && ri.Size() == e.Size && ri.ModTime().Unix() == e.MTime {
				if ri.Mode().Perm() != e.Mode.Perm() && !o.DryRun {
					_ = sc.Chmod(dst, e.Mode.Perm())
				}
				st.Skipped++
				continue
			}
			if o.DryRun {
				fmt.Fprintf(log, "  put   %s (%d bytes)\n", e.Rel, e.Size)
				st.Uploaded++
				st.Bytes += e.Size
				continue
			}
			if exists && ri.IsDir() {
				_ = sc.RemoveAll(dst)
			}
			if err := upload(sc, filepath.Join(o.LocalRoot, filepath.FromSlash(e.Rel)), dst, e); err != nil {
				fail(e.Rel, err)
				continue
			}
			st.Uploaded++
			st.Bytes += e.Size
		}
	}

	if o.Delete {
		var extra []string
		w := sc.Walk(remoteRoot)
		for w.Step() {
			if w.Err() != nil {
				continue
			}
			p := w.Path()
			if p == remoteRoot {
				continue
			}
			rel := strings.TrimPrefix(p, remoteRoot+"/")
			if excluded(rel, o.Patterns) {
				if w.Stat().IsDir() {
					w.SkipDir()
				}
				continue
			}
			if !keep[rel] {
				extra = append(extra, p)
				if w.Stat().IsDir() {
					w.SkipDir()
				}
			}
		}
		sort.Strings(extra)
		for _, p := range extra {
			if o.DryRun {
				fmt.Fprintf(log, "  del   %s\n", p)
				st.Deleted++
				continue
			}
			if err := sc.RemoveAll(p); err != nil {
				fail(p, err)
				continue
			}
			st.Deleted++
		}
	}
	if o.SetPath && o.SSHClient != nil {
		msg, err := ensurePathAt(sc, home, remoteShell(o.SSHClient), remoteRoot, o.DryRun)
		if err != nil {
			fail("PATH setup", err)
		} else {
			st.Note = msg
		}
	}
	return st, nil
}

// upload writes to a temp file then renames, so a node never sees a half-written
// file and a running binary can be replaced safely.
func upload(sc *sftp.Client, local, dst string, e Entry) error {
	src, err := os.Open(local)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp := dst + ".tiup-backup.tmp"
	out, err := sc.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		_ = sc.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = sc.Remove(tmp)
		return err
	}
	if err := sc.Chmod(tmp, e.Mode.Perm()); err != nil {
		_ = sc.Remove(tmp)
		return err
	}
	mt := fileTime(e.MTime)
	if err := sc.Chtimes(tmp, mt, mt); err != nil {
		_ = sc.Remove(tmp)
		return err
	}
	if err := sc.PosixRename(tmp, dst); err != nil {
		if err2 := sc.Rename(tmp, dst); err2 != nil {
			_ = sc.Remove(tmp)
			return err2
		}
	}
	return nil
}
