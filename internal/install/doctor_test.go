package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A moved binary must show up as stale with the repair, a resolving hook as
// self-healing, and a live absolute path as live — never silently fine.
func TestDoctorClassifiesEveryConsumer(t *testing.T) {
	home := t.TempDir()
	mkhome := func(rel, body string, mode os.FileMode) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}

	dead := "/gone/go/bin/membraid"
	liveBin := filepath.Join(home, "bin", "membraid")
	mkhome("bin/membraid", "#!/bin/sh\n", 0o755)

	// Stale: old silent-form hook pointing at the corpse.
	mkhome(".claude/settings.json",
		`{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "`+dead+` context --format claude 2>/dev/null || true"}]}]}}`, 0o644)
	// Legacy: old silent form, but the path still executes — alive today,
	// silent the day the binary moves.
	mkhome(".codex/hooks.json",
		`{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "`+liveBin+` context 2>/dev/null || true"}]}]}}`, 0o644)
	// Self-healing: the resolving form install now writes (JSON-encoded,
	// like the real writers produce — the raw command holds quotes).
	q, _ := json.Marshal(digestCommand(dead, "cursor"))
	mkhome(".cursor/hooks.json",
		`{"hooks": {"sessionStart": [{"command": `+string(q)+`}]}}`, 0o644)
	// Live MCP entry vs dead one.
	mkhome(".claude.json",
		`{"mcpServers": {"membraid": {"command": "`+liveBin+`", "args": ["mcp"]}}}`, 0o644)
	mkhome(".cursor/mcp.json",
		`{"mcpServers": {"membraid": {"command": "`+dead+`", "args": ["mcp"]}}}`, 0o644)
	// Widget fallback pointing at the corpse.
	mkhome(".config/omarchy/plugins/shockalotti.membraid/Panel.qml",
		"readonly property var binCandidates: [\n    \""+dead+"\",\n]\n", 0o644)

	got := map[string]string{}
	for _, f := range Doctor(home) {
		got[f.Consumer] = f.State
	}
	want := map[string]string{
		"claude hook":    "stale",
		"codex hooks":    "legacy",
		"cursor hooks":   "self-healing",
		"claude mcp":     "live",
		"cursor mcp":     "stale",
		"omarchy widget": "stale",
	}
	for consumer, state := range want {
		if got[consumer] != state {
			t.Errorf("%s: want %s, got %q (all findings: %v)", consumer, state, got[consumer], got)
		}
	}
}

func TestDoctorEmptyHomeFindsNothing(t *testing.T) {
	if out := Doctor(t.TempDir()); len(out) != 0 {
		t.Errorf("empty home must yield no findings, got %v", out)
	}
}
