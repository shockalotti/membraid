// Package vault is the cold store: plain markdown files a human can read,
// grep, edit and delete without this program's help (SPEC §4).
//
// That is the point, not a side effect. Every other design choice here defers
// to it: no proprietary format, no timestamp-prefixed filenames, no directory
// an editor cannot browse. If you can `cat` a file you can read your memory,
// and if you can edit a line you can correct it.
package vault

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Folders carry human organisation and nothing else: `type` in frontmatter is
// what routes a concept, and it is advisory (SPEC §4.1). A concept filed in the
// "wrong" folder still works.
var Folders = []string{
	"facts", "rules", "decisions", "procedures",
	"preferences", "people", "projects", "archive",
}

// HotDir holds the wire log. Dot-prefixed so Obsidian hides it: it is
// engine-owned bookkeeping, not something to browse.
const HotDir = ".hot"

// Types is the concept vocabulary (SPEC §4.3). Small and closed: `type` routes
// retrieval and pairs with `scope` and `key` as a concept's identity.
var Types = []string{
	"fact", "rule", "decision", "procedure", "preference", "person", "project",
}

// Status values (SPEC §4.4). `draft` is what an agent may write; only a human
// edit promotes to `stable`. Nothing here requires that edit to ever happen -
// a draft is retrievable, just rank-penalised, and decays like anything else.
const (
	StatusDraft      = "draft"
	StatusStable     = "stable"
	StatusDeprecated = "deprecated"
	StatusArchived   = "archived"
)

// ScopeShared is the cross-project bucket: human-curated knowledge that should
// surface everywhere (SPEC §17).
const ScopeShared = "shared"

// Concept is one markdown file: YAML frontmatter plus a prose body.
type Concept struct {
	Type       string   `yaml:"type"`
	Title      string   `yaml:"title"`
	Summary    string   `yaml:"summary,omitempty"`
	Status     string   `yaml:"status,omitempty"`
	Scope      string   `yaml:"scope,omitempty"`
	Key        string   `yaml:"key,omitempty"`
	Author     string   `yaml:"author,omitempty"`
	Created    string   `yaml:"created"`
	Updated    string   `yaml:"updated,omitempty"`
	Tags       []string `yaml:"tags,omitempty"`
	Related    []string `yaml:"related,omitempty"`
	Sources    []string `yaml:"sources,omitempty"`
	StaleAfter string   `yaml:"stale_after,omitempty"`

	Body string `yaml:"-"`
	Path string `yaml:"-"` // vault-relative, set on read
}

// Vault is a directory of markdown files. It is deliberately not a database.
type Vault struct{ root string }

func Open(root string) *Vault { return &Vault{root: root} }

func (v *Vault) Root() string    { return v.root }
func (v *Vault) HotPath() string { return filepath.Join(v.root, HotDir) }

// IndexPath keeps the hot index inside the vault's engine-owned directory
// rather than beside the vault. Putting it beside would drop a SQLite file
// into whatever contains the vault - for a folder inside an Obsidian vault,
// that is the middle of someone's notes.
func (v *Vault) IndexPath() string { return filepath.Join(v.root, HotDir, "index.db") }

