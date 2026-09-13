package index

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/shockalotti/membraid/internal/wirelog"
)

// Kinds are a closed set (SPEC §6.1). kind is part of a subject's identity, so
// a preference and an insight sharing a key are two subjects forever.
const (
	KindPreference   = "preference"
	KindProjectParam = "project_param"
	KindInsight      = "insight"
	KindTaskState    = "task_state"
)

var validKinds = map[string]bool{
	KindPreference: true, KindProjectParam: true, KindInsight: true, KindTaskState: true,
}

const ScopeShared = "shared"

type Index struct {
	db           *sql.DB
	log          *wirelog.Log
	now          func() time.Time
	halflifeDays float64

	// In-memory sign bits for vector search, used by long-lived processes
	// (see EnableVectorCache). vmu guards vcache.
	vmu      sync.Mutex
	vcacheOn bool
	vcache   *vectorCache
}

// Open opens the index. Transactions take the write lock when they begin
// (_txlock=immediate). Every write here reads before it writes; a transaction
// that starts as a read and upgrades after another process committed fails at
// once with SQLITE_BUSY_SNAPSHOT, and busy_timeout does not cover that case.
// With several harnesses writing to one vault that happened within a second.
// Taking the lock up front makes writers queue behind busy_timeout instead.
func Open(path string, log *wirelog.Log) (*Index, error) {
	// Only busy_timeout is set per connection. journal_mode is not: WAL is
	// stored in the database file, and switching mode can return SQLITE_BUSY
	// without consulting busy_timeout, so repeating the switch on every new
	// connection failed whenever another process was writing. It is set once,
	// when the schema is created.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("index: open: %w", err)
	}
	// Every command opens the index, so creating the schema is skipped when it
	// is already current: each CREATE takes the write lock, and doing that on
	// every open made each command queue behind every writer.
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		db.Close()
		return nil, fmt.Errorf("index: open: %w", err)
	}
	if version != schemaVersion {
		if err := enableWAL(db); err != nil {
			db.Close()
			return nil, fmt.Errorf("index: wal: %w", err)
		}
		if _, err := db.Exec(schemaSQL); err != nil {
			db.Close()
			return nil, fmt.Errorf("index: schema: %w", err)
		}
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Index{db: db, log: log, now: func() time.Time { return time.Now().UTC() }}, nil
}

// enableWAL switches a new index to write-ahead logging. Several processes can
// create the same index at once (every harness starting on a fresh vault), and
// the switch returns SQLITE_BUSY without waiting, so it is retried briefly.
func enableWAL(db *sql.DB) error {
	var err error
	for attempt := 0; attempt < 50; attempt++ {
		var mode string
		if err = db.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode); err == nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return err
}

func (ix *Index) Close() error                { return ix.db.Close() }
func (ix *Index) SetClock(f func() time.Time) { ix.now = f }

type Memory struct {
	ID         string
	Kind       string
	Key        string
	Content    string
	Scope      string
	Source     string
	Confidence *float64
	SessionRef string
}

type WriteResult struct {
	ID         string
	Scope      string
	Superseded []string
}

var ErrInvalidKind = errors.New("index: unknown kind")
var ErrNothingToClose = errors.New("index: no open task matches")
var ErrNothingToForget = errors.New("index: no current memory matches")

var keySep = regexp.MustCompile(`[\s._/\-]+`)
var keyJunk = regexp.MustCompile(`[^a-z0-9.]`)

// NormalizeKey collapses different agents' punctuation habits onto one spelling
// (SPEC §6.3): editor.theme, Editor_Theme and editor..theme are one subject.
func NormalizeKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	k = keySep.ReplaceAllString(k, ".")
	k = keyJunk.ReplaceAllString(k, "")
	return strings.Trim(k, ".")
}

