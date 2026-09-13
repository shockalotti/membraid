package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func codex() Target {
	return Target{
		ID: "codex", Name: "Codex",
		Detect: func(e *Env) bool { return e.has("codex", ".codex") },
		Notes: []string{
			"Codex runs a new or changed hook only once you trust it: start Codex, run /hooks and trust membraid's SessionStart hook. Until then sessions start without the digest; the MCP server and skill work regardless.",
			"Codex reads skills from ~/.agents/skills, not ~/.claude/skills, so it gets its own copy there.",
		},
		Steps: func(e *Env) []Step {
			return []Step{
				{Desc: "MCP server " + ServerName + " via codex mcp add", Apply: codexServer},
				{Desc: "session digest: SessionStart hook in ~/.codex/hooks.json", Apply: func(e *Env) error {
					return sessionStartHook(e.path(".codex", "hooks.json"), map[string]any{
						"type": "command", "command": e.Bin + " context 2>/dev/null || true", "timeout": 10,
					})
				}},
				skillStep(filepath.Join(".agents", "skills", "membraid", "SKILL.md")),
			}
		},
	}
}

// codexServer goes through Codex's own CLI, which owns config.toml. An entry
// already pointing at this binary is left alone; a stale one is replaced.
func codexServer(e *Env) error {
	cfg := e.path(".codex", "config.toml")
	if section, ok := tomlSection(cfg, "mcp_servers."+legacyName); ok && (strings.Contains(section, "membraid") || strings.Contains(section, "memory-engine")) {
		if out, err := e.Run("", "codex", "mcp", "remove", legacyName); err != nil {
			return fmt.Errorf("codex mcp remove %s: %v: %s", legacyName, err, lastLine(out))
		}
	}
	if section, ok := tomlSection(cfg, "mcp_servers."+ServerName); ok {
		if strings.Contains(section, "command = "+strconv.Quote(e.Bin)) && strings.Contains(section, `"--source", "codex"`) {
			return nil
		}
		if out, err := e.Run("", "codex", "mcp", "remove", ServerName); err != nil {
			return fmt.Errorf("codex mcp remove %s: %v: %s", ServerName, err, lastLine(out))
		}
	}
	if out, err := e.Run("", "codex", "mcp", "add", ServerName, "--", e.Bin, "mcp", "--source", "codex"); err != nil {
		return fmt.Errorf("codex mcp add: %v: %s", err, lastLine(out))
	}
	return nil
}

// tomlSection returns the body of the [name] table in a TOML file, up to the
// next table header, and whether the table exists. Enough to recognise an
// entry; install never writes TOML itself.
func tomlSection(path, name string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	s := string(raw)
	header := "[" + name + "]"
	i := strings.Index(s, header)
	if i < 0 {
		return "", false
	}
	section := s[i+len(header):]
	if j := strings.Index(section, "\n["); j >= 0 {
		section = section[:j]
	}
	return section, true
}

func copilot() Target {
	return Target{
		ID: "copilot", Name: "GitHub Copilot CLI",
		Detect: func(e *Env) bool { return e.has("copilot", ".copilot") },
		Notes: []string{
			"Copilot CLI puts only allowlisted MCP servers' instructions in the prompt, so its sessionStart hook brings membraid's instructions along with the digest.",
			"Copilot CLI reads skills from ~/.agents/skills, so it shares that copy. If you set COPILOT_HOME, Copilot stops reading ~/.agents/skills and the skill will not load.",
		},
		Steps: func(e *Env) []Step {
			return []Step{
				{Desc: "MCP server " + ServerName + " in ~/.copilot/mcp-config.json", Apply: func(e *Env) error {
					return editJSON(e.path(".copilot", "mcp-config.json"), func(doc map[string]any) bool {
						servers, _ := doc["mcpServers"].(map[string]any)
						if servers == nil {
							servers = map[string]any{}
						}
						changed := migrateLegacy(servers)
						want := map[string]any{"type": "local", "command": e.Bin, "args": []any{"mcp", "--source", "copilot"}, "tools": []any{"*"}}
						if !sameJSON(servers[ServerName], want) {
							servers[ServerName] = want
							changed = true
						}
						doc["mcpServers"] = servers
						return changed
					})
				}},
				{Desc: "instructions and session digest: sessionStart hook at ~/.copilot/hooks/membraid.json", Apply: func(e *Env) error {
					return writeOwnedJSON(e.path(".copilot", "hooks", "membraid.json"), map[string]any{
						"version": 1,
						"hooks": map[string]any{"sessionStart": []any{map[string]any{
							"type": "command", "bash": e.Bin + " context --format copilot 2>/dev/null || true", "timeoutSec": 10,
						}}},
					})
				}},
				skillStep(filepath.Join(".agents", "skills", "membraid", "SKILL.md")),
			}
		},
	}
}

