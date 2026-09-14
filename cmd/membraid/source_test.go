package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A knowledge location is a memory pointing at a folder. Adding the same folder
// again replaces its entry, the folder is stored relative to home so it reads
// the same on every machine, and a folder that is not there is refused.
func TestSourceAddListRemove(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "membraid")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := t.TempDir()
	specs := filepath.Join(home, "Work", "API Specs")
	if err := os.MkdirAll(specs, 0o755); err != nil {
		t.Fatal(err)
	}
	vaultDir := filepath.Join(t.TempDir(), "memory")
	env := append(os.Environ(), "HOME="+home, "MEMBRAID_CONFIG_DIR="+t.TempDir(), "MEMBRAID_VAULT="+vaultDir)
	run := func(wantErr bool, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		cmd.Dir = home
		out, err := cmd.CombinedOutput()
		if (err != nil) != wantErr {
			t.Fatalf("membraid %s: err=%v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	list := func() []sourceInfo {
		t.Helper()
		var got []sourceInfo
		if err := json.Unmarshal([]byte(run(false, "source", "list", "--json")), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	run(false, "init", "--vault", vaultDir)
	if out := run(false, "source", "add", specs, "--about", "API design specs; read before changing endpoints.", "--scope", "shared", "--source", "user"); !strings.Contains(out, "added knowledge location source.api.specs") {
		t.Errorf("add output: %q", out)
	}
	got := list()
	if len(got) != 1 {
		t.Fatalf("want one location, got %+v", got)
	}
	s := got[0]
	if s.Type != "folder" || s.Path != "~/Work/API Specs" || s.About != "API design specs; read before changing endpoints" || !s.Here || s.Host == "" {
		t.Errorf("a folder with a space, stored relative to home, must parse back whole: %+v", s)
	}
	if !strings.HasPrefix(s.Content, "Knowledge location: API design specs; read before changing endpoints. Folder ~/Work/API Specs, only on ") {
		t.Errorf("content wrong: %q", s.Content)
	}
	if s.Key != "source.api.specs" || s.Scope != "shared" || s.Source != "user" {
		t.Errorf("entry wrong: %+v", s)
	}

	run(false, "source", "add", specs, "--about", "API specs, versioned", "--scope", "shared")
	if got := list(); len(got) != 1 || got[0].About != "API specs, versioned" {
		t.Errorf("adding the same folder again must replace its entry, got %+v", got)
	}
	if out := run(true, "source", "add", filepath.Join(home, "nope"), "--about", "missing", "--scope", "shared"); !strings.Contains(out, "no folder") {
		t.Errorf("a missing folder must be refused, got %q", out)
	}
	run(false, "source", "add", "https://www.notion.so/Team-Runbook", "--about", "Team runbook", "--scope", "shared")
	var web sourceInfo
	for _, s := range list() {
		if s.Type == "web" {
			web = s
		}
	}
	if web.Service != "Notion" || !web.Login || web.Open != "https://www.notion.so/Team-Runbook" {
		t.Errorf("a Notion page must be recognised as needing Notion access: %+v", web)
	}
	run(false, "source", "remove", web.Key, "--scope", "shared")
	run(false, "source", "remove", "api.specs", "--scope", "shared")
	if got := list(); len(got) != 0 {
		t.Errorf("remove must retire the location, got %+v", got)
	}
}
