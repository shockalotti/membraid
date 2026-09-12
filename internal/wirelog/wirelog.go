// Package wirelog implements the append-only wire log: the engine's only
// artifact that is not rebuildable from something else (SPEC §5.3).
//
// Format rules this package exists to enforce, all from SPEC §3.3/§3.4/§5.3:
//
//   - Every line is one newline-terminated JSON object written with a single
//     write, so a concurrent reader never sees interleaved bytes.
//   - The log is written BEFORE the SQLite commit, so the log is always a
//     superset of the index.
//   - `v` is the LINE-format version, not a per-file one.
//   - A truncated FINAL line is left unread (the write never completed). An
//     unknown `v` or `t` is refused. A complete line that will not parse is
//     corruption.
//   - The log is never pruned, so a parser is kept for every `v` ever emitted.
//
// Each machine appends to its OWN file, writes-YYYY-MM-<host>.jsonl. The vault
// syncs through git, and two machines appending to the end of one shared file
// is a merge conflict every single time they both write between syncs. With a
// file per machine, appends never touch the same file, so they never collide.
// File naming is not part of the line format; files from before per-host
// naming (writes-YYYY-MM.jsonl) are still read.
package wirelog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// CurrentVersion is the line-format version this binary emits. Bump it for ANY
// change to an existing line type's fields, meaning or interpretation, and keep
// a parser for the old value (SPEC §5.3, writer-side obligation).
const CurrentVersion = 1

// Line types. Adding a new type is not a change to an existing one: an older
// binary meeting it refuses it as unknown rather than misreading it.
const (
	TypeWrite      = "write"
	TypeDistill    = "distill"
	TypeCheckpoint = "checkpoint"
	TypeClose      = "close"
	TypeRescope    = "rescope"
)

const (
	ModeKey      = "key"
	ModeFuzzy    = "fuzzy"
	ModeExplicit = "explicit"
)

type Superseded struct {
	ID         string   `json:"id"`
	Key        string   `json:"key,omitempty"`
	MatchScore *float64 `json:"matchScore,omitempty"`
}

// Header is the part every line shares.
type Header struct {
	V  int    `json:"v"`
	T  string `json:"t"`
	TS string `json:"ts"`
}

// WriteLine is t:"write".
type WriteLine struct {
	Header
	ID            string       `json:"id"`
	Key           *string      `json:"key"`
	Scope         string       `json:"scope"`
	Source        string       `json:"source"`
	Kind          string       `json:"kind"`
	Content       string       `json:"content"`
	Confidence    *float64     `json:"confidence,omitempty"`
	SessionRef    string       `json:"session_ref,omitempty"`
	SupersedeMode *string      `json:"supersede_mode"`
	Superseded    []Superseded `json:"superseded"`
}

// DistillLine is t:"distill".
type DistillLine struct {
	Header
	Rows    []string `json:"rows"`
	Concept string   `json:"concept"`
}

// CheckpointLine is t:"checkpoint": a full snapshot of retrieval state.
type CheckpointLine struct {
	Header
	Rows     []CheckpointRow     `json:"rows"`
	Concepts []CheckpointConcept `json:"concepts"`
}

type CheckpointRow struct {
	ID            string `json:"id"`
	LastRetrieved string `json:"last_retrieved"`
}

type CheckpointConcept struct {
	Path          string `json:"path"`
	Scope         string `json:"scope"`
	Type          string `json:"type"`
	Key           string `json:"key,omitempty"`
	LastRetrieved string `json:"last_retrieved"`
}

// CloseLine is t:"close": a row retired with no replacement - a finished task.
// Without it a task_state entry could only ever be superseded, so one written
// without a key stayed in "where you left off" forever.
type CloseLine struct {
	Header
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

// RescopeLine is t:"rescope": every memory filed under From now belongs to To.
// A rescope that only updated the local index would be undone on every other
// machine, which replays the original scope from the log.
type RescopeLine struct {
	Header
	From string `json:"from"`
	To   string `json:"to"`
}

var ErrUnknownVersion = errors.New("wirelog: unknown line-format version")
var ErrCorrupt = errors.New("wirelog: corrupt line")

// Log appends lines to vault/.hot/writes-YYYY-MM-<host>.jsonl.
type Log struct {
	dir  string
	host string

	mu   sync.Mutex
	f    *os.File
	name string
}

// Open prepares the log directory. host names this machine's file; it is
// sanitised, and empty becomes "local".
func Open(dir, host string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("wirelog: create %s: %w", dir, err)
	}
	return &Log{dir: dir, host: SafeHost(host)}, nil
}

var nonHost = regexp.MustCompile(`[^a-z0-9]+`)

