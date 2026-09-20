package session

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"golang.org/x/term"
)

// Package session tracks which agent sessions have received their digest: a
// local receipt book beside state.json, never synced. When a session is
// resumed, the hook can tell whether memory was ever loaded in it and say so
// loudly instead of assuming. A receipt book must never break the session it
// serves, so every error here is swallowed by design.

const ledgerFile = "sessions.json"

// maxEntries caps the ledger: old sessions fall off, the file never grows
// without bound on a machine that lives for years.
const maxEntries = 200

// Receipt is one session's delivery record.
type Receipt struct {
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
	Count     int    `json:"count"`
	Source    string `json:"source"`
}

// HookPayload reads a SessionStart hook payload from stdin: the harness
// passes session_id and source (startup, resume, clear, compact) there.
// Harnesses without hook payloads (the OpenCode plugin) pass the session id
// through MEMBRAID_SESSION_ID instead. Empty strings when there is neither —
// a terminal, an empty pipe, no env — so callers treat that as "not a hook".
func HookPayload() (sessionID, source string) {
	if id := os.Getenv("MEMBRAID_SESSION_ID"); id != "" {
		return id, ""
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return "", ""
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 64*1024))
	if err != nil || len(raw) == 0 {
		return "", ""
	}
	var payload struct {
		SessionID string `json:"session_id"`
		Source    string `json:"source"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", ""
	}
	return payload.SessionID, payload.Source
}

// Check records the receipt for sessionID and reports whether this delivery
// needs the loud banner: no prior receipt means memory was never loaded in
// this session, whatever the source, and the digest that follows is first
// contact. Harness hooks always name their source; plugin harnesses pass
// none, and an unseen session with no source gets the banner too — a fresh
// session hearing it once is the price of never staying silent. Empty
// sessionID means not a hook; nothing is recorded, no banner.
func Check(dir, sessionID, source string) string {
	if sessionID == "" {
		return ""
	}
	ledger := load(dir)
	_, seen := ledger[sessionID]
	now := time.Now().UTC().Format(time.RFC3339)
	if seen {
		r := ledger[sessionID]
		r.LastSeen = now
		r.Count++
		ledger[sessionID] = r
	} else {
		ledger[sessionID] = Receipt{FirstSeen: now, LastSeen: now, Count: 1, Source: source}
	}
	save(dir, ledger)
	if !seen && (source == "resume" || source == "") {
		return "MEMORY NEVER LOADED IN THIS SESSION — no digest was ever delivered here, so the digest below is first contact with shared memory. Read it before answering."
	}
	return ""
}

// Seen reports whether sessionID already has a receipt.
func Seen(dir, sessionID string) bool {
	_, ok := load(dir)[sessionID]
	return ok
}

func load(dir string) map[string]Receipt {
	ledger := map[string]Receipt{}
	raw, err := os.ReadFile(filepath.Join(dir, ledgerFile))
	if err != nil {
		return ledger
	}
	_ = json.Unmarshal(raw, &ledger)
	return ledger
}

func save(dir string, ledger map[string]Receipt) {
	if len(ledger) > maxEntries {
		type entry struct {
			id string
			at string
		}
		all := make([]entry, 0, len(ledger))
		for id, r := range ledger {
			all = append(all, entry{id, r.LastSeen})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].at < all[j].at })
		for _, e := range all[:len(ledger)-maxEntries] {
			delete(ledger, e.id)
		}
	}
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, ledgerFile), append(raw, '\n'), 0o644)
}
