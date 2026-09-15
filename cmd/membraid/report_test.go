package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/shockalotti/membraid/internal/index"
	"github.com/shockalotti/membraid/internal/wirelog"
)

func TestMetricsReport(t *testing.T) {
	now := time.Now().UTC()
	v, cfg := testMetricsVault(t)
	ix := testIndex(t)

	old, err := ix.Write(index.Memory{Kind: index.KindInsight, Content: "the API returns 400 on a missing id", Scope: "g1", Source: "x"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := ix.Write(index.Memory{Kind: index.KindInsight, Content: "api returns 400 when id missing", Scope: "g1", Source: "y"})
	if err != nil {
		t.Fatal(err)
	}

	l, err := wirelog.Open(v.HotPath(), cfg.HostName())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	at := now.Add(-48 * time.Hour)
	keyed := "deploy.target"
	for i := 0; i < 3; i++ {
		if err := l.Append(wirelog.WriteLine{Header: wirelog.NewHeader(wirelog.TypeWrite, at), ID: fmt.Sprintf("k%d", i), Key: &keyed, Scope: "g1", Source: "x", Kind: index.KindInsight, Content: "k"}); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Hour)
	}
	for i := 0; i < 6; i++ {
		if err := l.Append(wirelog.WriteLine{Header: wirelog.NewHeader(wirelog.TypeWrite, at), ID: fmt.Sprintf("u%d", i), Scope: "g1", Source: "y", Kind: index.KindInsight, Content: "u"}); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Hour)
	}
	if err := l.Append(wirelog.WriteLine{Header: wirelog.NewHeader(wirelog.TypeWrite, at), ID: "t1", Scope: "g1", Source: "y", Kind: index.KindTaskState, Content: "decode the task"}); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Hour)
	if err := l.Append(wirelog.CloseLine{Header: wirelog.NewHeader(wirelog.TypeClose, at), ID: "t1"}); err != nil {
		t.Fatal(err)
	}

	p := metricsPath(v, cfg, now)
	for _, line := range []map[string]any{
		{"t": "miss", "ts": now.Add(-24 * time.Hour).UTC().Format(time.RFC3339), "host": "solo", "scope": "g1", "kind": "insight", "source": "y", "id": w.ID, "missed": old.ID, "score": 0.928},
		{"t": "miss", "ts": now.Add(-2 * time.Hour).UTC().Format(time.RFC3339), "host": "solo", "scope": "g1", "kind": "insight", "source": "y", "id": w.ID, "missed": old.ID, "score": 0.905},
		{"t": "snapshot", "ts": now.Add(-1 * time.Hour).UTC().Format(time.RFC3339), "host": "solo"},
	} {
		if err := appendMetricsLine(p, line); err != nil {
			t.Fatal(err)
		}
	}

	r, err := buildMetricsReport(v, ix, cfg.HostName(), 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Writes.Total != 10 || r.Writes.Unkeyed != 7 {
		t.Errorf("writes wrong: %+v", r.Writes)
	}
	if len(r.Misses.Rows) != 2 || r.Misses.Per100Unkeyed != 28.6 {
		t.Errorf("misses wrong: %d rows, rate %.1f", len(r.Misses.Rows), r.Misses.Per100Unkeyed)
	}
	if r.Misses.ByScope["g1"] != 2 {
		t.Errorf("by scope wrong: %+v", r.Misses.ByScope)
	}
	if r.Misses.MinScore != 0.905 || r.Misses.MaxScore != 0.928 {
		t.Errorf("score range wrong: %g-%g", r.Misses.MinScore, r.Misses.MaxScore)
	}
	if r.Tasks.Opened != 1 || r.Tasks.Closed != 1 {
		t.Errorf("tasks wrong: %+v", r.Tasks)
	}
	if r.Snapshots != 1 || r.Injections != 0 {
		t.Errorf("journal counts wrong: snapshots %d injections %d", r.Snapshots, r.Injections)
	}
	if len(r.Misses.Top) == 0 || r.Misses.Top[0].Content != "api returns 400 when id missing" || r.Misses.Top[0].MissedContent != "the API returns 400 on a missing id" {
		t.Errorf("top miss content not resolved: %+v", r.Misses.Top)
	}
	if out := r.Text(); len(out) < 40 {
		t.Errorf("text form too short: %q", out)
	}
}
