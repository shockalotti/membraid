package vaultsync

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolate git from the developer's global config, so the fallback identity
// path is exercised and nothing personal leaks into test repos.
func isolate(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// twoMachines builds a bare remote with one commit, and two clones of it.
func twoMachines(t *testing.T) (a, b string) {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	a, b = filepath.Join(root, "a"), filepath.Join(root, "b")
	exec.Command("git", "init", "-q", "--bare", "-b", "main", remote).Run()
	os.MkdirAll(seed, 0o755)
	sh(t, seed, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(seed, "index.md"), []byte("# memory\n"), 0o600)
	sh(t, seed, "add", "-A")
	sh(t, seed, "commit", "-q", "-m", "seed")
	sh(t, seed, "remote", "add", "origin", remote)
	sh(t, seed, "push", "-q", "-u", "origin", "main")
	for _, d := range []string{a, b} {
		if out, err := exec.Command("git", "clone", "-q", remote, d).CombinedOutput(); err != nil {
			t.Fatalf("clone: %v %s", err, out)
		}
	}
	return a, b
}

func run(t *testing.T, dir, host string) *Result {
	t.Helper()
	res, err := Run(context.Background(), dir, Options{Host: host})
	if err != nil {
		t.Fatalf("sync %s: %v", host, err)
	}
	return res
}

func TestWriteOnOneMachineArrivesOnTheOther(t *testing.T) {
	isolate(t)
	a, b := twoMachines(t)
	os.WriteFile(filepath.Join(a, "note.md"), []byte("from a\n"), 0o600)

	if r := run(t, a, "a"); !r.Committed || !r.Pushed {
		t.Fatalf("a should commit and push: %+v", r)
	}
	if r := run(t, b, "b"); !r.Pulled {
		t.Fatalf("b should pull: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(b, "note.md")); string(got) != "from a\n" {
		t.Errorf("b did not receive the note: %q", got)
	}
}

// The case per-host log files exist for: both machines write between syncs.
// Different files never conflict, whichever order the syncs happen in.
func TestBothMachinesWritingBetweenSyncsDoesNotConflict(t *testing.T) {
	isolate(t)
	a, b := twoMachines(t)
	os.MkdirAll(filepath.Join(a, ".hot"), 0o700)
	os.MkdirAll(filepath.Join(b, ".hot"), 0o700)
	os.WriteFile(filepath.Join(a, ".hot", "writes-2026-09-a.jsonl"), []byte("line from a\n"), 0o600)
	os.WriteFile(filepath.Join(b, ".hot", "writes-2026-09-b.jsonl"), []byte("line from b\n"), 0o600)

	run(t, a, "a")
	if r := run(t, b, "b"); !r.Pulled || !r.Pushed {
		t.Fatalf("b should rebase onto a and push: %+v", r)
	}
	run(t, a, "a")
	for _, dir := range []string{a, b} {
		for _, f := range []string{"writes-2026-09-a.jsonl", "writes-2026-09-b.jsonl"} {
			if _, err := os.Stat(filepath.Join(dir, ".hot", f)); err != nil {
				t.Errorf("%s missing %s", filepath.Base(dir), f)
			}
		}
	}
}

// When both machines really did change one file, sync stops, says which file,
// and leaves the repo clean with the local commit intact - never mid-rebase.
func TestRealConflictAbortsCleanly(t *testing.T) {
	isolate(t)
	a, b := twoMachines(t)
	os.WriteFile(filepath.Join(a, "index.md"), []byte("# edited on a\n"), 0o600)
	os.WriteFile(filepath.Join(b, "index.md"), []byte("# edited on b\n"), 0o600)
	run(t, a, "a")

	_, err := Run(context.Background(), b, Options{Host: "b"})
	var ce *ConflictError
	if !errors.As(err, &ce) || len(ce.Files) != 1 || ce.Files[0] != "index.md" {
		t.Fatalf("want a conflict naming index.md, got %v", err)
	}
	if _, serr := os.Stat(filepath.Join(b, ".git", "rebase-merge")); serr == nil {
		t.Error("repo left mid-rebase")
	}
	if got, _ := os.ReadFile(filepath.Join(b, "index.md")); string(got) != "# edited on b\n" {
		t.Errorf("local edit lost: %q", got)
	}
	if log := sh(t, b, "log", "--oneline", "-1"); !strings.Contains(log, "sync from b") {
		t.Errorf("local commit not intact: %s", log)
	}
}

func TestConcurrentSyncIsSkippedNotRaced(t *testing.T) {
	isolate(t)
	a, _ := twoMachines(t)
	unlock, ok, err := tryLock(filepath.Join(a, ".git", "membraid-sync.lock"))
	if err != nil || !ok {
		t.Fatalf("could not take lock: %v", err)
	}
	defer unlock()
	if r := run(t, a, "a"); r.Skipped == "" {
		t.Errorf("a second sync must skip while the lock is held: %+v", r)
	}
}

func TestNotARepoIsASkip(t *testing.T) {
	isolate(t)
	if r := run(t, t.TempDir(), "a"); r.Skipped == "" {
		t.Errorf("want skip, got %+v", r)
	}
}

func TestNoRemoteStillCommits(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	sh(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "x.md"), []byte("x\n"), 0o600)
	r := run(t, dir, "solo")
	if !r.Committed || r.Remote || r.Pushed {
		t.Errorf("want a local commit only: %+v", r)
	}
}
