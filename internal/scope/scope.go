// Package scope decides which project a memory belongs to.
//
// One vault holds everything. A project is a column value, not a directory, so
// there is exactly one brain and exactly one thing to sync. Scope is what keeps
// one project's working memory out of another's recall while letting anything
// marked shared surface everywhere.
package scope

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Shared surfaces in every project.
const Shared = "shared"

// Unscoped quarantines a write whose project could not be worked out. No
// project reads it by default, so a misconfigured agent cannot pollute every
// project through shared, and its size is the sign that something is wrong
// (SPEC 17).
const Unscoped = "unscoped"

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Resolve picks the scope for a call, in priority order:
//
//  1. an explicit --scope
//  2. MEMBRAID_SCOPE in the environment
//  3. the current project, derived from the git root (or cwd)
//  4. shared, for reading: with no project, you read the curated bucket
//
// Writes use ResolveWrite, which falls back to Unscoped instead.
//
// Deriving from the git root rather than the working directory matters: you
// are in the same project whether you are at its root or three directories
// down, and a memory written from a subdirectory belongs to the same brain.
func Resolve(explicit string) string {
	if s := strings.TrimSpace(explicit); s != "" {
		return canon(s)
	}
	if s := strings.TrimSpace(os.Getenv("MEMBRAID_SCOPE")); s != "" {
		return canon(s)
	}
	if s := FromDir(""); s != "" {
		return canon(s)
	}
	return Shared
}

var canonical func(string) string

// SetCanonical installs the lookup from a scope id to where its memories live
// now. A rescope moves a project's memories to a new id; a checkout that still
// derives the old one reads and writes the project where it went.
func SetCanonical(f func(string) string) { canonical = f }

func canon(s string) string {
	if canonical == nil || s == Shared || s == Unscoped || s == "*" {
		return s
	}
	return canonical(s)
}

// ResolveWrite picks the scope a write lands in: an explicit scope, then
// MEMBRAID_SCOPE, then the current project, and otherwise Unscoped, never
// Shared (SPEC 17). "*" means every project and is for searching, and unscoped
// can be reached but never chosen, since a caller that could write there on
// purpose would fake the one sign of misconfiguration; both are refused.
func ResolveWrite(explicit string) (string, error) {
	for _, s := range []string{strings.TrimSpace(explicit), strings.TrimSpace(os.Getenv("MEMBRAID_SCOPE"))} {
		switch s {
		case "":
			continue
		case "*":
			return "", fmt.Errorf(`scope "*" means every project and is only for searching; write to a project or to "shared"`)
		case Unscoped:
			return "", fmt.Errorf(`scope "unscoped" is where writes with no project land and cannot be chosen; write to a project or to "shared"`)
		}
		return canon(s), nil
	}
	if s := FromDir(""); s != "" {
		return canon(s), nil
	}
	return Unscoped, nil
}

// FromDir derives a project slug from dir, or the working directory if empty.
// Returns "" when there is nothing to derive from.
//
// A git project is identified by its **root commit**, not its path. People move
// and rename directories constantly, and a path-derived identity orphans every
// memory the moment they do. The root commit survives a move, a rename, a
// second clone, and a worktree - which is correct, because all of those are
// still the same project.
//
// Directories outside git fall back to the path, which cannot survive a move;
// that is what the scope registry and "membraid rescope" exist for.
func FromDir(dir string) string {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return ""
		}
		dir = wd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if root := gitRoot(abs); root != "" {
		if id := rootCommit(root); id != "" {
			// Identity is the hash ALONE. Putting the readable basename in the
			// slug looked friendlier and silently broke the whole point: rename
			// the directory and the slug changes even though the commit did
			// not. The name is registered separately and shown in output.
			return "g" + id
		}
		abs = root
	}
	if home, err := os.UserHomeDir(); err == nil && abs == home {
		// Home is not a project. Writing from ~ means you had no project in
		// mind, and quietly filing it under "home" would bury it.
		return ""
	}
	return Slug(abs)
}

// Slug is basename plus a short hash of the absolute path, so ~/work/api and
// ~/personal/api are different projects rather than one confusing bucket.
func Slug(abs string) string {
	sum := sha256.Sum256([]byte(abs))
	return "p" + hex.EncodeToString(sum[:])[:8]
}

// Name is the human-readable label for the directory a scope came from. It is
// stored in the scope registry, never in the identity, so renaming a directory
// changes what you read without moving what you own.
func Name(dir string) string {
	if dir == "" {
		dir = Dir()
	}
	if dir == "" {
		return Shared
	}
	return slugPrefix(dir)
}

// slugPrefix keeps the slug legible: you should be able to read a scope and
// know which project it is without looking anything up.
func slugPrefix(abs string) string {
	base := strings.ToLower(filepath.Base(abs))
	base = strings.Trim(nonSlug.ReplaceAllString(base, "-"), "-")
	if base == "" {
		base = "project"
	}
	return base
}

// rootCommit returns the first 8 chars of the repository's root commit, or ""
// if this is not a readable repo (no commits yet, git missing, shallow clone
// without the root).
func rootCommit(root string) string {
	cmd := exec.Command("git", "-C", root, "rev-list", "--max-parents=0", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// A repo with several root commits (grafted histories) lists them newest
	// first; the last line is the oldest and is the stable one.
	lines := strings.Fields(string(out))
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1][:8]
}

// Dir reports the directory a scope was derived from, for the registry.
func Dir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(wd)
	if err != nil {
		return ""
	}
	if root := gitRoot(abs); root != "" {
		return root
	}
	return abs
}

func gitRoot(start string) string {
	dir := start
	for {
		if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
