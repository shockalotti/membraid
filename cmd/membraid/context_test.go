package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type hookPayload struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// The SessionStart hook path, through the real binary: valid hook JSON, the
// open task and a known fact in the digest, and a clean exit on an empty vault.
func TestContextIsAValidSessionStartPayload(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "membraid")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	env := append(os.Environ(), "MEMBRAID_VAULT="+filepath.Join(root, "vault"), "MEMBRAID_CONFIG_DIR="+filepath.Join(root, "cfg"))
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		cmd.Dir = root // not a git project: resolves to shared
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("membraid %v: %v", args, err)
		}
		return string(out)
	}
	parse := func(raw string) hookPayload {
		t.Helper()
		var p hookPayload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("hook output must be JSON, got %q", raw)
		}
		if p.HookSpecificOutput.HookEventName != "SessionStart" {
			t.Errorf("hookEventName must be SessionStart: %+v", p)
		}
		return p
	}

	run("init")
	empty := parse(run("context", "--format", "claude"))
	if !strings.Contains(empty.HookSpecificOutput.AdditionalContext, "Nothing is recorded") {
		t.Errorf("an empty vault should still say memory exists: %q", empty.HookSpecificOutput.AdditionalContext)
	}

	run("write", "migrating auth to OIDC", "--kind", "task_state", "--key", "task.auth", "--scope", "shared")
	run("write", "always use pnpm exec, never dlx", "--kind", "preference", "--key", "pkg.runner", "--scope", "shared")
	ctx := parse(run("context", "--format", "claude")).HookSpecificOutput.AdditionalContext
	for _, want := range []string{"Where the user left off", "migrating auth to OIDC", "pnpm exec", "memory_done"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("digest missing %q:\n%s", want, ctx)
		}
	}
	if len(ctx) > 4000 {
		t.Errorf("the digest must stay small, got %d chars", len(ctx))
	}
}

// A broken vault must not break the session the hook runs in.
func TestContextNeverFailsTheSession(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "membraid")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "context", "--format", "claude")
	// A vault path that cannot exist as a directory.
	blocker := filepath.Join(root, "file")
	os.WriteFile(blocker, []byte("x"), 0o600)
	cmd.Env = append(os.Environ(), "MEMBRAID_VAULT="+filepath.Join(blocker, "vault"), "MEMBRAID_CONFIG_DIR="+filepath.Join(root, "cfg"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("context must exit 0 even when the vault is unusable: %v", err)
	}
	var p hookPayload
	if json.Unmarshal(out, &p) != nil {
		t.Errorf("must still emit valid hook JSON, got %q", out)
	}
}
