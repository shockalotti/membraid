// Package distill writes the vault's readable notes: one markdown file per
// subject that agents keep coming back to (SPEC §8).
//
// Memories live in the append-only log, which people do not read. A subject
// written more than once, or in more than one session, has proven it matters,
// so it becomes a concept file a person can open, grep, or edit in Obsidian:
// the current answer, and every earlier answer with when and who.
//
// Files are drafts, and they belong to the person as soon as they touch them.
// membraid rewrites a file only while it is exactly what membraid last wrote;
// an edited or promoted file is never overwritten, though new memories on its
// subject are still linked to it.
package distill

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shockalotti/membraid/internal/index"
	"github.com/shockalotti/membraid/internal/vault"
)

// Result is what one pass did. Paths lists files created or updated.
type Result struct {
	Subjects int      `json:"subjects"`
	Created  int      `json:"created"`
	Updated  int      `json:"updated"`
	Kept     int      `json:"kept"` // edited by a person: linked, never rewritten
	Linked   int      `json:"linked"`
	Paths    []string `json:"paths,omitempty"`
}

// conceptType maps memory kinds to concept types (SPEC §8 step 4).
var conceptType = map[string]string{
	index.KindPreference:   "preference",
	index.KindProjectParam: "project",
	index.KindInsight:      "fact",
}

// Run distills every qualifying subject. names maps scope ids to readable
// project names, used for folders and titles.
func Run(ix *index.Index, v *vault.Vault, names map[string]string) (*Result, error) {
	subjects, err := ix.Subjects()
	if err != nil {
		return nil, err
	}
	existing, err := v.List()
	if err != nil {
		return nil, err
	}
	// A covering concept is found by identity, not by path (SPEC §6.2): a
	// person may have moved or renamed the file.
	covering := map[string]string{}
	for _, c := range existing {
		if c.Key != "" {
			covering[identity(c.Scope, c.Type, index.NormalizeKey(c.Key))] = c.Path
		}
	}

	r := &Result{Subjects: len(subjects)}
	for _, s := range subjects {
		typ, ok := conceptType[s.Kind]
		if !ok {
			continue
		}
		path := covering[identity(s.Scope, typ, s.Key)]
		if path == "" {
			path = defaultPath(s, typ, names)
		}
		c := render(s, typ, path, names)
		buf, err := c.Marshal()
		if err != nil {
			return r, err
		}
		action, err := write(ix, v, path, buf)
		if err != nil {
			return r, err
		}
		switch action {
		case created:
			r.Created++
			r.Paths = append(r.Paths, path)
		case updated:
			r.Updated++
			r.Paths = append(r.Paths, path)
		case kept:
			r.Kept++
		}
		ids := make([]string, len(s.Rows))
		for i, row := range s.Rows {
			ids[i] = row.ID
		}
		n, err := ix.LinkConcept(ids, path)
		if err != nil {
			return r, err
		}
		r.Linked += n
	}
	sort.Strings(r.Paths)
	return r, nil
}

func identity(scope, typ, key string) string { return scope + "\x00" + typ + "\x00" + key }

// defaultPath places a new concept: shared subjects in the type's folder,
// project subjects in a folder named for the project, so two projects with the
// same key never collide.
func defaultPath(s index.Subject, typ string, names map[string]string) string {
	dir := vault.FolderFor(typ)
	if s.Scope != index.ScopeShared {
		dir = filepath.Join(dir, vault.Slug(projectName(s.Scope, names)))
	}
	return filepath.ToSlash(filepath.Join(dir, vault.Slug(s.Key)+".md"))
}

func projectName(scope string, names map[string]string) string {
	if n := names[scope]; n != "" {
		return n
	}
	return scope
}

func render(s index.Subject, typ, path string, names map[string]string) *vault.Concept {
	cur := s.Current()
	oldest := s.Rows[len(s.Rows)-1]
	title := humanKey(s.Key)
	if s.Scope != index.ScopeShared {
		title += " (" + projectName(s.Scope, names) + ")"
	}

	var b strings.Builder
	b.WriteString(strings.TrimSpace(cur.Content))
	b.WriteString("\n\n## History\n\n")
	for _, row := range s.Rows {
		fmt.Fprintf(&b, "- %s, %s: %s", day(row.At), row.Source, oneLine(row.Content))
		if row.Current {
			b.WriteString(" (current)")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n_Written by membraid from what your agents recorded. Edit it freely: once you change this file, membraid will not overwrite it._")

	return &vault.Concept{
		Type:    typ,
		Title:   title,
		Summary: clip(oneLine(cur.Content), 160),
		Status:  vault.StatusDraft,
		Scope:   s.Scope,
		Key:     s.Key,
		Author:  cur.Source + " agent",
		Created: day(oldest.At),
		Updated: day(cur.At),
		Body:    b.String(),
		Path:    path,
	}
}

type action int

const (
	unchanged action = iota
	created
	updated
	kept
)

// write puts buf at path unless a person owns the file. A file is membraid's
// only while its content hashes to what membraid last wrote there.
func write(ix *index.Index, v *vault.Vault, path string, buf []byte) (action, error) {
	full := filepath.Join(v.Root(), filepath.FromSlash(path))
	want := hash(buf)
	raw, err := os.ReadFile(full)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := writeFile(full, buf); err != nil {
			return unchanged, err
		}
		return created, ix.ConceptWritten(path, want)
	case err != nil:
		return unchanged, err
	}
	have := hash(raw)
	if have == want {
		return unchanged, ix.ConceptWritten(path, want)
	}
	if have != ix.WrittenConcept(path) {
		return kept, nil
	}
	if err := writeFile(full, buf); err != nil {
		return unchanged, err
	}
	return updated, ix.ConceptWritten(path, want)
}

func writeFile(full string, buf []byte) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return err
	}
	return os.WriteFile(full, buf, 0o600)
}

func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// humanKey turns deploy.target into "Deploy target".
func humanKey(key string) string {
	words := strings.FieldsFunc(key, func(r rune) bool { return r == '.' || r == '-' || r == '_' })
	s := strings.Join(words, " ")
	if s == "" {
		return key
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func day(at string) string {
	if len(at) >= 10 {
		return at[:10]
	}
	return at
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