// SafeHost turns a hostname into something that can sit in a filename on every
// platform the vault syncs to.
func SafeHost(h string) string {
	h = strings.Trim(nonHost.ReplaceAllString(strings.ToLower(h), "-"), "-")
	if h == "" {
		return "local"
	}
	return h
}

func (l *Log) monthFile(t time.Time) string {
	return fmt.Sprintf("writes-%04d-%02d-%s.jsonl", t.Year(), int(t.Month()), l.host)
}

// Append writes one line, flushed and fsynced, before the caller commits the
// index transaction (SPEC §3.3: log-before-index).
func (l *Log) Append(v any) error {
	buf, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("wirelog: marshal: %w", err)
	}
	buf = append(buf, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.rotateLocked(time.Now().UTC()); err != nil {
		return err
	}
	if _, err := l.f.Write(buf); err != nil {
		return fmt.Errorf("wirelog: append: %w", err)
	}
	if err := l.f.Sync(); err != nil {
		return fmt.Errorf("wirelog: sync: %w", err)
	}
	return nil
}

// NewHeader stamps the current version and the given timestamp. Callers pass
// the same instant they store as valid_from, so a replayed row is identical to
// the row that was written.
func NewHeader(t string, at time.Time) Header {
	return Header{V: CurrentVersion, T: t, TS: at.UTC().Format(time.RFC3339Nano)}
}

func (l *Log) rotateLocked(now time.Time) error {
	want := l.monthFile(now)
	if l.f != nil && l.name == want {
		return nil
	}
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	f, err := os.OpenFile(filepath.Join(l.dir, want), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("wirelog: open %s: %w", want, err)
	}
	l.f, l.name = f, want
	return nil
}

func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Files lists every log file in the vault, from every machine. Order carries
// no meaning: readers order entries by timestamp, not by file.
func (l *Log) Files() ([]string, error) { return Files(l.dir) }

func Files(dir string) ([]string, error) {
	out, err := filepath.Glob(filepath.Join(dir, "writes-*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// Entry is one decoded line: exactly one of the typed pointers is set.
type Entry struct {
	Header
	Write      *WriteLine
	Distill    *DistillLine
	Checkpoint *CheckpointLine
	Close      *CloseLine
	Rescope    *RescopeLine
}

// Time parses the entry's timestamp. Timestamps are compared as instants, never
// as strings: RFC3339Nano drops an all-zero fraction entirely, so "10:00:00Z"
// sorts AFTER "10:00:00.5Z" as text ('Z' > '.') while being the earlier
// instant. Two machines' lines sorted as strings would merge out of order.
func (e Entry) Time() time.Time {
	t, _ := time.Parse(time.RFC3339Nano, e.TS)
	return t
}

func decode(raw []byte, where string) (*Entry, error) {
	var hd Header
	if err := json.Unmarshal(raw, &hd); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrCorrupt, where, err)
	}
	if hd.V != CurrentVersion {
		return nil, fmt.Errorf("%w: %s: v=%d, this binary knows %d", ErrUnknownVersion, where, hd.V, CurrentVersion)
	}
	e := &Entry{Header: hd}
	var err error
	switch hd.T {
	case TypeWrite:
		e.Write = &WriteLine{}
		err = json.Unmarshal(raw, e.Write)
	case TypeDistill:
		e.Distill = &DistillLine{}
		err = json.Unmarshal(raw, e.Distill)
	case TypeCheckpoint:
		e.Checkpoint = &CheckpointLine{}
		err = json.Unmarshal(raw, e.Checkpoint)
	case TypeClose:
		e.Close = &CloseLine{}
		err = json.Unmarshal(raw, e.Close)
	case TypeRescope:
		e.Rescope = &RescopeLine{}
		err = json.Unmarshal(raw, e.Rescope)
	default:
		return nil, fmt.Errorf("%w: %s: unknown line type %q", ErrUnknownVersion, where, hd.T)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrCorrupt, where, err)
	}
	return e, nil
}

// ReadFrom decodes every complete line from offset onward and returns the
// offset just past the last complete line.
//
// A partial final line is not an error and is not consumed: it is either a
// write still in progress or one a crash cut short, and in both cases the next
// read should start from its first byte. That is what makes incremental import
// safe to run while another process is appending.
func ReadFrom(path string, offset int64) ([]Entry, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}

	r := bufio.NewReaderSize(f, 1<<20)
	var out []Entry
	pos := offset
	for {
		raw, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return out, pos, nil // any bytes here are a partial tail: leave them
		}
		if err != nil {
			return out, pos, fmt.Errorf("wirelog: read %s: %w", path, err)
		}
		lineStart := pos
		pos += int64(len(raw))
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		e, derr := decode(raw, fmt.Sprintf("%s@%d", filepath.Base(path), lineStart))
		if derr != nil {
			return out, lineStart, derr
		}
		out = append(out, *e)
	}
}
