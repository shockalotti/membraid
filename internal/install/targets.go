package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Targets is every harness install knows, in the order it offers them.
func Targets() []Target {
	return []Target{claudeCode(), openCode(), codex(), copilot(), crush(), gemini(), grok(), hermes(), pi(), omarchyWidget(), embeddings()}
}

func claudeCode() Target {
	return Target{
		ID: "claude-code", Name: "Claude Code",
		Detect: func(e *Env) bool { return e.has("claude", ".claude") },
		Steps: func(e *Env) []Step {
			return []Step{
				{Desc: "MCP server " + ServerName + " in ~/.claude.json", Apply: func(e *Env) error {
					return editJSON(e.path(".claude.json"), func(doc map[string]any) bool {
						servers, _ := doc["mcpServers"].(map[string]any)
						if servers == nil {
							servers = map[string]any{}
						}
						changed := migrateLegacy(servers)
						want := map[string]any{"command": e.Bin, "args": []any{"mcp", "--source", "claude-code"}}
						if !sameJSON(servers[ServerName], want) {
							servers[ServerName] = want
							changed = true
						}
						doc["mcpServers"] = servers
						return changed
					})
				}},
				{Desc: "session digest: SessionStart hook in ~/.claude/settings.json", Apply: claudeHook},
				skillStep(filepath.Join(".claude", "skills", "membraid", "SKILL.md")),
			}
		},
	}
}

func claudeHook(e *Env) error {
	return sessionStartHook(e.path(".claude", "settings.json"), map[string]any{
		"type": "command", "command": e.Bin + " context --format claude 2>/dev/null || true", "timeout": 10,
		"statusMessage": "Loading membraid memory",
	})
}

