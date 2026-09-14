package scope

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Same-named projects in different places must not share a brain.
func TestSlugDisambiguatesSameBasename(t *testing.T) {
	a, b := Slug("/home/w/work/api"), Slug("/home/w/personal/api")
	if a == b {
		t.Fatalf("collision: %s", a)
	}
}

// The identity carries NO readable name. An earlier version prefixed the
// basename to keep slugs legible, which silently defeated the whole point:
// renaming a directory changed the slug even when the project had not moved.
// The name lives in the scope registry and is shown in output instead.
func TestIdentityCarriesNoName(t *testing.T) {
	if got := Slug("/home/w/work/api"); strings.Contains(got, "api") {
		t.Errorf("identity must not embed the directory name: %s", got)
	}
	if Name("/home/w/work/api") != "api" {
		t.Errorf("the readable name comes from Name(), got %q", Name("/home/w/work/api"))
	}
}

// The property that matters: a git project keeps its identity when it moves.
// People rename and relocate directories constantly, and a path-derived
// identity strands every memory the moment they do.
func TestGitProjectSurvivesAMove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	before := filepath.Join(base, "before")
	if err := os.MkdirAll(before, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", before}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	got := FromDir(before)
	if !strings.HasPrefix(got, "g") {
		t.Fatalf("a git project should get a commit-derived identity, got %q", got)
	}

	after := filepath.Join(base, "renamed-and-moved")
	if err := os.Rename(before, after); err != nil {
		t.Fatal(err)
	}
	if moved := FromDir(after); moved != got {
		t.Errorf("identity must survive a move: %q -> %q", got, moved)
	}
	if Name(after) != "renamed-and-moved" {
		t.Errorf("the name should follow the directory: %q", Name(after))
	}
}

// You are in the same project from its root or three directories down.
func TestDerivesFromGitRootNotCwd(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "internal", "pkg")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if FromDir(deep) != FromDir(root) {
		t.Errorf("subdirectory must resolve to the project: %s vs %s", FromDir(deep), FromDir(root))
	}
}

func TestPriorityOrder(t *testing.T) {
	t.Setenv("MEMBRAID_SCOPE", "from-env")
	if got := Resolve("explicit-wins"); got != "explicit-wins" {
		t.Errorf("explicit must win: %s", got)
	}
	if got := Resolve(""); got != "from-env" {
		t.Errorf("env is second: %s", got)
	}
}

// Writing from home means no project was in mind; burying it under "home"
// would make it unfindable.
func TestHomeIsNotAProject(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	if got := FromDir(home); got != "" {
		t.Errorf("home must not be a project scope, got %q", got)
	}
}

// A write with no project is quarantined, never filed under shared, while a
// read with no project still reads shared; "*" and "unscoped" cannot be chosen
// for a write, whether passed or set in the environment.
func TestResolveWriteQuarantinesInsteadOfShared(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MEMBRAID_SCOPE", "")
	t.Chdir(home)

	if got, err := ResolveWrite(""); err != nil || got != Unscoped {
		t.Errorf("a write from home must land in unscoped, got %q %v", got, err)
	}
	if got := Resolve(""); got != Shared {
		t.Errorf("a read from home still reads shared, got %q", got)
	}
	if got, err := ResolveWrite("shared"); err != nil || got != Shared {
		t.Errorf("an explicit shared write is allowed, got %q %v", got, err)
	}
	for _, bad := range []string{"*", Unscoped} {
		if _, err := ResolveWrite(bad); err == nil {
			t.Errorf("writing to %q must be refused", bad)
		}
		t.Setenv("MEMBRAID_SCOPE", bad)
		if _, err := ResolveWrite(""); err == nil {
			t.Errorf("MEMBRAID_SCOPE=%q must be refused for writes", bad)
		}
		t.Setenv("MEMBRAID_SCOPE", "")
	}
}
