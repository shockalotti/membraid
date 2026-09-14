package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A write that starts a key resembling one in use hears about it, and
// membraid keys lists the vocabulary with the pair.
func TestKeysListsVocabularyAndWarnsOnDrift(t *testing.T) {
	bin := buildMembraid(t)
	vaultDir := filepath.Join(t.TempDir(), "memory")
	env := cleanEnv("MEMBRAID_CONFIG_DIR="+t.TempDir(), "MEMBRAID_VAULT="+vaultDir)
	run := func(args ...string) (string, string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("membraid %v: %v\n%s", args, err, stderr.String())
		}
		return string(out), stderr.String()
	}
	run("init", "--vault", vaultDir)
	run("write", "prefers dark", "--kind", "preference", "--key", "editor.theme", "--scope", "shared")
	if _, errOut := run("write", "prefers dark", "--kind", "preference", "--key", "theme", "--scope", "shared"); !strings.Contains(errOut, `Key "theme" is new here, but editor.theme looks like the same subject`) {
		t.Errorf("a new key resembling a live one must be pointed out, got %q", errOut)
	}
	if _, errOut := run("write", "prefers light", "--kind", "preference", "--key", "theme", "--scope", "shared"); strings.Contains(errOut, "is new here") {
		t.Errorf("a key already in use is not new, got %q", errOut)
	}

	out, _ := run("keys", "--json")
	var got struct {
		Keys []struct {
			Key     string `json:"key"`
			Current int    `json:"current"`
			Total   int    `json:"total"`
		} `json:"keys"`
		Drift []struct {
			A string `json:"a"`
			B string `json:"b"`
		} `json:"drift"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("keys --json: %v\n%s", err, out)
	}
	if len(got.Keys) != 2 || len(got.Drift) != 1 || got.Drift[0].A != "editor.theme" || got.Drift[0].B != "theme" {
		t.Errorf("want two keys and one pair, got %+v", got)
	}
	var in struct {
		KeyDrift []any `json:"key_drift"`
	}
	insights, _ := run("insights", "--json")
	json.Unmarshal([]byte(insights), &in)
	if len(in.KeyDrift) != 1 {
		t.Errorf("insights must carry the pair for the widget, got %s", insights)
	}
}
