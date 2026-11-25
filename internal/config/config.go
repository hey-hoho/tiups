// Package config resolves the local TiUP home and persists the list of backup nodes.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Node is one backup node that receives a copy of $TIUP_HOME.
// Passwords are never persisted.
type Node struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	User         string `json:"user"`
	IdentityFile string `json:"identity_file,omitempty"`
	// RemoteDir overrides the destination directory; default is ~/.tiup on the node.
	RemoteDir string `json:"remote_dir,omitempty"`
}

// Home resolves the local TiUP home. tiup exports TIUP_HOME to components.
func Home() (string, error) {
	if h := os.Getenv("TIUP_HOME"); h != "" {
		return filepath.Clean(h), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tiup"), nil
}

// MetaDir is where this tool keeps its own state, inside the TiUP home.
func MetaDir(home string) string { return filepath.Join(home, "storage", "meta") }

// NodesFile holds the backup node list.
func NodesFile(home string) string { return filepath.Join(MetaDir(home), "backup_nodes.json") }

// KnownHostsFile holds the SSH host keys trusted by this tool.
func KnownHostsFile(home string) string { return filepath.Join(MetaDir(home), "known_hosts") }

// LogFile receives the output of scheduled syncs.
func LogFile(home string) string { return filepath.Join(MetaDir(home), "sync.log") }

// LockFile serialises concurrent syncs.
func LockFile(home string) string { return filepath.Join(MetaDir(home), "sync.lock") }

// LoadNodes reads the node list; a missing file means no nodes.
func LoadNodes(home string) ([]Node, error) {
	data, err := os.ReadFile(NodesFile(home))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var nodes []Node
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, fmt.Errorf("parse %s: %w", NodesFile(home), err)
	}
	return nodes, nil
}

// SaveNodes writes the node list atomically with owner-only permissions.
func SaveNodes(home string, nodes []Node) error {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Host < nodes[j].Host })
	if err := os.MkdirAll(MetaDir(home), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return err
	}
	tmp := NodesFile(home) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, NodesFile(home))
}