// Write records a fact. A keyed write retires the live answer on the same
// subject, across every harness and every machine.
//
// The wire-log line is appended and flushed BEFORE the transaction commits, so
// the log stays a superset of the index (SPEC §3.3).
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
		m.ID = NewID()
	}
	m.Key = NormalizeKey(m.Key)
	at := ix.now()

	line := wirelog.WriteLine{
		Header: wirelog.NewHeader(wirelog.TypeWrite, at),
		ID:     m.ID, Scope: m.Scope, Source: m.Source, Kind: m.Kind,
		Content: m.Content, Confidence: m.Confidence, SessionRef: m.SessionRef,
		Superseded: []wirelog.Superseded{},
	}
	if m.Key != "" {
		k, mode := m.Key, wirelog.ModeKey
		line.Key, line.SupersedeMode = &k, &mode
		// Record what this write closes, for anyone reading the log. Apply
		// re-derives it by timestamp, and agrees for any write whose clock is
		// not behind a live answer that arrived from another machine.
		cur, err := ix.currentFor(ix.db, m.Scope, m.Kind, m.Key)
		if err != nil {
			return nil, err
		}
		for _, c := range cur {
			if newer(at, m.ID, c.at, c.id) {
				line.Superseded = append(line.Superseded, wirelog.Superseded{ID: c.id, Key: m.Key})
			}
		}
	}

	if ix.log != nil {
		if err := ix.log.Append(line); err != nil {
			return nil, fmt.Errorf("index: wire log: %w", err)
		}
	}
	tx, err := ix.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	closed, _, err := applyWrite(tx, line)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &WriteResult{ID: m.ID, Scope: m.Scope, Superseded: closed}, nil
}

type liveRow struct {
	id string
	at time.Time
}

type querier interface {
	Query(string, ...any) (*sql.Rows, error)
}