// Init creates the skeleton.
//
// An existing EMPTY directory is adopted rather than refused: pointing at a
// folder inside an Obsidian vault is a first-class way to use this, and people
// make that folder before they think to run init. An existing directory with
// anything in it is refused, because re-initialising over live notes is never
// what anyone meant.
func (v *Vault) Init() error {
	if entries, err := os.ReadDir(v.root); err == nil {
		if len(entries) > 0 {
			return fmt.Errorf("vault: %s already has files in it (refusing to initialise over them).\n"+
				"To use a folder inside an existing Obsidian vault, point at a new empty subfolder:\n"+
				"  membraid init --vault '%s/Agent Memory'", v.root, strings.TrimRight(v.root, "/"))
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, d := range append(Folders, HotDir) {
		if err := os.MkdirAll(filepath.Join(v.root, d), 0o700); err != nil {
			return fmt.Errorf("vault: create %s: %w", d, err)
		}
	}
	readme := "# Memory\n\nAgent memory, stored as plain markdown. Every file here is\n" +
		"readable, greppable and editable by hand - that is the point.\n\n" +
		"- Folders are for your benefit; `type` in frontmatter is what routes a note.\n" +
		"- `status: draft` means an agent wrote it and nobody has confirmed it.\n" +
		"  Change it to `stable` if it is right. Delete the file if it is wrong.\n" +
		"- `scope: shared` makes a note surface in every project.\n" +
		"- `log.md` is a running digest of what the engine changed.\n" +
		"- `.hot/` is engine bookkeeping. Leave it alone.\n\n" +
		"Nothing here needs tending. Tending it just makes it better.\n"
	if err := os.WriteFile(filepath.Join(v.root, "index.md"), []byte(readme), 0o600); err != nil {
		return err
	}
	// The wire log is history and belongs in git. The index is a rebuildable
	// cache and does not.
	ignore := "# The index is rebuilt from the wire log and the vault.\nindex.db\nindex.db-wal\nindex.db-shm\n"
	if err := os.WriteFile(filepath.Join(v.root, HotDir, ".gitignore"), []byte(ignore), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(v.root, "log.md"), []byte("# Change log\n\nNewest first.\n"), 0o600)
}

var frontmatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?`)

// Parse reads a concept. A file with no frontmatter is still a concept: a
// human may have dropped a plain note in, and refusing to read it would punish
// exactly the hand-editing this store exists to allow.
func Parse(path string, raw []byte) (*Concept, error) {
	c := &Concept{Path: path}
	m := frontmatterRe.FindSubmatch(raw)
	if m == nil {
		c.Body = strings.TrimSpace(string(raw))
		c.Title = titleFromPath(path)
		return c, c.normalise()
	}
	if err := yaml.Unmarshal(m[1], c); err != nil {
		return nil, fmt.Errorf("vault: %s: frontmatter: %w", path, err)
	}
	c.Body = strings.TrimSpace(string(raw[len(m[0]):]))
	return c, c.normalise()
}

// normalise fills what a hand-written file may omit. It never rejects: a file
// a human can read is a file this package must accept.
func (c *Concept) normalise() error {
	if c.Title == "" {
		c.Title = titleFromPath(c.Path)
	}
	if c.Type == "" {
		c.Type = typeFromPath(c.Path)
	}
	if c.Status == "" {
		c.Status = StatusStable // a file present in the vault is kept unless it says otherwise
	}
	if c.Scope == "" {
		c.Scope = ScopeShared
	}
	if c.Created == "" {
		c.Created = time.Now().UTC().Format("2006-01-02")
	}
	return nil
}

func titleFromPath(p string) string {
	base := strings.TrimSuffix(filepath.Base(p), ".md")
	return strings.TrimSpace(strings.ReplaceAll(base, "-", " "))
}

// typeFromPath reads the folder as a hint. Advisory only (SPEC §4.1): a file
// in decisions/ that says `type: rule` is a rule.
func typeFromPath(p string) string {
	dir := filepath.Base(filepath.Dir(p))
	singular := map[string]string{
		"facts": "fact", "rules": "rule", "decisions": "decision",
		"procedures": "procedure", "preferences": "preference",
		"people": "person", "projects": "project",
	}
	if t, ok := singular[dir]; ok {
		return t
	}
	return "fact"
}

// Marshal renders a concept back to markdown. Field order is fixed so that a
// file the engine rewrites does not churn in `git diff` against one a human
// wrote.
func (c *Concept) Marshal() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("---\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	_ = enc.Close()
	b.WriteString("---\n")
	if c.Body != "" {
		b.WriteString(c.Body)
		b.WriteString("\n")
	}
	return b.Bytes(), nil
}

// Write saves a concept at its Path, creating the folder if needed.
func (v *Vault) Write(c *Concept) error {
	if c.Path == "" {
		return fmt.Errorf("vault: concept has no path")
	}
	buf, err := c.Marshal()
	if err != nil {
		return err
	}
	full := filepath.Join(v.root, c.Path)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return err
	}
	return os.WriteFile(full, buf, 0o600)
}

// Read loads one concept by vault-relative path.
func (v *Vault) Read(rel string) (*Concept, error) {
	raw, err := os.ReadFile(filepath.Join(v.root, rel))
	if err != nil {
		return nil, err
	}
	return Parse(rel, raw)
}

// List walks every concept in the vault, skipping the wire log and the two
// reserved files. Sorted for deterministic output.
func (v *Vault) List() ([]*Concept, error) {
	var out []*Concept
	err := filepath.WalkDir(v.root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == HotDir || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(v.root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".md") || rel == "index.md" || rel == "log.md" {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		c, perr := Parse(rel, raw)
		if perr != nil {
			return perr
		}
		out = append(out, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Slug turns a title into a human-meaningful filename. No timestamps, no
// invented suffixes (SPEC §4.1): a person should be able to guess the filename
// from the title and find it with tab-completion.
func Slug(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "untitled"
	}
	if len(s) > 60 {
		if cut := strings.LastIndex(s[:60], "-"); cut > 20 {
			s = s[:cut]
		} else {
			s = s[:60]
		}
	}
	return s
}

// FolderFor maps a type to its conventional folder. Human organisation only.
func FolderFor(typ string) string {
	plural := map[string]string{
		"fact": "facts", "rule": "rules", "decision": "decisions",
		"procedure": "procedures", "preference": "preferences",
		"person": "people", "project": "projects",
	}
	if f, ok := plural[typ]; ok {
		return f
	}
	return "facts"
}

// AppendLog prepends a dated line to log.md, newest first. This is the digest
// a human browsing in Obsidian sees; git carries the authoritative history.
func (v *Vault) AppendLog(line string) error {
	p := filepath.Join(v.root, "log.md")
	existing, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	const header = "# Change log\n\nNewest first.\n"
	body := strings.TrimPrefix(string(existing), header)
	entry := fmt.Sprintf("- %s %s\n", time.Now().UTC().Format("2006-01-02"), line)
	return os.WriteFile(p, []byte(header+entry+body), 0o600)
}
