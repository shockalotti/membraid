package scope

import (
	"os"
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
	if !strings.HasPrefix(a, "api-") || !strings.HasPrefix(b, "api-") {
		t.Errorf("slug should stay legible: %s %s", a, b)
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