func (ix *Index) currentFor(q querier, scope, kind, key string) ([]liveRow, error) {
	rows, err := q.Query(`SELECT id, valid_from FROM memories
	                       WHERE scope=? AND kind=? AND key=? AND valid_to IS NULL`, scope, kind, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []liveRow
	for rows.Next() {
		var id, vf string
		if err := rows.Scan(&id, &vf); err != nil {
			return nil, err
		}
		t, _ := time.Parse(time.RFC3339Nano, vf)
		out = append(out, liveRow{id: id, at: t})
	}
	return out, rows.Err()
}

// newer decides which of two writes on one subject is the live answer. Latest
// timestamp wins; an exact tie goes to the larger id, so every machine picks
// the same winner no matter what order the lines arrive in.
func newer(aAt time.Time, aID string, bAt time.Time, bID string) bool {
	return aAt.After(bAt) || (aAt.Equal(bAt) && aID > bID)
}

// applyWrite brings one write line into the index. It is the ONLY path rows
// take in - local writes and writes pulled from another machine alike - so the
// two can never disagree about what is current.
//
// The rule that makes cross-machine merge safe: on a keyed subject the newest
// write is live and every older one is closed, whichever order they arrive.
// Two machines that both wrote deploy.target while offline converge on the
// same answer, with the other kept as history rather than dropped.
//
// Returns the ids it closed, and whether the line was new (false for a row this
// index already has, which makes import idempotent).
func applyWrite(tx *sql.Tx, w wirelog.WriteLine) ([]string, bool, error) {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM memories WHERE id=?`, w.ID).Scan(&n); err != nil {
		return nil, false, err
	}
	if n > 0 {
		return nil, false, nil
	}
	at, err := time.Parse(time.RFC3339Nano, w.TS)
	if err != nil {
		return nil, false, fmt.Errorf("index: bad timestamp on %s: %w", w.ID, err)
	}
	key := ""
	if w.Key != nil {
		key = *w.Key
	}

	var validTo, supersededBy, supersedes any
	var closed []string
	handled := map[string]bool{}

	if key != "" {
		rows, err := tx.Query(`SELECT id, valid_from FROM memories
		                        WHERE scope=? AND kind=? AND key=? AND valid_to IS NULL`, w.Scope, w.Kind, key)
		if err != nil {
			return nil, false, err
		}
		var live []liveRow
		for rows.Next() {
			var id, vf string
			if err := rows.Scan(&id, &vf); err != nil {
				rows.Close()
				return nil, false, err
			}
			t, _ := time.Parse(time.RFC3339Nano, vf)
			live = append(live, liveRow{id: id, at: t})
		}
		rows.Close()

		// Close before insert: SQLite checks the partial unique index per
		// statement, so inserting first would trip it against the row this
		// write is about to vacate.
		for _, c := range live {
			handled[c.id] = true
			if newer(at, w.ID, c.at, c.id) {
				if _, err := tx.Exec(`UPDATE memories SET valid_to=?, superseded_by=? WHERE id=?`, w.TS, w.ID, c.id); err != nil {
					return nil, false, err
				}
				closed = append(closed, c.id)
				supersedes = c.id
			} else {
				// This write is older than an answer already live - it arrived
				// late from another machine. It goes in as history.
				validTo, supersededBy = c.at.Format(time.RFC3339Nano), c.id
			}
		}
	}

	for _, s := range w.Superseded {
		if s.ID == "" || handled[s.ID] {
			continue
		}
		res, err := tx.Exec(`UPDATE memories SET valid_to=?, superseded_by=? WHERE id=? AND valid_to IS NULL`, w.TS, w.ID, s.ID)
		if err != nil {
			return nil, false, err
		}
		if k, _ := res.RowsAffected(); k > 0 {
			closed = append(closed, s.ID)
			if supersedes == nil {
				supersedes = s.ID
			}
		}
	}

	if _, err := tx.Exec(`
		INSERT INTO memories (id, kind, key, content, scope, source, valid_from, valid_to,
		                      supersedes, superseded_by, confidence, session_ref, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		w.ID, w.Kind, nullable(key), w.Content, w.Scope, w.Source, w.TS, validTo,
		supersedes, supersededBy, w.Confidence, nullable(w.SessionRef), w.TS); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(`INSERT INTO memories_fts (content, id) VALUES (?,?)`, w.Content, w.ID); err != nil {
		return nil, false, err
	}
	return closed, true, nil
}

func applyClose(tx *sql.Tx, c wirelog.CloseLine) error {
	_, err := tx.Exec(`UPDATE memories SET valid_to=? WHERE id=? AND valid_to IS NULL`, c.TS, c.ID)
	return err
}

// applyRescope moves a scope's memories to another scope. Where both scopes
// hold a live answer on the same subject, the older is closed first: moving it
// across unchanged would put two live answers on one subject.
func applyRescope(tx *sql.Tx, from, to string) (int, error) {
	rows, err := tx.Query(`
		SELECT a.id, a.valid_from, b.id, b.valid_from
		  FROM memories a JOIN memories b
		    ON b.scope=? AND b.kind=a.kind AND b.key=a.key AND b.valid_to IS NULL
		 WHERE a.scope=? AND a.key IS NOT NULL AND a.valid_to IS NULL`, to, from)
	if err != nil {
		return 0, err
	}
	type pair struct{ aID, aVF, bID, bVF string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.aID, &p.aVF, &p.bID, &p.bVF); err != nil {
			rows.Close()
			return 0, err
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	for _, p := range pairs {
		aAt, _ := time.Parse(time.RFC3339Nano, p.aVF)
		bAt, _ := time.Parse(time.RFC3339Nano, p.bVF)
		oldID, newID, newVF := p.bID, p.aID, p.aVF
		if newer(bAt, p.bID, aAt, p.aID) {
			oldID, newID, newVF = p.aID, p.bID, p.bVF
		}
		if _, err := tx.Exec(`UPDATE memories SET valid_to=?, superseded_by=? WHERE id=?`, newVF, newID, oldID); err != nil {
			return 0, err
		}
	}
	res, err := tx.Exec(`UPDATE memories SET scope=? WHERE scope=?`, to, from)
	if err != nil {
		return 0, err
	}
	moved, _ := res.RowsAffected()
	if _, err := tx.Exec(`UPDATE concepts SET scope=? WHERE scope=?`, to, from); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM scopes WHERE scope=?`, from); err != nil {
		return 0, err
	}
	return int(moved), nil
}

// ImportAll brings every wire-log line this index has not seen into it: other
// machines' writes after a pull, or the whole history into a fresh index.
func (ix *Index) ImportAll() (int, error) {
	if ix.log == nil {
		return 0, nil
	}
	files, err := ix.log.Files()
	if err != nil {
		return 0, err
	}
	return ix.ImportLog(files)
}

// ImportLog reads each file from where this index last stopped, applies the
// new lines in timestamp order across every file, and records the new
// positions in the same transaction. A refused line (unknown version, corrupt)
// rolls the whole import back, so no position ever moves past a line that was
// not applied.
func (ix *Index) ImportLog(files []string) (int, error) {
	if !ix.logGrew(files) {
		return 0, nil
	}
	tx, err := ix.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var entries []wirelog.Entry
	positions := map[string]int64{}
	for _, f := range files {
		base := filepath.Base(f)
		var pos int64
		if err := tx.QueryRow(`SELECT pos FROM log_offsets WHERE file=?`, base).Scan(&pos); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		// A file shorter than the saved position was replaced - restored from a
		// backup, re-cloned. Reread it; ids make that safe.
		if fi, err := os.Stat(f); err == nil && fi.Size() < pos {
			pos = 0
		}
		es, next, err := wirelog.ReadFrom(f, pos)
		if err != nil {
			return 0, fmt.Errorf("index: import %s: %w", base, err)
		}
		entries = append(entries, es...)
		positions[base] = next
	}

	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time().Before(entries[j].Time()) })

	imported := 0
	for _, e := range entries {
		switch {
		case e.Write != nil:
			_, isNew, err := applyWrite(tx, *e.Write)
			if err != nil {
				return 0, err
			}
			if isNew {
				imported++
			}
		case e.Close != nil:
			if err := applyClose(tx, *e.Close); err != nil {
				return 0, err
			}
		case e.Rescope != nil:
			if _, err := applyRescope(tx, e.Rescope.From, e.Rescope.To); err != nil {
				return 0, err
			}
		case e.Checkpoint != nil:
			if err := applyCheckpoint(tx, *e.Checkpoint); err != nil {
				return 0, err
			}
		case e.Distill != nil:
			if err := applyDistill(tx, *e.Distill); err != nil {
				return 0, err
			}
		}
	}
	for base, pos := range positions {
		if _, err := tx.Exec(`INSERT INTO log_offsets (file, pos) VALUES (?,?)
		                      ON CONFLICT(file) DO UPDATE SET pos=excluded.pos`, base, pos); err != nil {
			return 0, err
		}
	}
	return imported, tx.Commit()
}

// Done marks open task_state entries finished, by id or by key. Tasks are the
// only kind that can be closed without a replacement: a preference or a project
// value is superseded by its successor, but a finished task has no successor,
// and without this it sits in "where you left off" forever.
// logGrew reports whether any log file differs in size from where this index
// last stopped reading. Nothing new is the common case, because every command
// and every MCP tool call imports first, so it is checked without a
// transaction: a write transaction takes the database's write lock, and doing
// that on every read would queue each search behind every writer on the machine.
func (ix *Index) logGrew(files []string) bool {
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil {
			return true
		}
		var pos int64
		if err := ix.db.QueryRow(`SELECT pos FROM log_offsets WHERE file=?`, filepath.Base(f)).Scan(&pos); err != nil || fi.Size() != pos {
			return true
		}
	}
	return false
}

func (ix *Index) Done(scope, key, id string) ([]string, error) {
	var targets []string
	if id != "" {
		var kind string
		err := ix.db.QueryRow(`SELECT kind FROM memories WHERE id=? AND valid_to IS NULL`, id).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNothingToClose
		}
		if err != nil {
			return nil, err
		}
		if kind != KindTaskState {
			return nil, fmt.Errorf("index: %s is a %s, and only task_state entries can be marked done", id, kind)
		}
		targets = []string{id}
	} else {
		key = NormalizeKey(key)
		if key == "" {
			return nil, errors.New("index: done needs a key or an id")
		}
		args := []any{KindTaskState, key}
		q := `SELECT id FROM memories WHERE kind=? AND key=? AND valid_to IS NULL`
		if scopes := effectiveScopes(scope); len(scopes) > 0 {
			q += ` AND scope IN (` + placeholders(len(scopes)) + `)`
			for _, s := range scopes {
				args = append(args, s)
			}
		}
		ids, err := ix.queryIDs(q, args...)
		if err != nil {
			return nil, err
		}
		targets = ids
	}
	if len(targets) == 0 {
		return nil, ErrNothingToClose
	}

	return ix.closeIDs(targets, "done")
}

// Forget retires live memories of any kind, by id or by key, for a fact that
// is wrong or no longer true and has nothing true to replace it: a removed
// tool, an abandoned plan, something recorded by mistake. A fact that has a
// correct answer is superseded by writing that answer under the same key
// instead. Nothing is deleted: the entry stays in history, and the close is
// logged so it is forgotten on every machine.
func (ix *Index) Forget(scope, key, id string) ([]string, error) {
	var targets []string
	if id != "" {
		ids, err := ix.queryIDs(`SELECT id FROM memories WHERE id=? AND valid_to IS NULL`, id)
		if err != nil {
			return nil, err
		}
		targets = ids
	} else {
		key = NormalizeKey(key)
		if key == "" {
			return nil, errors.New("index: forget needs a key or an id")
		}
		args := []any{key}
		q := `SELECT id FROM memories WHERE key=? AND valid_to IS NULL`
		if scopes := effectiveScopes(scope); len(scopes) > 0 {
			q += ` AND scope IN (` + placeholders(len(scopes)) + `)`
			for _, s := range scopes {
				args = append(args, s)
			}
		}
		ids, err := ix.queryIDs(q, args...)
		if err != nil {
			return nil, err
		}
		targets = ids
	}
	if len(targets) == 0 {
		return nil, ErrNothingToForget
	}
	return ix.closeIDs(targets, "forgotten")
}

func (ix *Index) queryIDs(q string, args ...any) ([]string, error) {
	rows, err := ix.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// closeIDs logs a close line for each target, then applies them in one
// transaction - log first, so the log stays a superset of the index.
func (ix *Index) closeIDs(targets []string, reason string) ([]string, error) {
	at := ix.now()
	lines := make([]wirelog.CloseLine, 0, len(targets))
	for _, t := range targets {
		cl := wirelog.CloseLine{Header: wirelog.NewHeader(wirelog.TypeClose, at), ID: t, Reason: reason}
		if ix.log != nil {
			if err := ix.log.Append(cl); err != nil {
				return nil, fmt.Errorf("index: wire log: %w", err)
			}
		}
		lines = append(lines, cl)
	}
	tx, err := ix.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, cl := range lines {
		if err := applyClose(tx, cl); err != nil {
			return nil, err
		}
	}
	return targets, tx.Commit()
}

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

type Hit struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Key     string `json:"key,omitempty"`
	Content string `json:"content"`
	Scope   string `json:"scope"`
	Source  string `json:"source"`
	At      string `json:"at,omitempty"`
	// ScopeName is the readable project name, filled in by callers that show
	// more than one project at once. An id like g0c59d778 means nothing to read.
	ScopeName string `json:"scope_name,omitempty"`
}

// Recent returns the newest current rows: what the agents have been learning.
func (ix *Index) Recent(scope string, kinds []string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 20
	}
	q := `SELECT id, kind, key, content, scope, source, valid_from FROM memories WHERE valid_to IS NULL`
	var args []any
	if scopes := effectiveScopes(scope); len(scopes) > 0 {
		q += ` AND scope IN (` + placeholders(len(scopes)) + `)`
		for _, s := range scopes {
			args = append(args, s)
		}
	}
	if len(kinds) > 0 {
		q += ` AND kind IN (` + placeholders(len(kinds)) + `)`
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	q += ` ORDER BY valid_from DESC LIMIT ?`
	args = append(args, limit)
	return ix.hits(q, args...)
}

func (ix *Index) hits(q string, args ...any) ([]Hit, error) {
	rows, err := ix.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("index: query: %w", err)
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		var k sql.NullString
		if err := rows.Scan(&h.ID, &h.Kind, &k, &h.Content, &h.Scope, &h.Source, &h.At); err != nil {
			return nil, err
		}
		h.Key = k.String
		out = append(out, h)
	}
	return out, rows.Err()
}

// effectiveScopes: "*" means everything; anything else means that scope plus
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

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// NewID is 128 random bits. Ids used to be derived from the clock, which was
// fine on one machine and not on two: import skips a line whose id it already
// has, so two machines minting the same id at the same instant would silently
// drop one of the two memories.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("index: no randomness for ids: " + err.Error())
	}
	return hex.EncodeToString(b)
}

type Stats struct {
	Current  int            `json:"current"`
	Total    int            `json:"total"`
	Scopes   int            `json:"scopes"`
	BySource map[string]int `json:"by_source"`
}

func (ix *Index) Stats() (*Stats, error) {
	s := &Stats{BySource: map[string]int{}}
	if err := ix.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE valid_to IS NULL`).Scan(&s.Current); err != nil {
		return nil, err
	}
	if err := ix.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&s.Total); err != nil {
		return nil, err
	}
	if err := ix.db.QueryRow(`SELECT COUNT(DISTINCT scope) FROM memories`).Scan(&s.Scopes); err != nil {
		return nil, err
	}
	rows, err := ix.db.Query(`SELECT source, COUNT(*) FROM memories WHERE valid_to IS NULL GROUP BY source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var src string
		var n int
		if err := rows.Scan(&src, &n); err != nil {
			return nil, err
		}
		s.BySource[src] = n
	}
	return s, rows.Err()
}

type ScopeInfo struct {
	Scope    string `json:"scope"`
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	LastSeen string `json:"last_seen"`
	Count    int    `json:"count"`
	Missing  bool   `json:"missing,omitempty"`
}

// ScopeNames maps every known scope id to the name it was last seen under.
func (ix *Index) ScopeNames() (map[string]string, error) {
	rows, err := ix.db.Query(`SELECT scope, name FROM scopes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{ScopeShared: ScopeShared}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// TouchScope records that this scope was seen here, under this name.
func (ix *Index) TouchScope(scope, name, path string) error {
	if scope == "" || scope == ScopeShared {
		return nil
	}
	now := ix.now().Format(time.RFC3339Nano)
	_, err := ix.db.Exec(`
		INSERT INTO scopes (scope, name, path, first_seen, last_seen) VALUES (?,?,?,?,?)
		ON CONFLICT(scope) DO UPDATE SET name=excluded.name, path=excluded.path, last_seen=excluded.last_seen`,
		scope, name, path, now, now)
	return err
}

func (ix *Index) Scopes() ([]ScopeInfo, error) {
	rows, err := ix.db.Query(`
		SELECT s.scope, s.name, COALESCE(s.path,''), s.last_seen,
		       (SELECT COUNT(*) FROM memories m WHERE m.scope = s.scope AND m.valid_to IS NULL)
		  FROM scopes s ORDER BY s.last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScopeInfo
	for rows.Next() {
		var s ScopeInfo
		if err := rows.Scan(&s.Scope, &s.Name, &s.Path, &s.LastSeen, &s.Count); err != nil {
			return nil, err
		}
		if s.Path != "" {
			if _, err := os.Stat(s.Path); err != nil {
				s.Missing = true
			}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Rescope moves every memory from one scope to another. It is logged, because a
// rescope that only touched the local index would be undone on every other
// machine, which replays the original scope from the log.
func (ix *Index) Rescope(from, to string) (int, error) {
	if ix.log != nil {
		if err := ix.log.Append(wirelog.RescopeLine{
			Header: wirelog.NewHeader(wirelog.TypeRescope, ix.now()), From: from, To: to,
		}); err != nil {
			return 0, fmt.Errorf("index: wire log: %w", err)
		}
	}
	tx, err := ix.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n, err := applyRescope(tx, from, to)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// SeedOffsets marks every existing log file as already read up to its current
// size. For an index that was built by direct writes before offsets existed:
// it already holds those rows, and ids would make rereading safe anyway.
func (ix *Index) SeedOffsets() error {
	if ix.log == nil {
		return nil
	}
	files, err := ix.log.Files()
	if err != nil {
		return err
	}
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil {
			if _, err := ix.db.Exec(`INSERT OR IGNORE INTO log_offsets (file, pos) VALUES (?,?)`, filepath.Base(f), fi.Size()); err != nil {
				return err
			}
		}
	}
	return nil
}