// sessionStartHook leaves exactly one membraid SessionStart hook in a Claude
// Code style hooks file, pointing at the installed binary: an older one is
// updated in place, duplicates are removed, and hooks that are not membraid's
// are not touched. want is the whole hook entry; its command identifies it.
func sessionStartHook(path string, want map[string]any) error {
	command, _ := want["command"].(string)
	return editJSON(path, func(doc map[string]any) bool {
		hooks, _ := doc["hooks"].(map[string]any)
		if hooks == nil {
			hooks = map[string]any{}
		}
		groups, _ := hooks["SessionStart"].([]any)
		found, changed := false, false
		kept := []any{}
		for _, g := range groups {
			group, ok := g.(map[string]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			entries, _ := group["hooks"].([]any)
			keep := []any{}
			removed := false
			for _, h := range entries {
				hook, ok := h.(map[string]any)
				cmd, _ := hook["command"].(string)
				if ok && strings.Contains(cmd, "membraid") && strings.Contains(cmd, " context") {
					if found {
						removed, changed = true, true
						continue
					}
					found = true
					if cmd != command {
						hook["command"] = command
						changed = true
					}
				}
				keep = append(keep, h)
			}
			if removed && len(keep) == 0 {
				continue
			}
			group["hooks"] = keep
			kept = append(kept, group)
		}
		if !found {
			kept = append(kept, map[string]any{"hooks": []any{want}})
			changed = true
		}
		hooks["SessionStart"] = kept
		doc["hooks"] = hooks
		return changed
	})
}

func openCode() Target {
	return Target{
		ID: "opencode", Name: "OpenCode",
		Detect: func(e *Env) bool { return e.has("opencode", filepath.Join(".config", "opencode")) },
		Notes:  []string{"OpenCode reads skills from ~/.claude/skills, so the skill is installed there."},
		Steps: func(e *Env) []Step {
			dir := e.path(".config", "opencode")
			return []Step{
				{Desc: "MCP server " + ServerName + " in ~/.config/opencode/opencode.json", Apply: func(e *Env) error {
					cfg := filepath.Join(dir, "opencode.json")
					if _, err := os.Stat(cfg); os.IsNotExist(err) {
						if _, jerr := os.Stat(filepath.Join(dir, "opencode.jsonc")); jerr == nil {
							return fmt.Errorf("found opencode.jsonc, which may hold comments; add the %s server to it by hand", ServerName)
						}
					}
					return editJSON(cfg, func(doc map[string]any) bool {
						changed := false
						if _, ok := doc["$schema"]; !ok {
							doc["$schema"] = "https://opencode.ai/config.json"
							changed = true
						}
						servers, _ := doc["mcp"].(map[string]any)
						if servers == nil {
							servers = map[string]any{}
						}
						if migrateLegacy(servers) {
							changed = true
						}
						want := map[string]any{"type": "local", "command": []any{e.Bin, "mcp", "--source", "opencode"}, "enabled": true}
						if !sameJSON(servers[ServerName], want) {
							servers[ServerName] = want
							changed = true
						}
						doc["mcp"] = servers
						return changed
					})
				}},
				{Desc: "session digest: plugin at ~/.config/opencode/plugins/membraid.js", Apply: func(e *Env) error {
					return writeAsset("opencode/membraid.js", filepath.Join(dir, "plugins", "membraid.js"),
						pointAt("`${process.env.HOME}/go/bin/membraid`", strconv.Quote(e.Bin)))
				}},
				skillStep(filepath.Join(".claude", "skills", "membraid", "SKILL.md")),
			}
		},
	}
}

func grok() Target {
	return Target{
		ID: "grok", Name: "Grok",
		Detect: func(e *Env) bool { return e.has("grok", ".grok") },
		Notes: []string{
			"Grok ignores SessionStart hook output, so its session digest comes from a grok() function in ~/.bashrc or ~/.zshrc that passes it as --rules. It reaches grok started from a terminal; with another shell (fish, PowerShell) grok gets the MCP server and skill but no digest.",
			"Grok reads ~/.claude/skills, so it shares that copy of the skill; a second copy in ~/.grok/skills would collide with it.",
		},
		Steps: func(e *Env) []Step {
			steps := []Step{
				{Desc: "MCP server " + ServerName + " via grok mcp add (user scope)", Apply: func(e *Env) error {
					if legacyGrokEntry(e) {
						if out, err := e.Run("", "grok", "mcp", "remove", legacyName, "--scope", "user"); err != nil {
							return fmt.Errorf("grok mcp remove %s: %v: %s", legacyName, err, lastLine(out))
						}
					}
					if out, err := e.Run("", "grok", "mcp", "add", ServerName, e.Bin, "--scope", "user", "--", "mcp", "--source", "grok"); err != nil {
						return fmt.Errorf("grok mcp add: %v: %s", err, lastLine(out))
					}
					return nil
				}},
				skillStep(filepath.Join(".claude", "skills", "membraid", "SKILL.md")),
			}
			if rc := e.shellRC(); rc != "" {
				steps = append(steps, Step{
					Desc:  "session digest: grok() function in ~/" + filepath.Base(rc) + " passing membraid context as --rules",
					Apply: upsertGrokDigest,
				})
			}
			return steps
		},
	}
}

// legacyGrokEntry finds a pre-rename [mcp_servers.memory] table that runs membraid.
func legacyGrokEntry(e *Env) bool {
	raw, err := os.ReadFile(e.path(".grok", "config.toml"))
	if err != nil {
		return false
	}
	s := string(raw)
	i := strings.Index(s, "[mcp_servers."+legacyName+"]")
	if i < 0 {
		return false
	}
	section := s[i+1:]
	if j := strings.Index(section, "\n["); j >= 0 {
		section = section[:j]
	}
	return strings.Contains(section, "membraid") || strings.Contains(section, "memory-engine")
}

func hermes() Target {
	return Target{
		ID: "hermes", Name: "Hermes",
		Detect: func(e *Env) bool { return e.has("hermes", ".hermes") },
		Steps: func(e *Env) []Step {
			pluginDir := e.path(".hermes", "plugins", "membraid")
			return []Step{
				{Desc: "MCP server " + ServerName + " via hermes mcp add", Apply: hermesServer},
				{Desc: "session digest: plugin at ~/.hermes/plugins/membraid", Apply: func(e *Env) error {
					if err := writeAsset("hermes/plugin.yaml", filepath.Join(pluginDir, "plugin.yaml"), nil); err != nil {
						return err
					}
					if err := writeAsset("hermes/__init__.py", filepath.Join(pluginDir, "__init__.py"),
						pointAt(`os.path.expanduser("~/go/bin/membraid")`, strconv.Quote(e.Bin))); err != nil {
						return err
					}
					// Hermes asks whether a plugin may override built-in tools;
					// membraid overrides none, so it says no up front instead of being asked.
					if out, err := e.Run("", "hermes", "plugins", "enable", "--no-allow-tool-override", "membraid"); err != nil {
						return fmt.Errorf("hermes plugins enable: %v: %s", err, lastLine(out))
					}
					return nil
				}},
				skillStep(filepath.Join(".hermes", "skills", "membraid", "SKILL.md")),
			}
		},
	}
}

// hermesServer goes through Hermes's own CLI rather than editing config.yaml,
// so Hermes owns how its config is written. Its add command asks before
// enabling the server's tools, so the answer is supplied, and the result is
// read back rather than trusted.
func hermesServer(e *Env) error {
	cfgPath := e.path(".hermes", "config.yaml")
	servers := func() (map[string]any, error) {
		doc, err := readYAMLMap(cfgPath)
		if err != nil {
			return nil, err
		}
		s, _ := doc["mcp_servers"].(map[string]any)
		if s == nil {
			s = map[string]any{}
		}
		return s, nil
	}
	current, err := servers()
	if err != nil {
		return err
	}
	if entry, ok := current[legacyName]; ok && ours(entry) {
		if out, err := e.Run("y\n", "hermes", "mcp", "remove", legacyName); err != nil {
			return fmt.Errorf("hermes mcp remove %s: %v: %s", legacyName, err, lastLine(out))
		}
	}
	if entry, ok := current[ServerName]; ok {
		if hermesMatches(entry, e.Bin) {
			return nil
		}
		if out, err := e.Run("y\n", "hermes", "mcp", "remove", ServerName); err != nil {
			return fmt.Errorf("hermes mcp remove %s: %v: %s", ServerName, err, lastLine(out))
		}
	}
	out, err := e.Run("Y\n", "hermes", "mcp", "add", ServerName, "--command", e.Bin,
		"--connect-timeout", "20", "--args", "mcp", "--source", "hermes")
	if err != nil {
		return fmt.Errorf("hermes mcp add: %v: %s", err, lastLine(out))
	}
	after, err := servers()
	if err != nil {
		return err
	}
	if !hermesMatches(after[ServerName], e.Bin) {
		return fmt.Errorf("hermes mcp add reported success but %s is not in %s: %s", ServerName, cfgPath, lastLine(out))
	}
	return nil
}

func hermesMatches(entry any, bin string) bool {
	m, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	if cmd, _ := m["command"].(string); cmd != bin {
		return false
	}
	return sameJSON(m["args"], []any{"mcp", "--source", "hermes"})
}

func omarchyWidget() Target {
	const id = "shockalotti.membraid"
	return Target{
		ID: "omarchy-widget", Name: "Omarchy bar widget",
		Detect: func(e *Env) bool { _, err := e.LookPath("omarchy"); return err == nil },
		Steps: func(e *Env) []Step {
			dir := e.path(".config", "omarchy", "plugins", id)
			return []Step{
				{Desc: "bar widget files in ~/.config/omarchy/plugins/" + id, Apply: func(e *Env) error {
					if err := writeAsset("omarchy/manifest.json", filepath.Join(dir, "manifest.json"), nil); err != nil {
						return err
					}
					return writeAsset("omarchy/Panel.qml", filepath.Join(dir, "Panel.qml"),
						pointAt(`home + "/go/bin/membraid"`, strconv.Quote(e.Bin)))
				}},
				{Desc: "enable the widget, top right of the bar unless already placed", Apply: func(e *Env) error {
					for _, args := range [][]string{
						{"plugin", "enable", id},
						// put, not move: a widget the user has already placed stays where they put it.
						{"bar", "put", id, "--before", "omarchy.power"},
					} {
						if out, err := e.Run("", "omarchy", args...); err != nil {
							return fmt.Errorf("omarchy %s: %v: %s", strings.Join(args, " "), err, lastLine(out))
						}
					}
					return nil
				}},
			}
		},
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// embeddingModel is the Ollama model semantic search uses: the most accurate
// in the search evaluation, and the smallest EmbeddingGemma variant.
const embeddingModel = "embeddinggemma:300m-qat-q4_0"

// embeddings turns on optional semantic search. It is never preselected: it is
// the one target that downloads a model. With Ollama running it uses
// EmbeddingGemma through Ollama; otherwise the model built into membraid.
func embeddings() Target {
	return Target{
		ID: "embeddings", Name: "Semantic search (embeddings)",
		Detect: func(*Env) bool { return false },
		Notes: []string{
			"Embeddings are computed on this machine and stored only in its local index, never in the synced vault. Nothing is sent to any cloud service.",
		},
		Steps: func(e *Env) []Step {
			embed := func(desc string) Step {
				return Step{Desc: desc, Apply: func(e *Env) error {
					if out, err := e.Run("", e.Bin, "embed"); err != nil {
						return fmt.Errorf("membraid embed: %v: %s", err, lastLine(out))
					}
					return nil
				}}
			}
			set := func(kind string) Step {
				return Step{Desc: "set embeddings=" + kind, Apply: func(e *Env) error {
					if out, err := e.Run("", e.Bin, "config", "set", "embeddings", kind); err != nil {
						return fmt.Errorf("membraid config set embeddings %s: %v: %s", kind, err, lastLine(out))
					}
					return nil
				}}
			}
			if e.OllamaUp != nil && e.OllamaUp() {
				return []Step{
					{Desc: "pull " + embeddingModel + " (239 MB) via ollama pull", Apply: func(e *Env) error {
						if out, err := e.Run("", "ollama", "pull", embeddingModel); err != nil {
							return fmt.Errorf("ollama pull %s: %v: %s", embeddingModel, err, lastLine(out))
						}
						return nil
					}},
					set("ollama"),
					embed("embed existing memories"),
				}
			}
			return []Step{
				set("builtin"),
				embed("embed existing memories (downloads the built-in model, about 90 MB)"),
			}
		},
	}
}
