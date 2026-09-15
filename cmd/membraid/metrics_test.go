package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shockalotti/membraid/internal/config"
	"github.com/shockalotti/membraid/internal/index"
	"github.com/shockalotti/membraid/internal/vault"
)

// testMetricsVault returns a vault whose .hot directory exists: journaling is a
// file write, not an index read, so the index is a plain test index.
func testMetricsVault(t *testing.T) (*vault.Vault, config.Config) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, vault.HotDir), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Host = "solo"
	return vault.Open(root), cfg
}

func metricsLines(t *testing.T, p string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	var out []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("undecodable line %q: %v", ln, err)
		}
		out = append(out, m)
	}
	return out
}

// A near-miss on an unkeyed write becomes one "miss" line; a write with no
// near-miss journals nothing.
func TestMissesAreJournaled(t *testing.T) {
	v, cfg := testMetricsVault(t)
	ix := testIndex(t)
	old, err := ix.Write(index.Memory{Kind: index.KindInsight, Content: "the staging DB is on port 5432", Scope: "g1", Source: "x"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := ix.Write(index.Memory{Kind: index.KindInsight, Content: "staging db is on port 5432", Scope: "g1", Source: "y"})
	if err != nil {
		t.Fatal(err)
	}
	recordMisses(v, cfg, res, index.Memory{Kind: index.KindInsight, Scope: res.Scope, Source: "y"})

	p := metricsPath(v, cfg, time.Now())
	lines := metricsLines(t, p)
	if len(lines) != 1 {
		t.Fatalf("want one miss line, got %d: %v", len(lines), lines)
	}
	m := lines[0]
	if m["t"] != "miss" || m["scope"] != "g1" || m["source"] != "y" || m["missed"] != old.ID || m["id"] != res.ID {
		t.Errorf("miss line misrecorded: %v", m)
	}
	if s, _ := m["score"].(float64); s != res.NearMiss[0].Score || s < 0.9 {
		t.Errorf("score not carried: %v (result %+v)", m["score"], res.NearMiss)
	}

	other, err := ix.Write(index.Memory{Kind: index.KindInsight, Content: "an unrelated new fact", Scope: "g1", Source: "x"})
	if err != nil {
		t.Fatal(err)
	}
	recordMisses(v, cfg, other, index.Memory{Kind: index.KindInsight, Scope: other.Scope, Source: "x"})
	if got := len(metricsLines(t, p)); got != 1 {
		t.Errorf("a write with no near-miss must journal nothing, got %d lines", got)
	}
}

// A torn fragment left by a crash is skipped as a line of its own: every
// record appended afterwards is still a parseable line.
func TestMetricsLineStartsOnAFreshLine(t *testing.T) {
	v, cfg := testMetricsVault(t)
	p := metricsPath(v, cfg, time.Now())
	if err := os.WriteFile(p, []byte(`{"t":"snapshot","ts":"2026-09-01T00:00:00Z"`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ts := range []string{"2026-09-02T00:00:00Z", "2026-09-03T00:00:00Z"} {
		if err := appendMetricsLine(p, map[string]any{"t": "snapshot", "ts": ts}); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var good []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			continue // the torn fragment from the crash
		}
		good = append(good, m)
	}
	if len(good) != 2 || good[1]["ts"] != "2026-09-03T00:00:00Z" {
		t.Errorf("both appended records must be parseable and unglued, got %v in %q", good, string(raw))
	}
}
