package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shockalotti/membraid/internal/config"
	"github.com/shockalotti/membraid/internal/index"
	"github.com/shockalotti/membraid/internal/vault"
	"github.com/shockalotti/membraid/internal/wirelog"
)

// The metrics journal (SPEC 18) records what observability looks like over
// time: one JSONL file per machine next to the wire log, committed and synced
// through the same git, so trends survive a rebuilt index and span machines.
// It is log-only and dark in v1: nothing reads it, nothing tunes from it, and
// no correctness path depends on it. Two line kinds are written:
//
//   - "snapshot": current health and engagement, when a scheduled sync finds
//     one due (metrics_every_days) or on `membraid metrics`.
//   - "miss": a write-time near-miss candidate - an unkeyed write restated a
//     current memory closely enough to suggest the search before it failed to
//     surface it, but not closely enough to supersede it (index Write).
//
// Errors here must never fail the write or sync they sit next to: the journal
// is telemetry, not truth, so every failure is dropped where it happens.

func metricsFileName(host string, t time.Time) string {
	return fmt.Sprintf("metrics-%04d-%02d-%s.jsonl", t.Year(), int(t.Month()), wirelog.SafeHost(host))
}

// metricsPath is this machine's journal file for the month of t.
func metricsPath(v *vault.Vault, cfg config.Config, t time.Time) string {
	return filepath.Join(v.HotPath(), metricsFileName(cfg.HostName(), t))
}

// appendMetricsLine appends one newline-terminated JSON object, starting on a
// fresh line so a torn fragment from a crash never glues onto this record.
func appendMetricsLine(p string, obj any) error {
	buf, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err == nil && last[0] != '\n' {
			buf = append([]byte{'\n'}, buf...)
		}
	}
	if _, err := f.Write(append(buf, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// snapshotLine assembles one snapshot: the same health and engagement numbers
// status and insights print, as one JSON object for the journal.
func snapshotLine(ix *index.Index, cfg config.Config) (map[string]any, error) {
	st, err := ix.Stats()
	if err != nil {
		return nil, err
	}
	in, err := ix.Insights(7, time.Local)
	if err != nil {
		return nil, err
	}
	if in.KeyDrift, err = ix.KeyDrift("*"); err != nil {
		return nil, err
	}
	if in.KeyDrift == nil {
		in.KeyDrift = []index.KeyPair{}
	}
	ss := config.LoadState()
	return map[string]any{
		"t":        "snapshot",
		"ts":       time.Now().UTC().Format(time.RFC3339),
		"host":     wirelog.SafeHost(cfg.HostName()),
		"version":  currentVersion(),
		"stats":    st,
		"insights": in,
		"unscoped": ix.UnscopedCount(),
		"sweep":    ix.LastSweep(),
		"search":   searchStatus(cfg, ix),
		"sync": map[string]any{
			"enabled": cfg.AutoSync, "last_success": ss.LastSuccess, "last_error": ss.LastError,
			"last_skipped": ss.LastSkipped, "interval_min": cfg.PullIntervalMin,
		},
	}, nil
}

// appendSnapshot appends one snapshot line now, never counting it as retrieval,
// and returns the line itself so `membraid metrics --json` prints exactly what
// was written.
func appendSnapshot(v *vault.Vault, ix *index.Index, cfg config.Config) (map[string]any, error) {
	line, err := snapshotLine(ix, cfg)
	if err != nil {
		return nil, err
	}
	if err := appendMetricsLine(metricsPath(v, cfg, time.Now()), line); err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	ss := config.LoadState()
	ss.LastMetricsAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := ss.Save(); err != nil {
		return nil, err
	}
	return line, nil
}

// metricsDue reports whether the scheduled sync should append a snapshot now:
// metrics_every_days > 0 and no snapshot at or after that many days ago.
func metricsDue(cfg config.Config) bool {
	if cfg.MetricsEveryDays <= 0 {
		return false
	}
	last := config.LoadState().LastMetricsAt
	if last == "" {
		return true
	}
	at, err := time.Parse(time.RFC3339Nano, last)
	if err != nil {
		return true
	}
	return time.Since(at) >= time.Duration(cfg.MetricsEveryDays)*24*time.Hour
}

// recordMisses journals the near-miss candidates an unkeyed write produced
// (SPEC 18): memories it restated closely enough to be the same fact the
// search before it failed to surface, but not enough to replace. Candidates,
// not verdicts - the journal is what a later version labels. Failure to
// journal never fails the write.
func recordMisses(v *vault.Vault, cfg config.Config, r *index.WriteResult, w index.Memory) {
	if len(r.NearMiss) == 0 {
		return
	}
	at := time.Now()
	p := metricsPath(v, cfg, at)
	for _, nm := range r.NearMiss {
		line := map[string]any{
			"t": "miss", "ts": at.UTC().Format(time.RFC3339), "host": wirelog.SafeHost(cfg.HostName()),
			"scope": r.Scope, "kind": w.Kind, "source": w.Source,
			"id": r.ID, "missed": nm.ID, "score": nm.Score,
		}
		if w.SessionRef != "" {
			line["session"] = w.SessionRef
		}
		_ = appendMetricsLine(p, line)
	}
}
