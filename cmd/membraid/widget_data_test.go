package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The bar widget reads everything through membraid commands with --json and
// acts through membraid commands. These are the ones it depends on.
func TestWidgetDataCommands(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "membraid")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	vaultDir := filepath.Join(t.TempDir(), "memory")
	project := t.TempDir()
	env := append(os.Environ(), "MEMBRAID_CONFIG_DIR="+t.TempDir(), "MEMBRAID_VAULT="+vaultDir, "HOME="+t.TempDir())
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		cmd.Dir = project
		out, err := cmd.Output()
		if err != nil {
			stderr := ""
			if ee, ok := err.(*exec.ExitError); ok {
				stderr = string(ee.Stderr)
			}
			t.Fatalf("membraid %s: %v\n%s%s", strings.Join(args, " "), err, out, stderr)
		}
		return string(out)
	}
	decode := func(s string, v any) {
		t.Helper()
		if err := json.Unmarshal([]byte(s), v); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, s)
		}
	}
	idOf := func(out string) string {
		return strings.Fields(strings.TrimPrefix(out, "wrote "))[0]
	}

	run("init", "--vault", vaultDir)
	run("write", "uses pnpm", "--kind", "preference", "--scope", "shared", "--source", "user")
	wrong := idOf(run("write", "the staging db is on port 5433", "--kind", "insight", "--source", "opencode"))
	run("write", "finish the widget", "--kind", "task_state", "--key", "task.widget", "--source", "claude-code")

	// Correcting a memory without a key retires the original.
	run("write", "the staging db is on port 5432", "--kind", "insight", "--source", "user", "--replaces", wrong)
	var mems []map[string]any
	decode(run("memories", "--json"), &mems)
	if len(mems) != 3 {
		t.Fatalf("want 3 current memories after the correction, got %d: %v", len(mems), mems)
	}
	for _, m := range mems {
		if strings.Contains(m["content"].(string), "5433") {
			t.Error("the corrected memory must be retired")
		}
	}
	decode(run("memories", "--json", "--source", "user", "--kind", "preference"), &mems)
	if len(mems) != 1 || mems[0]["content"] != "uses pnpm" {
		t.Errorf("filters wrong: %v", mems)
	}

	// A person searching from the widget does not count as an agent using memory.
	var hits []map[string]any
	decode(run("search", "pnpm", "--json", "--scope", "*", "--no-track"), &hits)
	if len(hits) != 1 {
		t.Errorf("want one hit, got %v", hits)
	}
	decode(run("search", "nothingmatchesthis", "--json", "--scope", "*", "--no-track"), &hits)
	if hits == nil {
		t.Error("no results must be an empty list")
	}
	var in struct {
		NeverUsed int `json:"never_used"`
		Current   int `json:"current"`
	}
	decode(run("insights", "--json"), &in)
	if in.Current != 3 || in.NeverUsed != 3 {
		t.Errorf("browsing must not mark memories used: %+v", in)
	}

	var projects []map[string]any
	decode(run("projects", "--json"), &projects)
	if len(projects) != 2 || projects[0]["scope"] != "shared" || projects[1]["open_tasks"] != float64(1) {
		t.Errorf("projects wrong: %v", projects)
	}

	var cfg map[string]any
	decode(run("config", "--json"), &cfg)
	if cfg["embeddings"] != "off" || cfg["halflife_days"] != float64(30) || cfg["embed_model"] == "" {
		t.Errorf("config wrong: %v", cfg)
	}

	var status map[string]any
	decode(run("status", "--json", "--scope", "*"), &status)
	if status["version"] == "" || status["version"] == nil {
		t.Error("status must carry the version")
	}

	var harnesses []map[string]any
	decode(run("install", "--list", "--bin", bin), &harnesses)
	seen := map[string]map[string]any{}
	for _, h := range harnesses {
		seen[h["id"].(string)] = h
	}
	if seen["claude-code"] == nil || seen["claude-code"]["configured"] != false || seen["embeddings"]["configured"] != nil {
		t.Errorf("harness list wrong: %v", harnesses)
	}
}
