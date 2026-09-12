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
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Shared surfaces in every project.
const Shared = "shared"

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Resolve picks the scope for a call, in priority order:
//
//  1. an explicit --scope
//  2. MEMBRAID_SCOPE in the environment
//  3. the current project, derived from the git root (or cwd)
//  4. shared
//
// Deriving from the git root rather than the working directory matters: you
// are in the same project whether you are at its root or three directories
// down, and a memory written from a subdirectory belongs to the same brain.
func Resolve(explicit string) string {
	if s := strings.TrimSpace(explicit); s != "" {
		return s
	}
	if s := strings.TrimSpace(os.Getenv("MEMBRAID_SCOPE")); s != "" {
		return s
	}
	if s := FromDir(""); s != "" {
		return s
	}
	return Shared
}

// FromDir derives a project slug from dir, or the working directory if empty.
// Returns "" when there is nothing to derive from.
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
	base := strings.ToLower(filepath.Base(abs))
	base = strings.Trim(nonSlug.ReplaceAllString(base, "-"), "-")
	if base == "" {
		base = "project"
	}
	sum := sha256.Sum256([]byte(abs))
	return base + "-" + hex.EncodeToString(sum[:])[:8]
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
