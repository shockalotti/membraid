package wirelog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type collector struct {
	writes      []WriteLine
	distills    []DistillLine
	checkpoints []CheckpointLine
}

func (c *collector) Write(l WriteLine) error     { c.writes = append(c.writes, l); return nil }
func (c *collector) Distill(l DistillLine) error { c.distills = append(c.distills, l); return nil }
func (c *collector) Checkpoint(l CheckpointLine) error {
	c.checkpoints = append(c.checkpoints, l)
	return nil
}

func writeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "writes-2026-09.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The on-disk shape is locked (SPEC §3.4). This pins the exact field names so
// that changing one fails here rather than silently in a year-old log.
func TestWriteLineOnDiskShape(t *testing.T) {
	key := "editor.theme"
	mode := ModeKey
	conf := 0.7
	l := WriteLine{
		Header:        Header{V: 1, T: TypeWrite, TS: "2026-09-12T10:00:00Z"},
		ID:            "u1",
		Key:           &key,
		Scope:         "mem-a3f7b2c1",
		Source:        "claude-code",
		Kind:          "preference",
		Content:       "prefers dark theme",
		Confidence:    &conf,
		SessionRef:    "s-123",
		SupersedeMode: &mode,
		Superseded:    []Superseded{{ID: "prior", Key: "editor.theme"}},
	}
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"v":1`, `"t":"write"`, `"key":"editor.theme"`, `"scope":"mem-a3f7b2c1"`,
		`"source":"claude-code"`, `"kind":"preference"`, `"session_ref":"s-123"`,
		`"supersede_mode":"key"`, `"superseded":[{"id":"prior","key":"editor.theme"}]`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}

// superseded is ALWAYS an array, empty when nothing closed (SPEC §3.4): a
// null here would make "did this close anything" ambiguous on replay.
func TestSupersededEmptyIsArrayNotNull(t *testing.T) {
	l := WriteLine{Header: Header{V: 1, T: TypeWrite}, Superseded: []Superseded{}}
	b, _ := json.Marshal(l)
	if !strings.Contains(string(b), `"superseded":[]`) {
		t.Errorf("want empty array, got %s", b)
	}
}

// distill and checkpoint both use "rows" with different element types. This
// is why lines are decoded per type rather than through one union struct.
func TestDistillAndCheckpointBothUseRows(t *testing.T) {
	d, _ := json.Marshal(DistillLine{Header: Header{V: 1, T: TypeDistill}, Rows: []string{"a"}, Concept: "facts/foo.md"})
	c, _ := json.Marshal(CheckpointLine{Header: Header{V: 1, T: TypeCheckpoint}, Rows: []CheckpointRow{{ID: "a", LastRetrieved: "t"}}})
	if !strings.Contains(string(d), `"rows":["a"]`) {
		t.Errorf("distill rows: %s", d)
	}
	if !strings.Contains(string(c), `"rows":[{"id":"a"`) {
		t.Errorf("checkpoint rows: %s", c)
	}
}

// The checkpoint concepts entry carries the mirror's full identity axis:
// path, scope, type, key (SPEC §3.4, R12-52). Restore matches path first,
// then (scope, type, key).
func TestCheckpointConceptCarriesFullIdentity(t *testing.T) {
	b, _ := json.Marshal(CheckpointConcept{
		Path: "rules/foo.md", Scope: "shared", Type: "rule",
		Key: "memory.history", LastRetrieved: "2026-09-12T12:00:00Z",
	})
	for _, want := range []string{`"path":"rules/foo.md"`, `"scope":"shared"`, `"type":"rule"`, `"key":"memory.history"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}

// A truncated FINAL line is skipped: the write never completed, so by
// log-before-index no committed row corresponds to it (SPEC §5.3).
func TestReplaySkipsTruncatedFinalLine(t *testing.T) {
	good := `{"v":1,"t":"write","ts":"2026-09-12T10:00:00Z","id":"u1","key":null,"scope":"s","source":"x","kind":"insight","content":"c","supersede_mode":null,"superseded":[]}` + "\n"
	p := writeFile(t, good+`{"v":1,"t":"write","ts":"2026-09-`)

	var c collector
	if err := Replay(p, &c); err != nil {
		t.Fatalf("truncated tail must not error: %v", err)
	}
	if len(c.writes) != 1 {
		t.Fatalf("want 1 complete line, got %d", len(c.writes))
	}
}

// An unknown `v` is a COMPLETE record this binary cannot interpret: replay
// must stop rather than silently drop data that exists (SPEC §5.3).
func TestReplayRefusesUnknownVersion(t *testing.T) {
	p := writeFile(t, `{"v":99,"t":"write","ts":"2026-09-12T10:00:00Z","id":"u1"}`+"\n")
	var c collector
	err := Replay(p, &c)
	if !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("want ErrUnknownVersion, got %v", err)
	}
}

// An unrecognised `t` at a known `v` is the same class as an unknown `v`.
func TestReplayRefusesUnknownLineType(t *testing.T) {
	p := writeFile(t, `{"v":1,"t":"vacuum","ts":"2026-09-12T10:00:00Z"}`+"\n")
	var c collector
	if err := Replay(p, &c); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("want ErrUnknownVersion, got %v", err)
	}
}

// A COMPLETE line that will not parse is corruption, not truncation: the
// newline proves the write finished (SPEC §5.3).
func TestReplayRefusesCorruptCompleteLine(t *testing.T) {
	p := writeFile(t, "{not json}\n")
	var c collector
	if err := Replay(p, &c); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}

// Round trip through a real Log: whole lines, append-only, replayable.
func TestAppendThenReplay(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	if err := l.Append(WriteLine{
		Header: NewHeader(TypeWrite), ID: "u1", Scope: "shared", Source: "test",
		Kind: "preference", Content: "c", Superseded: []Superseded{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(DistillLine{
		Header: NewHeader(TypeDistill), Rows: []string{"u1"}, Concept: "facts/foo.md",
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(CheckpointLine{
		Header:   NewHeader(TypeCheckpoint),
		Rows:     []CheckpointRow{{ID: "u1", LastRetrieved: "2026-09-12T12:00:00Z"}},
		Concepts: []CheckpointConcept{{Path: "rules/f.md", Scope: "shared", Type: "rule", LastRetrieved: "2026-09-12T12:00:00Z"}},
	}); err != nil {
		t.Fatal(err)
	}

	files, err := l.Files()
	if err != nil || len(files) != 1 {
		t.Fatalf("files=%v err=%v", files, err)
	}
	var c collector
	if err := Replay(files[0], &c); err != nil {
		t.Fatal(err)
	}
	if len(c.writes) != 1 || len(c.distills) != 1 || len(c.checkpoints) != 1 {
		t.Fatalf("got %d writes, %d distills, %d checkpoints", len(c.writes), len(c.distills), len(c.checkpoints))
	}
	if c.checkpoints[0].Concepts[0].Type != "rule" {
		t.Errorf("checkpoint concept type lost in round trip")
	}
}
