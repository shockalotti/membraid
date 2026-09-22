package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Doctor checks every harness consumer that points at a membraid binary and
// reports whether each pointer still resolves. It changes nothing: a stale
// pointer means re-running install, which replaces stale entries in place.
//
// A pointer is self-healing when it resolves dynamically (the digest command
// tries PATH before its install-time fallback), live when its absolute path
// still executes, and stale when it names a binary that is gone. Stale used
// to be silent - sessions started without their digest and nobody was told -
// so doctor exits non-zero when anything is stale.
type Finding struct {
	Consumer string // e.g. "claude hook"
	File     string // path relative to home
	Pointer  string // the command or binary the consumer holds
	State    string // self-healing, live, stale, or absent
}

func (f Finding) Stale() bool { return f.State == "stale" }

// Doctor walks the known consumers under home and classifies each one.
func Doctor(home string) []Finding {
	var out []Finding
	join := func(parts ...string) string { return filepath.Join(append([]string{home}, parts...)...) }

	hookFiles := []struct{ consumer, rel string }{
		{"claude hook", ".claude/settings.json"},
		{"cursor hooks", ".cursor/hooks.json"},
		{"copilot hook", ".copilot/hooks/membraid.json"},
		{"codex hooks", ".codex/hooks.json"},
	}
	for _, h := range hookFiles {
		for _, cmd := range hookCommands(join(h.rel)) {
			out = append(out, Finding{Consumer: h.consumer, File: h.rel, Pointer: cmd, State: commandState(cmd)})
		}
	}

	mcpFiles := []struct {
		consumer, rel string
		keys          []string
	}{
		{"claude mcp", ".claude.json", []string{"mcpServers"}},
		{"cursor mcp", ".cursor/mcp.json", []string{"mcpServers"}},
		{"copilot mcp", ".copilot/mcp-config.json", []string{"mcpServers"}},
		{"opencode mcp", ".config/opencode/opencode.json", []string{"mcp"}},
		{"crush mcp", ".config/crush/crush.json", []string{"mcp"}},
	}
	for _, m := range mcpFiles {
		if cmd, ok := mcpCommand(join(m.rel), m.keys); ok {
			out = append(out, Finding{Consumer: m.consumer, File: m.rel, Pointer: cmd, State: commandState(cmd)})
		}
	}

	if bin, ok := widgetBinary(join(".config", "omarchy", "plugins", "shockalotti.membraid", "Panel.qml")); ok {
		out = append(out, Finding{Consumer: "omarchy widget", File: ".config/omarchy/plugins/shockalotti.membraid/Panel.qml", Pointer: bin, State: commandState(bin)})
	}

	// Hermes and Grok keep their MCP entries outside JSON: YAML and TOML.
	// A doctor blind to them reports "nothing" on machines whose whole job
	// is consuming, which teaches distrust in the tool.
	if bin, ok := hermesCommand(join(".hermes", "config.yaml")); ok {
		out = append(out, Finding{Consumer: "hermes mcp", File: ".hermes/config.yaml", Pointer: bin, State: commandState(bin)})
	}
	if bin, ok := grokCommand(join(".grok", "config.toml")); ok {
		out = append(out, Finding{Consumer: "grok mcp", File: ".grok/config.toml", Pointer: bin, State: commandState(bin)})
	}
	if bin, ok := opencodePluginBinary(join(".config", "opencode", "plugins", "membraid.js")); ok {
		out = append(out, Finding{Consumer: "opencode plugin", File: ".config/opencode/plugins/membraid.js", Pointer: bin, State: commandState(bin)})
	}
	return out
}

// commandState classifies one held command or path. Absolute MCP entries can
// only ever be live or stale (their harness spawns them directly, no shell
// to resolve through), but a hook still in the old silent form is legacy:
// alive today, silent the day the binary moves.
func commandState(cmd string) string {
	if strings.Contains(cmd, "command -v membraid") {
		return "self-healing"
	}
	bin := cmd
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		bin = cmd[:i]
	}
	bin = strings.Trim(bin, `"'`)
	if bin == "" || !executable(bin) {
		return "stale"
	}
	if strings.Contains(cmd, "|| true") {
		return "legacy"
	}
	return "live"
}

func executable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}

// hookCommands returns every membraid digest command a hooks file holds.
func hookCommands(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, val := range t {
				if s, ok := val.(string); ok && (k == "command" || k == "bash") &&
					strings.Contains(s, "membraid") && strings.Contains(s, "context") {
					out = append(out, s)
				} else {
					walk(val)
				}
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(doc)
	return out
}

// mcpCommand returns the command of the membraid MCP server entry, if any.
func mcpCommand(path string, keys []string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", false
	}
	cur := doc
	for _, k := range keys {
		next, _ := cur[k].(map[string]any)
		cur = next
		if cur == nil {
			return "", false
		}
	}
	entry, _ := cur[ServerName].(map[string]any)
	cmd, _ := entry["command"].(string)
	if cmd == "" {
		return "", false
	}
	return cmd, true
}

var widgetBinRe = regexp.MustCompile(`"([^"]*membraid[^"]*)"`)
var tomlCommandRe = regexp.MustCompile(`(?m)^command\s*=\s*"([^"]+)"`)

// hermesCommand reads the membraid MCP command from Hermes YAML config.
func hermesCommand(path string) (string, bool) {
	doc, err := readYAMLMap(path)
	if err != nil {
		return "", false
	}
	servers, _ := doc["mcp_servers"].(map[string]any)
	entry, _ := servers[ServerName].(map[string]any)
	cmd, _ := entry["command"].(string)
	if cmd == "" {
		return "", false
	}
	return cmd, true
}

// grokCommand reads the membraid MCP command from Grok's TOML config.
func grokCommand(path string) (string, bool) {
	section, ok := tomlSection(path, "mcp_servers."+ServerName)
	if !ok {
		return "", false
	}
	m := tomlCommandRe.FindStringSubmatch(section)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// opencodePluginBinary reads the binary the plugin spawns for digests: the
// first quoted membraid path in the file.
func opencodePluginBinary(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if m := widgetBinRe.FindStringSubmatch(line); m != nil && strings.Contains(m[1], "/") {
			return m[1], true
		}
	}
	return "", false
}

// widgetBinary reads the installed binary path the widget falls back to: the
// first quoted membraid path in the binary block (the rewritten first
// candidate, or the legacy single fallback).
func widgetBinary(panel string) (string, bool) {
	raw, err := os.ReadFile(panel)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, `setting("binary"`) {
			continue
		}
		if m := widgetBinRe.FindStringSubmatch(line); m != nil && strings.Contains(m[1], "/") {
			return m[1], true
		}
	}
	return "", false
}
