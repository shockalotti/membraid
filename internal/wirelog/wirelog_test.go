package wirelog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "writes-2026-09-test.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The on-disk shape is locked (SPEC §3.4): changing a field name fails here
// rather than silently in a year-old log.
func TestWriteLineOnDiskShape(t *testing.T) {
	key, mode, conf := "editor.theme", ModeKey, 0.7
	b, err := json.Marshal(WriteLine{
		Header: Header{V: 1, T: TypeWrite, TS: "2026-09-12T10:00:00Z"},
		ID:     "u1", Key: &key, Scope: "g1", Source: "claude-code", Kind: "preference",
		Content: "prefers dark theme", Confidence: &conf, SessionRef: "s-123",
		SupersedeMode: &mode, Superseded: []Superseded{{ID: "prior", Key: "editor.theme"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"v":1`, `"t":"write"`, `"key":"editor.theme"`, `"scope":"g1"`,
		`"source":"claude-code"`, `"kind":"preference"`, `"session_ref":"s-123"`,
		`"supersede_mode":"key"`, `"superseded":[{"id":"prior","key":"editor.theme"}]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}

func TestSupersededEmptyIsArrayNotNull(t *testing.T) {
	b, _ := json.Marshal(WriteLine{Header: Header{V: 1, T: TypeWrite}, Superseded: []Superseded{}})
	if !strings.Contains(string(b), `"superseded":[]`) {
		t.Errorf("want empty array, got %s", b)
	}
}

func TestCheckpointConceptCarriesFullIdentity(t *testing.T) {
	b, _ := json.Marshal(CheckpointConcept{Path: "rules/foo.md", Scope: "shared", Type: "rule", Key: "memory.history"})
	for _, want := range []string{`"path":"rules/foo.md"`, `"scope":"shared"`, `"type":"rule"`, `"key":"memory.history"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}

// Two machines must never append to the same file: that is a git merge
// conflict every time both write between syncs.
func TestEachHostAppendsToItsOwnFile(t *testing.T) {
	dir := t.TempDir()
	a, _ := Open(dir, "Omarchy-Laptop")
	b, _ := Open(dir, "WIN-DESKTOP")
	defer a.Close()
	defer b.Close()
	if err := a.Append(CloseLine{Header: NewHeader(TypeClose, time.Now()), ID: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Append(CloseLine{Header: NewHeader(TypeClose, time.Now()), ID: "y"}); err != nil {
		t.Fatal(err)
	}
	files, _ := Files(dir)
	if len(files) != 2 {
		t.Fatalf("want one file per host, got %v", files)
	}
	for _, f := range files {
		base := filepath.Base(f)
		if !strings.HasSuffix(base, "-omarchy-laptop.jsonl") && !strings.HasSuffix(base, "-win-desktop.jsonl") {
			t.Errorf("unexpected file name %s", base)
		}
	}
}

func TestSafeHost(t *testing.T) {
	for in, want := range map[string]string{"Omarchy Laptop": "omarchy-laptop", "WIN_DESK.local": "win-desk-local", "": "local", "!!!": "local"} {
		if got := SafeHost(in); got != want {
			t.Errorf("SafeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// A partial tail is not consumed, so the next read starts at its first byte
// once the writer finishes it. This is what makes import safe mid-append.
func TestReadFromLeavesPartialTailForNextRead(t *testing.T) {
	good := `{"v":1,"t":"close","ts":"2026-09-12T10:00:00Z","id":"a"}` + "\n"
	partial := `{"v":1,"t":"close","ts":"2026-09-12T10:00:01Z","id":"b"}`
	p := writeFile(t, good+partial)

	es, off, err := ReadFrom(p, 0)
	if err != nil {
		t.Fatalf("partial tail must not error: %v", err)
	}
	if len(es) != 1 || es[0].Close.ID != "a" {
		t.Fatalf("want only the complete line, got %+v", es)
	}
	if off != int64(len(good)) {
		t.Fatalf("offset must stop before the partial line: %d", off)
	}

	// The writer finishes the line; a read from the saved offset gets it.
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("\n")
	f.Close()
	es, off2, err := ReadFrom(p, off)
	if err != nil || len(es) != 1 || es[0].Close.ID != "b" {
		t.Fatalf("resumed read: %v %+v", err, es)
	}
	if es, _, _ := ReadFrom(p, off2); len(es) != 0 {
		t.Errorf("reading from the end must yield nothing, got %d", len(es))
	}
}

func TestReadFromRefusesUnknownVersionAndType(t *testing.T) {
	for _, body := range []string{
		`{"v":99,"t":"write","ts":"2026-09-12T10:00:00Z"}` + "\n",
		`{"v":1,"t":"vacuum","ts":"2026-09-12T10:00:00Z"}` + "\n",
	} {
		if _, _, err := ReadFrom(writeFile(t, body), 0); !errors.Is(err, ErrUnknownVersion) {
			t.Errorf("want ErrUnknownVersion for %s, got %v", body, err)
		}
	}
}

func TestReadFromRefusesCorruptCompleteLine(t *testing.T) {
	if _, _, err := ReadFrom(writeFile(t, "{not json}\n"), 0); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}

func TestAllLineTypesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir, "h")
	defer l.Close()
	now := time.Now()
	lines := []any{
		WriteLine{Header: NewHeader(TypeWrite, now), ID: "u1", Scope: "shared", Source: "t", Kind: "preference", Content: "c", Superseded: []Superseded{}},
		DistillLine{Header: NewHeader(TypeDistill, now), Rows: []string{"u1"}, Concept: "facts/foo.md"},
		CheckpointLine{Header: NewHeader(TypeCheckpoint, now), Rows: []CheckpointRow{{ID: "u1"}}},
		CloseLine{Header: NewHeader(TypeClose, now), ID: "u1", Reason: "done"},
		RescopeLine{Header: NewHeader(TypeRescope, now), From: "pabc", To: "gdef"},
	}
	for _, ln := range lines {
		if err := l.Append(ln); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := l.Files()
	es, _, err := ReadFrom(files[0], 0)
	if err != nil || len(es) != 5 {
		t.Fatalf("got %d entries, err %v", len(es), err)
	}
	if es[0].Write == nil || es[1].Distill == nil || es[2].Checkpoint == nil || es[3].Close == nil || es[4].Rescope == nil {
		t.Errorf("types not decoded: %+v", es)
	}
	if es[4].Rescope.To != "gdef" || es[3].Close.Reason != "done" {
		t.Errorf("fields lost in round trip")
	}
}

// Instants, not strings: RFC3339Nano drops an all-zero fraction, and 'Z' sorts
// after '.', so a whole second sorts after a later fractional one as text.
func TestEntryTimeComparesInstants(t *testing.T) {
	early := Entry{Header: Header{TS: "2026-09-12T10:00:00Z"}}
	late := Entry{Header: Header{TS: "2026-09-12T10:00:00.5Z"}}
	if !(early.TS > late.TS) {
		t.Fatal("test premise: the earlier instant must sort later as text")
	}
	if !early.Time().Before(late.Time()) {
		t.Errorf("10:00:00 is before 10:00:00.5")
	}
}
