// Package syncer copies a local TiUP home to a node over SFTP.
package syncer

import (
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"time"
)

// DefaultExcludes are never copied: per-machine state of this tool.
var DefaultExcludes = []string{"storage/meta/known_hosts", "storage/meta/sync.log", "storage/meta/sync.lock"}

// Entry is one file, directory or symlink below the local TiUP home.
type Entry struct {
	Rel   string // slash-separated, relative to TIUP_HOME
	Mode  fs.FileMode
	Size  int64
	MTime int64 // unix seconds
	Link  string
}

func (e Entry) isDir() bool  { return e.Mode.IsDir() }
func (e Entry) isLink() bool { return e.Mode&fs.ModeSymlink != 0 }

func fileTime(sec int64) time.Time { return time.Unix(sec, 0) }

func excluded(rel string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := pathpkg.Match(p, rel); ok {
			return true
		}
		if ok, _ := pathpkg.Match(p, pathpkg.Base(rel)); ok {
			return true
		}
	}
	return false
}

// Scan lists everything under root (parents before children).
func Scan(root string, patterns []string) ([]Entry, error) {
	var out []Entry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if excluded(rel, patterns) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		e := Entry{Rel: rel, Mode: info.Mode(), Size: info.Size(), MTime: info.ModTime().Unix()}
		switch {
		case e.isLink():
			if e.Link, err = os.Readlink(p); err != nil {
				return err
			}
		case e.isDir(), e.Mode.IsRegular():
		default:
			return nil // sockets, devices, ...
		}
		out = append(out, e)
		return nil
	})
	return out, err
}