// writeOwnedJSON writes a file membraid owns outright, such as its own hook
// file, skipping the write when it already holds exactly that content.
func writeOwnedJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if cur, err := os.ReadFile(path); err == nil && string(cur) == string(b) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func crush() Target {
	return Target{
		ID: "crush", Name: "Crush",
		Detect: func(e *Env) bool { return e.has("crush", filepath.Join(".config", "crush")) },
		Notes: []string{
			"Crush has no session-start hook, but it puts MCP server instructions in the system prompt, so the server runs with --digest and the digest arrives with them when Crush starts.",
			"Crush reads skills from ~/.claude/skills, so it shares that copy of the skill.",
		},
		Steps: func(e *Env) []Step {
			dir := e.path(".config", "crush")
			return []Step{
				{Desc: "MCP server " + ServerName + ", with the session digest, in ~/.config/crush/crush.json", Apply: func(e *Env) error {
					if _, err := os.Stat(filepath.Join(dir, "crushrc")); err == nil {
						return fmt.Errorf("found ~/.config/crush/crushrc, so your Crush config is kept there; add the server to it by hand: mcp add %s --command %s --args mcp --args --source --args crush --args --digest", ServerName, e.Bin)
					}
					return editJSON(filepath.Join(dir, "crush.json"), func(doc map[string]any) bool {
						changed := false
						if _, ok := doc["$schema"]; !ok {
							doc["$schema"] = "https://charm.land/crush.json"
							changed = true
						}
						servers, _ := doc["mcp"].(map[string]any)
						if servers == nil {
							servers = map[string]any{}
						}
						if migrateLegacy(servers) {
							changed = true
						}
						want := map[string]any{"type": "stdio", "command": e.Bin, "args": []any{"mcp", "--source", "crush", "--digest"}}
						if !sameJSON(servers[ServerName], want) {
							servers[ServerName] = want
							changed = true
						}
						doc["mcp"] = servers
						return changed
					})
				}},
				skillStep(filepath.Join(".claude", "skills", "membraid", "SKILL.md")),
			}
		},
	}
}

func gemini() Target {
	return Target{
		ID: "gemini", Name: "Gemini CLI",
		Detect: func(e *Env) bool { return e.has("gemini", ".gemini") },
		Notes: []string{
			"Gemini CLI runs user MCP servers only in folders you trust, and asks the first time you open one. The digest comes with membraid's server instructions (--digest), so it follows the same rule.",
			"Gemini CLI reads skills from ~/.agents/skills, so it shares that copy; a second copy in ~/.gemini/skills would draw a conflict warning.",
		},
		Steps: func(e *Env) []Step {
			return []Step{
				{Desc: "MCP server " + ServerName + ", with the session digest, via gemini mcp add (user scope)", Apply: geminiServer},
				skillStep(filepath.Join(".agents", "skills", "membraid", "SKILL.md")),
			}
		},
	}
}

// geminiServer goes through Gemini CLI's own command, which owns settings.json.
// Its add replaces an entry whole, so an entry already running this binary with
// these args is left alone, keeping anything the user added to it, like trust.
func geminiServer(e *Env) error {
	var doc struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if raw, err := os.ReadFile(e.path(".gemini", "settings.json")); err == nil {
		_ = json.Unmarshal(raw, &doc)
	}
	if entry, ok := doc.MCPServers[legacyName]; ok && ours(entry) {
		if out, err := e.Run("", "gemini", "mcp", "remove", "-s", "user", legacyName); err != nil {
			return fmt.Errorf("gemini mcp remove %s: %v: %s", legacyName, err, lastLine(out))
		}
	}
	args := []string{"mcp", "--source", "gemini", "--digest"}
	if m, ok := doc.MCPServers[ServerName].(map[string]any); ok && m["command"] == e.Bin && sameJSON(m["args"], args) {
		return nil
	}
	if out, err := e.Run("", "gemini", append([]string{"mcp", "add", "-s", "user", ServerName, e.Bin}, args...)...); err != nil {
		return fmt.Errorf("gemini mcp add: %v: %s", err, lastLine(out))
	}
	return nil
}

func pi() Target {
	return Target{
		ID: "pi", Name: "Pi",
		Detect: func(e *Env) bool { return e.has("pi", ".pi") },
		Notes: []string{
			"Pi has no MCP support, so a Pi extension runs membraid's MCP server for each session, registers its tools, and adds the digest to the system prompt.",
			"Pi reads skills from ~/.agents/skills, not ~/.claude/skills, so it gets its own copy there, shared with Codex.",
		},
		Steps: func(e *Env) []Step {
			return []Step{
				{Desc: "tools and session digest: extension at ~/.pi/agent/extensions/membraid.ts", Apply: func(e *Env) error {
					return writeAsset("pi/membraid.ts", e.path(".pi", "agent", "extensions", "membraid.ts"),
						pointAt("`${process.env.HOME}/go/bin/membraid`", strconv.Quote(e.Bin)))
				}},
				skillStep(filepath.Join(".agents", "skills", "membraid", "SKILL.md")),
			}
		},
	}
}
