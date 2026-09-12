package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// machine is one computer: its own vault clone, its own settings, its own name.
type machine struct {
	t     *testing.T
	bin   string
	vault string
	cfg   string
}

func (m machine) run(args ...string) string {
	m.t.Helper()
	cmd := exec.Command(m.bin, args...)
	cmd.Env = append(os.Environ(), "MEMBRAID_VAULT="+m.vault, "MEMBRAID_CONFIG_DIR="+m.cfg,
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		m.t.Fatalf("membraid %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// The whole point of sync, end to end through the real binary: two machines,
// one remote, memory written on either shows up on both, and a subject both
// changed while offline converges on one answer instead of conflicting.
func TestTwoMachinesShareOneBrain(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "membraid")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	remote := filepath.Join(root, "remote.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}

	win := machine{t, bin, filepath.Join(root, "win", "vault"), filepath.Join(root, "win", "cfg")}
	arch := machine{t, bin, filepath.Join(root, "arch", "vault"), filepath.Join(root, "arch", "cfg")}

	win.run("init")
	win.run("config", "set", "host", "windows")
	git(t, win.vault, "init", "-q", "-b", "main")
	git(t, win.vault, "add", "-A")
	git(t, win.vault, "commit", "-q", "-m", "vault")
	git(t, win.vault, "remote", "add", "origin", remote)
	git(t, win.vault, "push", "-q", "-u", "origin", "main")

	if out, err := exec.Command("git", "clone", "-q", remote, arch.vault).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v %s", err, out)
	}
	arch.run("config", "set", "host", "omarchy")

	// Written on Windows, pushed, pulled on Omarchy.
	win.run("write", "deploys to railway", "--key", "deploy.target", "--kind", "project_param", "--scope", "shared")
	win.run("sync")
	arch.run("sync")
	if got := arch.run("get", "deploy.target", "--scope", "shared"); !strings.Contains(got, "railway") {
		t.Fatalf("omarchy did not receive the windows write: %q", got)
	}

	// Both change the same subject between syncs. Omarchy writes second, so
	// its answer is newest and must win on BOTH machines, with no conflict.
	win.run("write", "deploys to render", "--key", "deploy.target", "--kind", "project_param", "--scope", "shared")
	arch.run("write", "deploys to fly.io", "--key", "deploy.target", "--kind", "project_param", "--scope", "shared")
	win.run("sync")
	arch.run("sync") // rebases onto windows' push: one log file per machine, so nothing collides
	win.run("sync")
	for name, m := range map[string]machine{"windows": win, "omarchy": arch} {
		if got := m.run("get", "deploy.target", "--scope", "shared"); !strings.Contains(got, "fly.io") {
			t.Errorf("%s: want the newest answer (fly.io), got %q", name, got)
		}
	}
	if h := win.run("history", "deploy.target", "--scope", "shared"); strings.Count(h, "project_param") != 3 {
		t.Errorf("all three answers must survive as history:\n%s", h)
	}

	// A task finished on one machine is finished on the other.
	arch.run("write", "migrating the auth service", "--key", "task.auth", "--kind", "task_state", "--scope", "shared")
	arch.run("sync")
	win.run("sync")
	win.run("done", "task.auth", "--scope", "shared")
	win.run("sync")
	arch.run("sync")
	var st struct {
		Doing []any `json:"doing"`
		Sync  struct {
			LastSuccess string `json:"last_success"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(arch.run("status", "--json", "--scope", "shared")), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Doing) != 0 {
		t.Errorf("task finished on windows is still open on omarchy: %v", st.Doing)
	}
	if st.Sync.LastSuccess == "" {
		t.Error("status must report when this machine last synced")
	}

	// Each machine appended only to its own file.
	files, _ := filepath.Glob(filepath.Join(arch.vault, ".hot", "writes-*.jsonl"))
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	joined := strings.Join(names, " ")
	if !strings.Contains(joined, "-windows.jsonl") || !strings.Contains(joined, "-omarchy.jsonl") {
		t.Errorf("want one log file per machine, got %v", names)
	}
}
