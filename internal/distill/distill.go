// Package distill writes the vault's readable notes: one markdown file per
// subject that agents keep coming back to (SPEC §8).
//
// Memories live in the append-only log, which people do not read. A subject
// written more than once, or in more than one session, has proven it matters,
// so it becomes a note a person can open or grep: the current answer, and every
// earlier answer with when and who.
//
// Notes are a view of memory, not a second source of it. Agents read the
// memories and never the notes, so what agents know changes when a memory is
// written, not when a note is edited (SPEC changelog v1.17.1). A note a person
// edits is still left alone, and one a person deletes is not written again.
//
// Which notes membraid may rewrite is carried in each note: a hash, in its
// frontmatter, of everything else in the file. Every machine, and every rebuilt
// index, can tell an untouched note from an edited one without having written
// it.
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
	Subjects int `json:"subjects"`
	Created  int `json:"created"`
	Updated  int `json:"updated"`
	// Retired counts notes marked no longer current: every memory on the
	// subject was forgotten, finished or closed.
	Retired int `json:"retired"`
	// Kept counts notes edited by hand: linked, never rewritten.
	Kept int `json:"kept"`
	// LeftDeleted counts subjects whose note a person deleted: not written again.
	LeftDeleted int `json:"left_deleted"`
	Linked      int `json:"linked"`
	// Unreadable names notes whose frontmatter could not be read; they are
	// skipped, and never overwritten.
	Unreadable []string `json:"unreadable,omitempty"`
	Paths      []string `json:"paths,omitempty"`
}

// conceptType maps memory kinds to concept types (SPEC §8 step 4).
var conceptType = map[string]string{
	index.KindPreference:   "preference",
	index.KindProjectParam: "project",
	index.KindInsight:      "fact",
}

// footer starts with the text vaultsync recognises to settle a sync conflict on
// an untouched note; a test in this package checks the two agree.
const footer = "_Written by membraid from what your agents recorded. Agents read the memories, not this note: to change what they know, write the right answer with the same key (membraid write, or Correct in the widget). If you edit this note, membraid leaves it alone._"

