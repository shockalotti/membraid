package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cleanEnv is the test process's environment without anything that would pick
// a scope for it.
func cleanEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "MEMBRAID_SCOPE=") {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

func buildMembraid(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "membraid")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// From inside a project, memory_get finds a preference kept in shared, and says
// which scope each answer is in. Before, it only looked in the project.
func TestMCPGetFindsSharedFromAProject(t *testing.T) {
	bin := buildMembraid(t)
	vaultDir := filepath.Join(t.TempDir(), "memory")
	env := cleanEnv("MEMBRAID_CONFIG_DIR="+t.TempDir(), "MEMBRAID_VAULT="+vaultDir)
	exec.Command(bin, "init", "--vault", vaultDir).Run()

	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_write","arguments":{"content":"uses pnpm","kind":"preference","key":"pkg.manager","scope":"shared"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memory_get","arguments":{"key":"pkg.manager","scope":"g12345678"}}}`,
	}, "\n") + "\n"
	cmd := exec.Command(bin, "mcp", "--source", "test")
	cmd.Env = env
	cmd.Stdin = strings.NewReader(in)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[float64]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var m map[string]any
		json.Unmarshal([]byte(line), &m)
		if id, ok := m["id"].(float64); ok {
			byID[id] = m
		}
	}
	if txt := toolText(t, byID[3]); !strings.Contains(txt, "uses pnpm") || !strings.Contains(txt, "shared") {
		t.Errorf("memory_get from a project must find the shared preference, got %q", txt)
	}
}

// A write with no project, such as an agent started from the home directory,
// is quarantined in unscoped instead of shared, the tool reply says so, a
// search does not surface it, and status counts it.
func TestWriteWithNoProjectIsQuarantined(t *testing.T) {
	bin := buildMembraid(t)
	home := t.TempDir()
	vaultDir := filepath.Join(t.TempDir(), "memory")
	env := cleanEnv("HOME="+home, "MEMBRAID_CONFIG_DIR="+t.TempDir(), "MEMBRAID_VAULT="+vaultDir)
	run := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		cmd.Dir = home
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("membraid %v: %v", args, err)
		}
		return string(out)
	}
	run("", "init", "--vault", vaultDir)

	out := run(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_write","arguments":{"content":"the staging db is on port 5432","kind":"insight"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memory_write","arguments":{"content":"forged","kind":"insight","scope":"unscoped"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"memory_search","arguments":{"query":"staging db port"}}}`,
	}, "\n")+"\n", "mcp", "--source", "test")
	byID := map[float64]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var m map[string]any
		json.Unmarshal([]byte(line), &m)
		if id, ok := m["id"].(float64); ok {
			byID[id] = m
		}
	}
	if txt := toolText(t, byID[2]); !strings.Contains(txt, "Remembered in unscoped") || !strings.Contains(txt, `scope "shared"`) {
		t.Errorf("a write with no project must say it was quarantined, got %q", txt)
	}
	if txt := toolText(t, byID[3]); !strings.Contains(txt, "cannot be chosen") {
		t.Errorf("asking for unscoped must be refused, got %q", txt)
	}
	if txt := toolText(t, byID[4]); strings.Contains(txt, "5432") {
		t.Errorf("a default search must not surface quarantined memories, got %q", txt)
	}
	var status map[string]any
	json.Unmarshal([]byte(run("", "status", "--json", "--scope", "*")), &status)
	if status["unscoped"] != float64(1) {
		t.Errorf("status must count the quarantined memory, got %v", status["unscoped"])
	}
}

// reindex sets the index aside and rebuilds it from the log, losing nothing
// the log holds.
func TestReindexRebuildsFromTheLog(t *testing.T) {
	bin := buildMembraid(t)
	vaultDir := filepath.Join(t.TempDir(), "memory")
	env := cleanEnv("MEMBRAID_CONFIG_DIR="+t.TempDir(), "MEMBRAID_VAULT="+vaultDir)
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("membraid %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "--vault", vaultDir)
	run("write", "uses pnpm", "--kind", "preference", "--key", "pkg.manager", "--scope", "shared")
	run("write", "uses bun now", "--kind", "preference", "--key", "pkg.manager", "--scope", "shared")

	if out := run("reindex"); !strings.Contains(out, "1 current memories, 2 in history") {
		t.Errorf("reindex output: %q", out)
	}
	if got := run("get", "pkg.manager", "--scope", "shared"); !strings.Contains(got, "uses bun now") {
		t.Errorf("the rebuilt index must hold the current answer, got %q", got)
	}
	if backups, _ := filepath.Glob(filepath.Join(vaultDir, ".hot", "index.db.reindex.*.bak")); len(backups) != 1 {
		t.Errorf("the old index must be kept, got %v", backups)
	}
}
