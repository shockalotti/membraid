// Package wirelog implements the append-only wire log: the engine's only
// artifact that is not rebuildable from something else (SPEC §5.3).
//
// Format rules this package exists to enforce, all from SPEC §3.3/§3.4/§5.3:
//
//   - Every line is one newline-terminated JSON object written with a single
//     write, so a concurrent reader never sees interleaved bytes.
//   - The log is written BEFORE the SQLite commit, so the log is always a
//     superset of the index. A crash between the two leaves a logged line
//     with no row, which replay heals; the reverse loses a row silently at
//     the next rebuild.
//   - `v` is the LINE-format version, not a per-file one: a monthly file
//     routinely holds lines from two binary versions because upgrades happen
//     whenever they happen.
//   - A truncated FINAL line is skipped (the write never completed, so no
//     committed row corresponds to it). An unknown `v` is refused (a complete
//     record this binary cannot interpret). A partial line anywhere but the
//     end is corruption and refuses like an unknown `v`.
//   - The log is never pruned, so a parser is kept for every `v` ever
//     emitted. Any change to a line type's fields, their meaning, or their
//     interpretation bumps `v` for that line type.
package wirelog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CurrentVersion is the line-format version this binary emits. Bump it for ANY
// change to a line type's fields, their meaning, or their interpretation, and
// keep a parser for the old value (SPEC §5.3, writer-side obligation).
const CurrentVersion = 1

// LineType values. Each is versioned independently by the `v` on its own line.
const (
	TypeWrite      = "write"
	TypeDistill    = "distill"
	TypeCheckpoint = "checkpoint"
)

// SupersedeMode records how supersession was derived, so the log is
// self-describing and M4's threshold tuning can filter by mode (SPEC §3.4).
const (
	ModeKey      = "key"
	ModeFuzzy    = "fuzzy"
	ModeExplicit = "explicit"
)

// Superseded is one closed row. A keyed close carries the matching key; a
// fuzzy close carries the score that fired. Always serialised as an array,
// empty when nothing was closed: one write can close several rows, and a
// single column cannot hold that (SPEC §6.2).
type Superseded struct {
	ID         string   `json:"id"`
	Key        string   `json:"key,omitempty"`
	MatchScore *float64 `json:"matchScore,omitempty"`
}

// Lines are decoded per type rather than as one union struct: `distill` and
// `checkpoint` both use the JSON field "rows" with different element types,
// so a single struct cannot carry both without renaming a field on disk - and
// the on-disk format is locked (SPEC §5.3: any field change bumps `v`).

// Header is the part every line shares. Replay probes this, then decodes the
// concrete type.
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

// DistillLine is t:"distill". Written AFTER the concept file, so a crash
// leaves an unlinked file the next pass adopts (SPEC §3.4, §8 step 5).
type DistillLine struct {
	Header
	Rows    []string `json:"rows"`
	Concept string   `json:"concept"`
}

// CheckpointLine is t:"checkpoint": a FULL snapshot that supersedes all prior
// checkpoints on replay; replay never merges them.
type CheckpointLine struct {
	Header
	Rows     []CheckpointRow     `json:"rows"`
	Concepts []CheckpointConcept `json:"concepts"`
}

type CheckpointRow struct {
	ID            string `json:"id"`
	LastRetrieved string `json:"last_retrieved"`
}

// CheckpointConcept carries the mirror's full identity axis. `path` alone is
// not enough (§4.1 invites humans to reorganise folders) and neither is
// (scope, key): §5.2 indexes concepts on (scope, type, key) and §6.2 de-dupes
// on it, so one scope may hold a preference and a rule under one key.
type CheckpointConcept struct {
	Path          string `json:"path"`
	Scope         string `json:"scope"`
	Type          string `json:"type"`
	Key           string `json:"key,omitempty"`
	LastRetrieved string `json:"last_retrieved"`
}

// ErrUnknownVersion is returned for a complete line whose `v` this binary does
// not know. Replay must stop rather than silently drop a record that exists.
var ErrUnknownVersion = errors.New("wirelog: unknown line-format version")

// ErrCorrupt is returned for a partial line anywhere but the end of a file.
var ErrCorrupt = errors.New("wirelog: partial line before end of file")

// Log appends lines to vault/.hot/writes-YYYY-MM.jsonl.
type Log struct {
	dir string

	mu   sync.Mutex
	f    *os.File
	name string // basename of the currently open month file
}

// Open prepares the log directory. dir is the vault's .hot directory.
func Open(dir string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("wirelog: create %s: %w", dir, err)
	}
	return &Log{dir: dir}, nil
}