// Run distills every qualifying subject, and marks the notes of retired ones.
// names maps scope ids to readable project names, used for folders and titles.
func Run(ix *index.Index, v *vault.Vault, names map[string]string) (*Result, error) {
	subjects, err := ix.Subjects()
	if err != nil {
		return nil, err
	}
	existing, unreadable, err := v.List()
	if err != nil {
		return nil, err
	}
	// A covering note is found by identity, not by path (SPEC §6.2): a person
	// may have moved or renamed the file. taken maps every note's path to the
	// identity it holds ("" for a note without a key).
	covering := map[string]string{}
	taken := map[string]string{}
	for _, c := range existing {
		id := ""
		if c.Key != "" {
			id = identity(c.Scope, c.Type, index.NormalizeKey(c.Key))
			covering[id] = c.Path
		}
		taken[c.Path] = id
	}
	isUnreadable := map[string]bool{}
	for _, p := range unreadable {
		isUnreadable[p] = true
	}

	r := &Result{Subjects: len(subjects), Unreadable: unreadable}
	for _, s := range subjects {
		typ, ok := conceptType[s.Kind]
		if !ok {
			continue
		}
		id := identity(s.Scope, typ, s.Key)
		path := covering[id]
		if path == "" {
			switch linked := linkedPath(v, s, isUnreadable); linked {
			case linkedUnreadable:
				r.Kept++
				continue
			case linkedDeleted:
				r.LeftDeleted++
				continue
			}
			path = placement(s, typ, names, taken, isUnreadable)
		}
		action, err := write(ix, v, path, render(s, typ, path, names))
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
		taken[path] = id
		covering[id] = path
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

	retired, err := ix.RetiredSubjects()
	if err != nil {
		return r, err
	}
	for _, s := range retired {
		typ, ok := conceptType[s.Kind]
		if !ok {
			continue
		}
		// Only an existing note needs correcting; a subject retired before it
		// had one, or whose note was deleted, gets nothing.
		path := covering[identity(s.Scope, typ, s.Key)]
		if path == "" {
			continue
		}
		action, err := write(ix, v, path, renderRetired(s, typ, path, names))
		if err != nil {
			return r, err
		}
		switch action {
		case updated:
			r.Retired++
			r.Paths = append(r.Paths, path)
		case kept:
			r.Kept++
		}
	}
	sort.Strings(r.Paths)
	return r, nil
}

func identity(scope, typ, key string) string { return scope + "\x00" + typ + "\x00" + key }

type linkState int

const (
	linkedNone linkState = iota
	linkedDeleted
	linkedUnreadable
)

// linkedPath looks at the note a subject's memories were linked to, for a
// subject with no readable note under any name. A linked note that is gone was
// deleted by hand, and writing it again would undo that within half an hour. A
// linked note whose frontmatter cannot be read is still there and still the
// person's: a second copy must not appear beside it.
func linkedPath(v *vault.Vault, s index.Subject, unreadable map[string]bool) linkState {
	for _, row := range s.Rows {
		if row.Concept == "" {
			continue
		}
		if unreadable[row.Concept] {
			return linkedUnreadable
		}
		if _, err := os.Stat(filepath.Join(v.Root(), filepath.FromSlash(row.Concept))); errors.Is(err, fs.ErrNotExist) {
			return linkedDeleted
		}
	}
	return linkedNone
}

// placement is where a new note goes: the type's folder, and inside it a folder
// named for the project. When that path already holds a different note (two
// projects with the same folder name, or a person's own note), the folder
// carries the scope id as well, so the two never overwrite each other.
func placement(s index.Subject, typ string, names map[string]string, taken map[string]string, unreadable map[string]bool) string {
	path := defaultPath(s, typ, names, false)
	if other, ok := taken[path]; (ok && other != identity(s.Scope, typ, s.Key)) || unreadable[path] {
		path = defaultPath(s, typ, names, true)
	}
	return path
}

func defaultPath(s index.Subject, typ string, names map[string]string, withScopeID bool) string {
	dir := vault.FolderFor(typ)
	name := vault.Slug(s.Key)
	if s.Scope != index.ScopeShared {
		folder := vault.Slug(projectName(s.Scope, names))
		if withScopeID {
			folder += "-" + vault.Slug(s.Scope)
		}
		dir = filepath.Join(dir, folder)
	} else if withScopeID {
		name += "-shared"
	}
	return filepath.ToSlash(filepath.Join(dir, name+".md"))
}

func projectName(scope string, names map[string]string) string {
	if n := names[scope]; n != "" {
		return n
	}
	return scope
}

func title(s index.Subject, names map[string]string) string {
	t := humanKey(s.Key)
	if s.Scope != index.ScopeShared {
		t += " (" + projectName(s.Scope, names) + ")"
	}
	return t
}

func history(b *strings.Builder, s index.Subject) {
	b.WriteString("\n\n## History\n\n")
	for _, row := range s.Rows {
		fmt.Fprintf(b, "- %s, %s: %s", day(row.At), row.Source, oneLine(row.Content))
		if row.Current {
			b.WriteString(" (current)")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n" + footer)
}

func render(s index.Subject, typ, path string, names map[string]string) *vault.Concept {
	cur := s.Current()
	oldest := s.Rows[len(s.Rows)-1]
	var b strings.Builder
	b.WriteString(strings.TrimSpace(cur.Content))
	history(&b, s)
	return &vault.Concept{
		Type:    typ,
		Title:   title(s, names),
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

// renderRetired is the note for a subject with no live answer: marked
// deprecated, saying so at the top, with the last answer and the history kept.
func renderRetired(s index.Subject, typ, path string, names map[string]string) *vault.Concept {
	last := s.Rows[0]
	oldest := s.Rows[len(s.Rows)-1]
	retiredOn := day(last.Until)
	if retiredOn == "" {
		retiredOn = day(last.At)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**No longer current.** The last answer was retired on %s:\n\n> %s", retiredOn, oneLine(last.Content))
	history(&b, s)
	return &vault.Concept{
		Type:    typ,
		Title:   title(s, names),
		Summary: clip("No longer current: "+oneLine(last.Content), 160),
		Status:  vault.StatusDeprecated,
		Scope:   s.Scope,
		Key:     s.Key,
		Author:  last.Source + " agent",
		Created: day(oldest.At),
		Updated: retiredOn,
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

// write puts the note at path unless a person owns the file. A note is
// membraid's while its ownership line still matches the rest of the file, or,
// for a note written before notes carried that line, while it matches what this
// index last wrote there.
func write(ix *index.Index, v *vault.Vault, path string, c *vault.Concept) (action, error) {
	rendered, err := c.Marshal()
	if err != nil {
		return unchanged, err
	}
	buf := vault.Stamp(rendered)
	full := filepath.Join(v.Root(), filepath.FromSlash(path))
	raw, err := os.ReadFile(full)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := writeFile(full, buf); err != nil {
			return unchanged, err
		}
		return created, ix.ConceptWritten(path, hash(buf))
	case err != nil:
		return unchanged, err
	}
	if string(raw) == string(buf) {
		return unchanged, nil
	}
	if !vault.Owned(raw) && hash(raw) != ix.WrittenConcept(path) {
		return kept, nil
	}
	if err := writeFile(full, buf); err != nil {
		return unchanged, err
	}
	return updated, ix.ConceptWritten(path, hash(buf))
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
