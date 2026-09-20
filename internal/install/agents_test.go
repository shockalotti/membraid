package install

import (
	"os"
	"strings"
	"testing"
)

func TestCodexMigratesThroughItsCLI(t *testing.T) {
	e, r := newEnv(t)
	write(t, e.path(".codex", "config.toml"), "[mcp_servers.memory]\ncommand = \"/home/x/go/bin/membraid\"\nargs = [\"mcp\"]\n\n[mcp_servers.railway]\ncommand = \"railway\"\n")
	write(t, e.path(".codex", "hooks.json"), `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "echo mine"}]}]}}`)
	apply(t, e, "codex")

	want := []string{
		"|codex mcp remove memory",
		"|codex mcp add membraid -- " + bin + " mcp --source codex",
	}
	if strings.Join(r.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(r.calls, "\n"), strings.Join(want, "\n"))
	}
	groups := readJSON(t, e.path(".codex", "hooks.json"))["hooks"].(map[string]any)["SessionStart"].([]any)
	var commands []string
	for _, g := range groups {
		for _, h := range g.(map[string]any)["hooks"].([]any) {
			commands = append(commands, h.(map[string]any)["command"].(string))
		}
	}
	if len(commands) != 2 || commands[0] != "echo mine" || commands[1] != digestCommand(bin, "") {
		t.Errorf("want the user's hook kept and one membraid hook added, got %q", commands)
	}
	if got, _ := os.ReadFile(e.path(".agents", "skills", "membraid", "SKILL.md")); string(got) != skill(t) {
		t.Error("Codex reads skills from ~/.agents/skills")
	}
}

// A server entry already pointing at this binary is left alone; one pointing
// elsewhere is replaced.
func TestCodexServerCurrentOrStale(t *testing.T) {
	e, r := newEnv(t)
	write(t, e.path(".codex", "config.toml"), "[mcp_servers.membraid]\ncommand = \""+bin+"\"\nargs = [\"mcp\", \"--source\", \"codex\"]\n")
	apply(t, e, "codex")
	if len(r.calls) != 0 {
		t.Errorf("a current entry needs no CLI calls, got %q", r.calls)
	}

	s, r2 := newEnv(t)
	write(t, s.path(".codex", "config.toml"), "[mcp_servers.membraid]\ncommand = \"/old/membraid\"\nargs = [\"mcp\", \"--source\", \"codex\"]\n")
	apply(t, s, "codex")
	if len(r2.calls) != 2 || r2.calls[0] != "|codex mcp remove membraid" {
		t.Errorf("a stale entry must be removed and added again, got %q", r2.calls)
	}
}

func TestCrush(t *testing.T) {
	e, _ := newEnv(t)
	cfg := e.path(".config", "crush", "crush.json")
	write(t, cfg, `{"options": {"debug": true}, "mcp": {"memory": {"type": "stdio", "command": "/home/x/go/bin/membraid", "args": ["mcp"]}}}`)
	apply(t, e, "crush")

	doc := readJSON(t, cfg)
	if doc["options"].(map[string]any)["debug"] != true {
		t.Error("other settings must survive")
	}
	servers := doc["mcp"].(map[string]any)
	if _, ok := servers["memory"]; ok {
		t.Error("legacy entry must be migrated")
	}
	m := servers["membraid"].(map[string]any)
	if m["type"] != "stdio" || m["command"] != bin || strings.Join(toStrings(m["args"]), " ") != "mcp --source crush --digest" {
		t.Errorf("membraid entry wrong: %v", m)
	}
	if _, err := os.Stat(e.path(".claude", "skills", "membraid", "SKILL.md")); err != nil {
		t.Error("Crush reads the shared ~/.claude/skills copy")
	}

	before, _ := os.ReadFile(cfg)
	apply(t, e, "crush")
	if after, _ := os.ReadFile(cfg); string(after) != string(before) {
		t.Error("a second install must change nothing")
	}
}

// Crush is moving its config to crushrc; a JSON file beside one would split it.
func TestCrushLeavesCrushrcAlone(t *testing.T) {
	e, _ := newEnv(t)
	write(t, e.path(".config", "crush", "crushrc"), "option debug true\n")
	err := Apply(e, target("crush"))
	if err == nil || !strings.Contains(err.Error(), "crushrc") {
		t.Fatalf("want a crushrc refusal, got %v", err)
	}
	if _, serr := os.Stat(e.path(".config", "crush", "crush.json")); serr == nil {
		t.Error("must not create crush.json beside a crushrc")
	}
}

func TestPi(t *testing.T) {
	e, _ := newEnv(t)
	apply(t, e, "pi")
	ext, err := os.ReadFile(e.path(".pi", "agent", "extensions", "membraid.ts"))
	if err != nil {
		t.Fatal("the extension must be installed")
	}
	if !strings.Contains(string(ext), `"`+bin+`"`) || strings.Contains(string(ext), "go/bin/membraid") {
		t.Errorf("extension must point at the installed binary:\n%s", ext)
	}
	if !strings.Contains(string(ext), `"--source", "pi", "--digest"`) {
		t.Error("the extension must start the server as pi, with the digest")
	}
	if got, _ := os.ReadFile(e.path(".agents", "skills", "membraid", "SKILL.md")); string(got) != skill(t) {
		t.Error("Pi reads skills from ~/.agents/skills")
	}
}