func monthFile(t time.Time) string {
	return fmt.Sprintf("writes-%04d-%02d.jsonl", t.Year(), int(t.Month()))
}

// Append writes one line and flushes it to the OS, then fsyncs. It MUST be
// called before the corresponding SQLite commit (SPEC §3.3: log-before-index).
//
// The marshalled object plus its newline go out in a single Write so that a
// concurrent reader - a backup capture, another replay - never observes
// interleaved bytes from two appends.
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
	// Durability is the whole point of writing here first: an unflushed line
	// would leave the index a superset of the log, inverting the invariant.
	if err := l.f.Sync(); err != nil {
		return fmt.Errorf("wirelog: sync: %w", err)
	}
	return nil
}

// NewHeader stamps the current line-format version and an RFC3339 UTC time.
func NewHeader(t string) Header {
	return Header{V: CurrentVersion, T: t, TS: time.Now().UTC().Format(time.RFC3339Nano)}
}

func (l *Log) rotateLocked(now time.Time) error {
	want := monthFile(now)
	if l.f != nil && l.name == want {
		return nil
	}
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	// O_APPEND keeps every write atomic against other writers on the same
	// file, which matters even though the daemon is a single writer: reindex
	// and backup are separate processes (SPEC §3.3).
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

// Files lists the month files in chronological order. Replay reads them in
// this order; the log is never pruned, so this is the complete history.
func (l *Log) Files() ([]string, error) {
	entries, err := filepath.Glob(filepath.Join(l.dir, "writes-*.jsonl"))
	if err != nil {
		return nil, err
	}
	// Glob returns lexical order, which for writes-YYYY-MM is chronological.
	return entries, nil
}

// Handler receives each complete, known line, already decoded to its type.
// Exactly one method is called per line.
type Handler interface {
	Write(WriteLine) error
	Distill(DistillLine) error
	Checkpoint(CheckpointLine) error
}

// Replay reads one file and dispatches each complete, known line.
//
// A truncated final line is skipped: the write never completed, so by
// log-before-index no committed row corresponds to it. The same truncation
// anywhere but at the end is corruption and returns ErrCorrupt. An unknown
// `v` returns ErrUnknownVersion and stops replay (SPEC §5.3).
func Replay(path string, h Handler) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 1<<20)
	lineNo := 0
	for {
		lineNo++
		raw, err := r.ReadBytes('\n')
		atEOF := errors.Is(err, io.EOF)
		if err != nil && !atEOF {
			return fmt.Errorf("wirelog: read %s:%d: %w", path, lineNo, err)
		}
		if len(raw) == 0 && atEOF {
			return nil
		}
		if raw[len(raw)-1] != '\n' {
			// No trailing newline. ReadBytes only returns this together with
			// io.EOF, so a partial line mid-stream cannot occur here; guard
			// anyway so the distinction stays explicit rather than implied.
			if !atEOF {
				return fmt.Errorf("%w: %s:%d", ErrCorrupt, path, lineNo)
			}
			return nil // skip the partial tail
		}

		var hd Header
		if err := json.Unmarshal(raw, &hd); err != nil {
			// A complete line that will not parse is corruption, not
			// truncation: the newline proves the write finished.
			return fmt.Errorf("%w: %s:%d: %v", ErrCorrupt, path, lineNo, err)
		}
		if hd.V != CurrentVersion {
			return fmt.Errorf("%w: %s:%d: v=%d, this binary knows %d",
				ErrUnknownVersion, path, lineNo, hd.V, CurrentVersion)
		}

		switch hd.T {
		case TypeWrite:
			var l WriteLine
			if err := json.Unmarshal(raw, &l); err != nil {
				return fmt.Errorf("%w: %s:%d: %v", ErrCorrupt, path, lineNo, err)
			}
			if err := h.Write(l); err != nil {
				return err
			}
		case TypeDistill:
			var l DistillLine
			if err := json.Unmarshal(raw, &l); err != nil {
				return fmt.Errorf("%w: %s:%d: %v", ErrCorrupt, path, lineNo, err)
			}
			if err := h.Distill(l); err != nil {
				return err
			}
		case TypeCheckpoint:
			var l CheckpointLine
			if err := json.Unmarshal(raw, &l); err != nil {
				return fmt.Errorf("%w: %s:%d: %v", ErrCorrupt, path, lineNo, err)
			}
			if err := h.Checkpoint(l); err != nil {
				return err
			}
		default:
			// An unrecognised `t` at a known `v` is a complete record this
			// binary cannot interpret: same class as an unknown `v`.
			return fmt.Errorf("%w: %s:%d: unknown line type %q", ErrUnknownVersion, path, lineNo, hd.T)
		}
		if atEOF {
			return nil
		}
	}
}
