package index

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/shockalotti/membraid/internal/wirelog"
)

// Kinds are a closed set (SPEC §6.1). The choice is load-bearing: kind is part
// of a subject's identity, so a preference and an insight sharing a key are two
// subjects forever.
const (
	KindPreference   = "preference"
	KindProjectParam = "project_param"
	KindInsight      = "insight"
	KindTaskState    = "task_state"
)

var validKinds = map[string]bool{
	KindPreference: true, KindProjectParam: true, KindInsight: true, KindTaskState: true,
}

// ScopeShared surfaces everywhere; a workspace slug surfaces in that project.
const ScopeShared = "shared"

type Index struct {
	db  *sql.DB
	log *wirelog.Log
	now func() time.Time // injectable so decay and sweep tests are deterministic
}

// Open creates or opens the index. log may be nil for read-only use.
func Open(path string, log *wirelog.Log) (*Index, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("index: open: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("index: schema: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		db.Close()
		return nil, err
	}
	return &Index{db: db, log: log, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (ix *Index) Close() error { return ix.db.Close() }

// SetClock replaces the time source. Tests only.
func (ix *Index) SetClock(f func() time.Time) { ix.now = f }

// Memory is one hot row.
type Memory struct {
	ID         string
	Kind       string
	Key        string // "" means unkeyed
	Content    string
	Scope      string
	Source     string
	Confidence *float64
	SessionRef string
}

// WriteResult reports what a write closed, so a caller that disagrees can
// correct it by writing the old content again.
type WriteResult struct {
	ID         string
	Scope      string
	Superseded []string
}

var ErrInvalidKind = errors.New("index: unknown kind")

// normalizeKey collapses the punctuation habits of different agents onto one
// spelling (SPEC §6.3). Without this, editor.theme / Editor_Theme / editor..theme
// are three subjects and the shared brain fragments silently.
var keySep = regexp.MustCompile(`[\s._/\-]+`)

func NormalizeKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	k = keySep.ReplaceAllString(k, ".")
	k = regexp.MustCompile(`[^a-z0-9.]`).ReplaceAllString(k, "")
	return strings.Trim(k, ".")
}

// Write records a fact. If it carries a key, every current row on the same
// subject is closed first: one subject, one live answer, across every harness.
//
// The wire-log line is appended and flushed BEFORE the transaction commits, so
// the log is always a superset of the index (SPEC §3.3). A crash between the
// two leaves a logged line with no row, which replay heals; the reverse loses
// a row silently at the next rebuild.
func (ix *Index) Write(m Memory) (*WriteResult, error) {
	if !validKinds[m.Kind] {
		return nil, fmt.Errorf("%w: %q (want preference, project_param, insight or task_state)", ErrInvalidKind, m.Kind)
	}
	if strings.TrimSpace(m.Content) == "" {
		return nil, errors.New("index: empty content")
	}
	if m.Scope == "" {
		m.Scope = ScopeShared
	}
	if m.Scope == "*" {
		return nil, errors.New("index: * is a query sentinel, not a scope to write into")
	}
	if m.ID == "" {
		m.ID = newID()
	}
	m.Key = NormalizeKey(m.Key)
	now := ix.now().Format(time.RFC3339Nano)

	// Find what this closes, before writing anything.
	var closing []string
	if m.Key != "" {
		rows, err := ix.db.Query(
			`SELECT id FROM memories WHERE scope=? AND kind=? AND key=? AND valid_to IS NULL`,
			m.Scope, m.Kind, m.Key)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			closing = append(closing, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// Log first. If this fails, nothing is written.
	if ix.log != nil {
		line := wirelog.WriteLine{
			Header: wirelog.NewHeader(wirelog.TypeWrite),
			ID:     m.ID, Scope: m.Scope, Source: m.Source, Kind: m.Kind,
			Content: m.Content, Confidence: m.Confidence, SessionRef: m.SessionRef,
			Superseded: []wirelog.Superseded{},
		}
		if m.Key != "" {
			k := m.Key
			line.Key = &k
			mode := wirelog.ModeKey
			line.SupersedeMode = &mode
			for _, id := range closing {
				line.Superseded = append(line.Superseded, wirelog.Superseded{ID: id, Key: m.Key})
			}
		}
		if err := ix.log.Append(line); err != nil {
			return nil, fmt.Errorf("index: wire log: %w", err)
		}
	}

	tx, err := ix.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Close before insert: SQLite enforces unique indexes per statement, not at
	// commit, so inserting first would trip the partial unique index against the
	// row it is about to vacate.
	var primaryParent any
	for _, id := range closing {
		if _, err := tx.Exec(
			`UPDATE memories SET valid_to=?, superseded_by=? WHERE id=?`, now, m.ID, id); err != nil {
			return nil, err
		}
		primaryParent = id
	}

	if _, err := tx.Exec(`
		INSERT INTO memories (id, kind, key, content, scope, source, valid_from,
		                      supersedes, confidence, session_ref, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.Kind, nullable(m.Key), m.Content, m.Scope, m.Source, now,
		primaryParent, m.Confidence, nullable(m.SessionRef), now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO memories_fts (content, id) VALUES (?,?)`, m.Content, m.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &WriteResult{ID: m.ID, Scope: m.Scope, Superseded: closing}, nil
}

// Current returns the live row for a subject, or nil. This is the cheapest
// useful question at the start of a session: "what is editor.theme?"
func (ix *Index) Current(scope, kind, key string) (*Memory, error) {
	key = NormalizeKey(key)
	var m Memory
	var k sql.NullString
	err := ix.db.QueryRow(
		`SELECT id, kind, key, content, scope, source FROM memories
		  WHERE scope=? AND kind=? AND key=? AND valid_to IS NULL`,
		scope, kind, key).Scan(&m.ID, &m.Kind, &k, &m.Content, &m.Scope, &m.Source)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m.Key = k.String
	return &m, nil
}

// History returns every row for a subject, newest first: the chain that makes
// "what did we used to think" answerable.
func (ix *Index) History(scope, kind, key string) ([]Memory, error) {
	key = NormalizeKey(key)
	rows, err := ix.db.Query(
		`SELECT id, kind, key, content, scope, source FROM memories
		  WHERE scope=? AND kind=? AND key=? ORDER BY valid_from DESC`,
		scope, kind, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		var k sql.NullString
		if err := rows.Scan(&m.ID, &m.Kind, &k, &m.Content, &m.Scope, &m.Source); err != nil {
			return nil, err
		}
		m.Key = k.String
		out = append(out, m)
	}
	return out, rows.Err()
}

// Hit is one search result.
type Hit struct {
	ID      string
	Kind    string
	Key     string
	Content string
	Scope   string
	Source  string
}

// Search runs FTS over current rows in the caller's effective scopes.
//
// The scope predicate is pushed into the query rather than applied to its
// results: filtering afterwards lets a busy unrelated project eat the result
// set and starve the recall you asked for.
func (ix *Index) Search(q, scope string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	scopes := effectiveScopes(scope)
	args := []any{ftsQuery(q)}
	ph := make([]string, len(scopes))
	for i, s := range scopes {
		ph[i] = "?"
		args = append(args, s)
	}
	args = append(args, limit)

	sqlText := `
		SELECT m.id, m.kind, m.key, m.content, m.scope, m.source
		  FROM memories_fts f
		  JOIN memories m ON m.id = f.id
		 WHERE memories_fts MATCH ?
		   AND m.valid_to IS NULL`
	if len(scopes) > 0 {
		sqlText += ` AND m.scope IN (` + strings.Join(ph, ",") + `)`
	}
	sqlText += ` ORDER BY bm25(memories_fts) LIMIT ?`

	rows, err := ix.db.Query(sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("index: search: %w", err)
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		var k sql.NullString
		if err := rows.Scan(&h.ID, &h.Kind, &k, &h.Content, &h.Scope, &h.Source); err != nil {
			return nil, err
		}
		h.Key = k.String
		out = append(out, h)
	}
	return out, rows.Err()
}

// ftsQuery makes arbitrary human text safe to hand to FTS5.
//
// FTS5 MATCH takes a query language, not a string: "fly.io", "c++" and a
// stray hyphen are all syntax errors. People search with the words they used,
// so every token is quoted as a literal and the tokens are ANDed.
func ftsQuery(q string) string {
	fields := strings.Fields(q)
	if len(fields) == 0 {
		return `""`
	}
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted = append(quoted, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " ")
}

// effectiveScopes: "*" means everything, anything else means that scope plus
// shared, so curated cross-project knowledge surfaces without being asked for.
func effectiveScopes(scope string) []string {
	switch scope {
	case "*":
		return nil
	case "", ScopeShared:
		return []string{ScopeShared}
	default:
		return []string{scope, ScopeShared}
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func newID() string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(time.Now().UnixNano() >> (i % 8 * 8))
	}
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), b[:4])
}