func TestGeminiMigratesThroughItsCLI(t *testing.T) {
	e, r := newEnv(t)
	write(t, e.path(".gemini", "settings.json"), `{"security": {"auth": {"selectedType": "oauth-personal"}}, "mcpServers": {"memory": {"command": "/home/x/go/bin/membraid", "args": ["mcp"]}}}`)
	apply(t, e, "gemini")
	want := []string{
		"|gemini mcp remove -s user memory",
		"|gemini mcp add -s user membraid " + bin + " mcp --source gemini --digest",
	}
	if strings.Join(r.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(r.calls, "\n"), strings.Join(want, "\n"))
	}
	if got, _ := os.ReadFile(e.path(".agents", "skills", "membraid", "SKILL.md")); string(got) != skill(t) {
		t.Error("Gemini CLI reads skills from ~/.agents/skills")
	}
	if _, err := os.Stat(e.path(".gemini", "skills", "membraid")); err == nil {
		t.Error("a ~/.gemini/skills copy conflicts with the ~/.agents/skills one")
	}
}

// Gemini's add replaces an entry whole, so a current one, perhaps trusted by
// the user, is not re-added.
func TestGeminiCurrentServerIsLeftAlone(t *testing.T) {
	e, r := newEnv(t)
	write(t, e.path(".gemini", "settings.json"), `{"mcpServers": {"membraid": {"command": "`+bin+`", "args": ["mcp", "--source", "gemini", "--digest"], "trust": true}}}`)
	apply(t, e, "gemini")
	if len(r.calls) != 0 {
		t.Errorf("a current entry needs no CLI calls, got %q", r.calls)
	}
}

func TestCopilot(t *testing.T) {
	e, _ := newEnv(t)
	cfg := e.path(".copilot", "mcp-config.json")
	write(t, cfg, `{"mcpServers": {"github": {"type": "http", "url": "https://example.test"}, "memory": {"type": "local", "command": "/home/x/go/bin/membraid", "args": ["mcp"]}}}`)
	apply(t, e, "copilot")

	servers := readJSON(t, cfg)["mcpServers"].(map[string]any)
	if _, ok := servers["github"]; !ok {
		t.Error("other servers must survive")
	}
	if _, ok := servers["memory"]; ok {
		t.Error("legacy entry must be migrated")
	}
	m := servers["membraid"].(map[string]any)
	if m["type"] != "local" || m["command"] != bin || strings.Join(toStrings(m["args"]), " ") != "mcp --source copilot" {
		t.Errorf("membraid entry wrong: %v", m)
	}
	hook := readJSON(t, e.path(".copilot", "hooks", "membraid.json"))
	start := hook["hooks"].(map[string]any)["sessionStart"].([]any)[0].(map[string]any)
	if hook["version"] != float64(1) || start["bash"] != digestCommand(bin, "copilot") {
		t.Errorf("hook wrong: %v", hook)
	}
	if got, _ := os.ReadFile(e.path(".agents", "skills", "membraid", "SKILL.md")); string(got) != skill(t) {
		t.Error("Copilot CLI reads skills from ~/.agents/skills")
	}

	before, _ := os.ReadFile(cfg)
	apply(t, e, "copilot")
	if after, _ := os.ReadFile(cfg); string(after) != string(before) {
		t.Error("a second install must change nothing")
	}
}

func TestCursor(t *testing.T) {
	e, _ := newEnv(t)
	write(t, e.path(".cursor", "mcp.json"), `{"mcpServers": {"memory": {"command": "/home/x/go/bin/membraid", "args": ["mcp"]}, "other": {"url": "https://example.test"}}}`)
	write(t, e.path(".cursor", "hooks.json"), `{"version": 1, "hooks": {"sessionStart": [{"command": "echo mine"}, {"command": "/old/membraid context"}, {"command": "/old/membraid context --format cursor"}]}}`)
	apply(t, e, "cursor")

	servers := readJSON(t, e.path(".cursor", "mcp.json"))["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Error("other servers must survive")
	}
	if _, ok := servers["memory"]; ok {
		t.Error("legacy entry must be migrated")
	}
	if m := servers["membraid"].(map[string]any); m["command"] != bin || strings.Join(toStrings(m["args"]), " ") != "mcp --source cursor" {
		t.Errorf("membraid entry wrong: %v", m)
	}
	var commands []string
	for _, h := range readJSON(t, e.path(".cursor", "hooks.json"))["hooks"].(map[string]any)["sessionStart"].([]any) {
		commands = append(commands, h.(map[string]any)["command"].(string))
	}
	if len(commands) != 2 || commands[0] != "echo mine" || commands[1] != digestCommand(bin, "cursor") {
		t.Errorf("want the user's hook kept and one current membraid hook, got %q", commands)
	}
	if got, _ := os.ReadFile(e.path(".agents", "skills", "membraid", "SKILL.md")); string(got) != skill(t) {
		t.Error("Cursor reads skills from ~/.agents/skills")
	}

	before, _ := os.ReadFile(e.path(".cursor", "hooks.json"))
	apply(t, e, "cursor")
	if after, _ := os.ReadFile(e.path(".cursor", "hooks.json")); string(after) != string(before) {
		t.Error("a second install must change nothing")
	}
}
