package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHome(t *testing.T) {
	t.Setenv("TIUP_HOME", "/data/tiup/")
	got, err := Home()
	if err != nil || got != filepath.Clean("/data/tiup/") {
		t.Fatalf("Home() = %q, %v", got, err)
	}
	t.Setenv("TIUP_HOME", "")
	got, err = Home()
	if err != nil || filepath.Base(got) != ".tiup" {
		t.Fatalf("default Home() = %q, %v", got, err)
	}
}

func TestNodesRoundTrip(t *testing.T) {
	home := t.TempDir()
	if nodes, err := LoadNodes(home); err != nil || len(nodes) != 0 {
		t.Fatalf("missing file should mean no nodes: %v %v", nodes, err)
	}
	in := []Node{
		{Host: "10.0.0.3", Port: 22, User: "tidb", IdentityFile: "/k"},
		{Host: "10.0.0.2", Port: 2222, User: "root", RemoteDir: "/opt/tiup"},
	}
	if err := SaveNodes(home, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadNodes(home)
	if err != nil || len(out) != 2 {
		t.Fatalf("LoadNodes: %v %v", out, err)
	}
	if out[0].Host != "10.0.0.2" || out[1].IdentityFile != "/k" || out[0].RemoteDir != "/opt/tiup" {
		t.Errorf("unexpected content (should be sorted by host): %+v", out)
	}
	if _, err := os.Stat(NodesFile(home) + ".tmp"); err == nil {
		t.Error("temp file left behind")
	}
	if fi, err := os.Stat(NodesFile(home)); err != nil || (filepath.Separator == '/' && fi.Mode().Perm() != 0o600) {
		t.Errorf("node file must be owner-only: %v %v", fi, err)
	}
}

func TestLoadNodesRejectsGarbage(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(MetaDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(NodesFile(home), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadNodes(home); err == nil {
		t.Error("corrupt node file must be reported, not ignored")
	}
}
