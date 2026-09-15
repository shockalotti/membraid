package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shockalotti/membraid/internal/index"
	"github.com/shockalotti/membraid/internal/vault"
	"github.com/shockalotti/membraid/internal/wirelog"
)

// buildMetricsReport aggregates the observability journal and the wire log
// into the feedback-loop signals SPEC 18 tracks, over the last days days on
// this machine: near-misses per write, reported use, and task openings versus
// completions. It reads only; nothing is appended. It is the weekly ritual of
// the two-week frame experiment: numbers mean something relative to the last
// report, and a human (or agent) greps the top misses to see the restatements
// themselves.
//
// Sources: misses and snapshots come from the journal, and injections will
// too, once the frame (the relevance-scored memory bundle put in front of the
// model at session start) is built; writes and task lines come from the wire
// log; use and never-used come from the live store, because they are exact
// there (the journal only archives them weekly).
func buildMetricsReport(v *vault.Vault, ix *index.Index, host string, days int, now time.Time) (*metricsReport, error) {
	r := &metricsReport{Host: wirelog.SafeHost(host), WindowDays: days}
	r.Misses.ByScope = map[string]int{}
	since := now.UTC().AddDate(0, 0, -days)

	files, err := wirelog.Files(v.HotPath())
	if err != nil {
		return nil, err
	}
	for _, p := range files {
		entries, _, err := wirelog.ReadFrom(p, 0)
		if err != nil {
			return nil, fmt.Errorf("metrics: read %s: %w", filepath.Base(p), err)
		}
		for i := range entries {
			e := &entries[i]
			if e.Time().Before(since) {
				continue
			}
			switch {
			case e.Write != nil:
				r.Writes.Total++
				if e.Write.Key == nil {
					r.Writes.Unkeyed++
				}
				if e.Write.Kind == index.KindTaskState {
					r.Tasks.Opened++
				}
			case e.Close != nil:
				r.Tasks.Closed++
			}
		}
	}

	jfiles, err := filepath.Glob(filepath.Join(v.HotPath(), "metrics-*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(jfiles)
	var missScores []float64
	for _, p := range jfiles {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("metrics: read %s: %w", filepath.Base(p), err)
		}
		for _, ln := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(ln) == "" {
				continue
			}
			var line map[string]any
			if err := json.Unmarshal([]byte(ln), &line); err != nil {
				continue // a torn line cut by a crash is skipped
			}
			at, ok := lineTime(line)
			if !ok || at.Before(since) {
				continue
			}
			switch line["t"] {
			case "miss":
				miss := missRow{
					ID: str(line["id"]), Missed: str(line["missed"]),
					Scope: str(line["scope"]), Kind: str(line["kind"]), Source: str(line["source"]),
					Score: num(line["score"]),
				}
				r.Misses.Rows = append(r.Misses.Rows, miss)
				missScores = append(missScores, miss.Score)
				r.Misses.ByScope[str(line["scope"])]++
			case "snapshot":
				r.Snapshots++
				if r.LatestAt == "" || at.After(r.latest) {
					r.latest = at
					r.LatestAt = at.UTC().Format(time.RFC3339)
				}
			case "inject":
				r.Injections++
			}
		}
	}

	sort.SliceStable(missScores, func(i, j int) bool { return missScores[i] < missScores[j] })
	if len(missScores) > 0 {
		r.Misses.MinScore = missScores[0]
		r.Misses.MaxScore = missScores[len(missScores)-1]
		r.Misses.MedianScore = median(missScores)
	}
	if r.Writes.Unkeyed > 0 {
		r.Misses.Per100Unkeyed = math.Round(100*float64(len(missScores))/float64(r.Writes.Unkeyed)*10) / 10
	}

	top := append([]missRow(nil), r.Misses.Rows...)
	sort.SliceStable(top, func(i, j int) bool { return top[i].Score > top[j].Score })
	if len(top) > 5 {
		top = top[:5]
	}
	for i := range top {
		if m, ok, err := ix.Memory(top[i].ID); err == nil && ok {
			top[i].Content = m.Content
		}
		if m, ok, err := ix.Memory(top[i].Missed); err == nil && ok {
			top[i].MissedContent = m.Content
		}
	}
	r.Misses.Top = top

	if in, err := ix.Insights(days, time.Local); err == nil {
		r.Use = useSummary{
			BySource:     in.UsesBySource,
			NeverUsed:    in.NeverUsed,
			Current:      in.Current,
			RecentlyUsed: len(in.RecentlyUsed),
		}
	}

	return r, nil
}

// lineTime parses a journal line's ts, which writers stamp as RFC3339 (the
// journal) or RFC3339Nano (snapshot bookkeeping); RFC3339Nano accepts both.
func lineTime(m map[string]any) (time.Time, bool) {
	s, _ := m["ts"].(string)
	if s == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

func str(v any) string  { s, _ := v.(string); return s }
func num(v any) float64 { f, _ := v.(float64); return f }

func median(xs []float64) float64 {
	n := len(xs)
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}

type metricsReport struct {
	Host       string     `json:"host"`
	WindowDays int        `json:"window_days"`
	Writes     writesSumm `json:"writes"`
	Misses     missesSumm `json:"misses"`
	Use        useSummary `json:"use"`
	Tasks      tasksSumm  `json:"tasks"`
	Snapshots  int        `json:"snapshots_in_window"`
	LatestAt   string     `json:"snapshot_latest_at,omitempty"`
	Injections int        `json:"injections"`
	latest     time.Time  `json:"-"`
}

type writesSumm struct {
	Total   int `json:"total"`
	Unkeyed int `json:"unkeyed"`
}

type missesSumm struct {
	Rows          []missRow      `json:"-"`
	ByScope       map[string]int `json:"by_scope"`
	Per100Unkeyed float64        `json:"per_100_unkeyed"`
	MinScore      float64        `json:"min_score"`
	MedianScore   float64        `json:"median_score"`
	MaxScore      float64        `json:"max_score"`
	Top           []missRow      `json:"top"`
}

type missRow struct {
	ID            string  `json:"id"`
	Missed        string  `json:"missed"`
	Scope         string  `json:"scope"`
	Kind          string  `json:"kind"`
	Source        string  `json:"source"`
	Score         float64 `json:"score"`
	Content       string  `json:"content,omitempty"`
	MissedContent string  `json:"missed_content,omitempty"`
}

type useSummary struct {
	BySource     map[string]float64 `json:"by_source"`
	NeverUsed    int                `json:"never_used"`
	Current      int                `json:"current"`
	RecentlyUsed int                `json:"recently_used"`
}

type tasksSumm struct {
	Opened int `json:"opened"`
	Closed int `json:"closed"`
}

func (r *metricsReport) Text() string {
	var b strings.Builder
	rate := "n/a"
	if r.Writes.Unkeyed > 0 {
		rate = fmt.Sprintf("%.1f per 100 unkeyed writes", r.Misses.Per100Unkeyed)
	}
	fmt.Fprintf(&b, "metrics report - last %d days on %s\n", r.WindowDays, r.Host)
	fmt.Fprintf(&b, "  writes            %d (%d unkeyed)\n", r.Writes.Total, r.Writes.Unkeyed)
	fmt.Fprintf(&b, "  misses            %d (%s)", len(r.Misses.Rows), rate)
	if len(r.Misses.Rows) > 0 {
		fmt.Fprintf(&b, "; scores %.4g-%.4g, median %.4g", r.Misses.MinScore, r.Misses.MaxScore, r.Misses.MedianScore)
	}
	fmt.Fprintln(&b)
	if len(r.Misses.ByScope) > 0 {
		scopes := make([]string, 0, len(r.Misses.ByScope))
		for s := range r.Misses.ByScope {
			scopes = append(scopes, s)
		}
		sort.Strings(scopes)
		parts := make([]string, 0, len(scopes))
		for _, s := range scopes {
			parts = append(parts, fmt.Sprintf("%s: %d", s, r.Misses.ByScope[s]))
		}
		fmt.Fprintf(&b, "    by scope         %s\n", strings.Join(parts, ", "))
	}
	for _, m := range r.Misses.Top {
		fmt.Fprintf(&b, "    %.4g  %s %s/%s: %s restated %s\n", m.Score, m.Kind, m.Scope, m.Source, short(m.ID), short(m.Missed))
		if m.Content != "" {
			fmt.Fprintf(&b, "      wrote:  %s\n", m.Content)
		}
		if m.MissedContent != "" {
			fmt.Fprintf(&b, "      missed: %s\n", m.MissedContent)
		}
	}
	b.WriteString("  use              ")
	if r.Use.BySource == nil {
		b.WriteString("(no use reports)")
	} else {
		total := 0.0
		srcs := make([]string, 0, len(r.Use.BySource))
		for s, n := range r.Use.BySource {
			total += n
			srcs = append(srcs, s)
		}
		sort.Strings(srcs)
		parts := make([]string, 0, len(srcs))
		for _, s := range srcs {
			parts = append(parts, fmt.Sprintf("%s: %.0f", s, r.Use.BySource[s]))
		}
		fmt.Fprintf(&b, "%.0f reports", total)
		if len(parts) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(parts, ", "))
		}
		fmt.Fprintf(&b, "; %d of %d never used; %d used recently", r.Use.NeverUsed, r.Use.Current, r.Use.RecentlyUsed)
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "  tasks            %d opened, %d closed\n", r.Tasks.Opened, r.Tasks.Closed)
	latest := "none"
	if r.LatestAt != "" {
		latest = r.LatestAt
	}
	fmt.Fprintf(&b, "  snapshots        %d in window (latest %s)\n", r.Snapshots, latest)
	fmt.Fprintf(&b, "  injections       %d (times a session-start frame was delivered and journaled; 0 until the frame is built)\n", r.Injections)
	return strings.TrimSuffix(b.String(), "\n")
}

// short is a one-liner id for the human reader.
func short(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + ".."
}
