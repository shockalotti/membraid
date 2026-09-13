package install

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shockalotti/membraid/assets"
)

const bin = "/opt/bin/membraid"

type recorder struct{ calls []string }

func newEnv(t *testing.T) (*Env, *recorder) {
	t.Helper()
	r := &recorder{}
	e := &Env{
		Home: t.TempDir(), Bin: bin, Out: io.Discard,
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
	e.Run = func(stdin, name string, args ...string) (string, error) {
		r.calls = append(r.calls, strings.TrimSpace(stdin)+"|"+name+" "+strings.Join(args, " "))
		// Hermes's add writes the entry; the installer reads it back.
		if name == "hermes" && len(args) > 1 && args[0] == "mcp" && args[1] == "add" {
			p := e.path(".hermes", "config.yaml")
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte("mcp_servers:\n  membraid:\n    command: "+bin+"\n    args: [mcp, --source, hermes]\n"), 0o600)
		}
		return "", nil
	}
	return e, r
}

func write(t *testing.T, path, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func target(id string) Target {
	for _, t := range Targets() {
		if t.ID == id {
			return t
		}
	}
	panic("no target " + id)
}

func apply(t *testing.T, e *Env, id string) {
	t.Helper()
	if err := Apply(e, target(id)); err != nil {
		t.Fatalf("%s: %v", id, err)
	}
}

func skill(t *testing.T) string {
	b, err := fs.ReadFile(assets.FS, "skill/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Claude Code: the old "memory" entry is migrated, another server survives, the
// file's numbers and HTML characters come back exactly as they were, the stale
// hook is repointed rather than duplicated, and a second run changes nothing.
func TestClaudeCodeMigratesPreservesAndIsIdempotent(t *testing.T) {
	e, _ := newEnv(t)
	claudeJSON := e.path(".claude.json")
	settings := e.path(".claude", "settings.json")
	write(t, claudeJSON, `{"firstStartTime": 1757000000000, "note": "<b>&amp;</b>",
	  "mcpServers": {"memory": {"command": "/home/x/go/bin/membraid", "args": ["mcp","--source","claude-code"]},
	                 "other": {"command": "npx", "args": ["other-server"]}}}`)
	write(t, settings, `{"tui": "fullscreen", "hooks": {"SessionStart": [
	  {"hooks": [{"type": "command", "command": "/old/bin/membraid context --format claude 2>/dev/null || true", "timeout": 10}]},
	  {"hooks": [{"type": "command", "command": "echo unrelated"}]}]}}`)

	apply(t, e, "claude-code")

	raw, _ := os.ReadFile(claudeJSON)
	if !strings.Contains(string(raw), "1757000000000") || !strings.Contains(string(raw), "<b>&amp;</b>") {
		t.Errorf("numbers or HTML characters were rewritten:\n%s", raw)
	}
	servers := readJSON(t, claudeJSON)["mcpServers"].(map[string]any)
	if _, ok := servers["memory"]; ok {
		t.Error("legacy memory entry must be migrated")
	}
	if _, ok := servers["other"]; !ok {
		t.Error("an unrelated server must survive")
	}
	if m := servers["membraid"].(map[string]any); m["command"] != bin || !strings.Contains(strings.Join(toStrings(m["args"]), " "), "--source claude-code") {
		t.Errorf("membraid entry wrong: %v", m)
	}

	doc := readJSON(t, settings)
	if doc["tui"] != "fullscreen" {
		t.Error("other settings must survive")
	}
	var membraidHooks, unrelated int
	for _, g := range doc["hooks"].(map[string]any)["SessionStart"].([]any) {
		for _, h := range g.(map[string]any)["hooks"].([]any) {
			cmd := h.(map[string]any)["command"].(string)
			if strings.Contains(cmd, "membraid") {
				membraidHooks++
				if !strings.HasPrefix(cmd, bin+" context") {
					t.Errorf("hook not repointed: %s", cmd)
				}
			} else {
				unrelated++
			}
		}
	}
	if membraidHooks != 1 || unrelated != 1 {
		t.Errorf("want one membraid hook and the unrelated one kept, got %d and %d", membraidHooks, unrelated)
	}
	if got, _ := os.ReadFile(e.path(".claude", "skills", "membraid", "SKILL.md")); string(got) != skill(t) {
		t.Error("skill not installed")
	}
	if _, err := os.Stat(claudeJSON + ".membraid.bak"); err != nil {
		t.Error("original must be backed up")
	}

	before1, _ := os.ReadFile(claudeJSON)
	before2, _ := os.ReadFile(settings)
	apply(t, e, "claude-code")
	after1, _ := os.ReadFile(claudeJSON)
	after2, _ := os.ReadFile(settings)
	if string(before1) != string(after1) || string(before2) != string(after2) {
		t.Error("a second install must change nothing")
	}
}

// Another tool's server that happens to be called memory is not membraid's.
func TestSomeoneElsesMemoryServerIsLeftAlone(t *testing.T) {
	e, _ := newEnv(t)
	write(t, e.path(".claude.json"), `{"mcpServers": {"memory": {"command": "npx", "args": ["@acme/memory-server"]}}}`)
	apply(t, e, "claude-code")
	servers := readJSON(t, e.path(".claude.json"))["mcpServers"].(map[string]any)
	if _, ok := servers["memory"]; !ok {
		t.Error("a non-membraid memory server must not be removed")
	}
}

func TestOpenCode(t *testing.T) {
	e, _ := newEnv(t)
	cfg := e.path(".config", "opencode", "opencode.json")
	write(t, cfg, `{"$schema": "https://opencode.ai/config.json", "autoupdate": false,
	  "mcp": {"memory": {"type": "local", "command": ["/home/x/go/bin/membraid","mcp","--source","opencode"], "enabled": true}}}`)
	apply(t, e, "opencode")

	doc := readJSON(t, cfg)
	if doc["autoupdate"] != false {
		t.Error("other settings must survive")
	}
	servers := doc["mcp"].(map[string]any)
	if _, ok := servers["memory"]; ok {
		t.Error("legacy entry must be migrated")
	}
	if cmd := toStrings(servers["membraid"].(map[string]any)["command"]); cmd[0] != bin || cmd[len(cmd)-1] != "opencode" {
		t.Errorf("membraid entry wrong: %v", cmd)
	}
	plugin, _ := os.ReadFile(e.path(".config", "opencode", "plugins", "membraid.js"))
	if !strings.Contains(string(plugin), `"`+bin+`"`) || strings.Contains(string(plugin), "go/bin/membraid") {
		t.Errorf("plugin must point at the installed binary:\n%s", plugin)
	}
	if _, err := os.Stat(e.path(".claude", "skills", "membraid", "SKILL.md")); err != nil {
		t.Error("OpenCode reads skills from ~/.claude/skills")
	}
}

// A JSONC config may hold comments that a JSON round-trip would destroy.
func TestOpenCodeJSONCIsNotTouched(t *testing.T) {
	e, _ := newEnv(t)
	write(t, e.path(".config", "opencode", "opencode.jsonc"), "{ // mine\n}")
	err := Apply(e, target("opencode"))
	if err == nil || !strings.Contains(err.Error(), "jsonc") {
		t.Fatalf("want a jsonc refusal, got %v", err)
	}
	if _, serr := os.Stat(e.path(".config", "opencode", "opencode.json")); serr == nil {
		t.Error("must not create opencode.json beside a jsonc")
	}
}

func TestGrokMigratesThroughItsCLI(t *testing.T) {
	e, r := newEnv(t)
	write(t, e.path(".grok", "config.toml"), "[mcp_servers.memory]\ncommand = \"/home/x/go/bin/membraid\"\nargs = [\"mcp\"]\n\n[other]\nx = 1\n")
	apply(t, e, "grok")
	want := []string{
		"|grok mcp remove memory --scope user",
		"|grok mcp add membraid " + bin + " --scope user -- mcp --source grok",
	}
	if strings.Join(r.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(r.calls, "\n"), strings.Join(want, "\n"))
	}
	if _, err := os.Stat(e.path(".claude", "skills", "membraid", "SKILL.md")); err != nil {
		t.Error("grok reads the shared ~/.claude/skills copy")
	}
	if _, err := os.Stat(e.path(".grok", "skills", "membraid")); err == nil {
		t.Error("a ~/.grok/skills copy collides with the ~/.claude/skills one")
	}
}

func TestHermesMigratesVerifiesAndSkipsWhenCurrent(t *testing.T) {
	e, r := newEnv(t)
	write(t, e.path(".hermes", "config.yaml"), "model: x\nmcp_servers:\n  memory:\n    command: /home/x/go/bin/membraid\n    args: [mcp, --source, hermes]\n")
	apply(t, e, "hermes")
	want := []string{
		"y|hermes mcp remove memory",
		"Y|hermes mcp add membraid --command " + bin + " --connect-timeout 20 --args mcp --source hermes",
		"|hermes plugins enable --no-allow-tool-override membraid",
	}
	if strings.Join(r.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(r.calls, "\n"), strings.Join(want, "\n"))
	}
	py, _ := os.ReadFile(e.path(".hermes", "plugins", "membraid", "__init__.py"))
	if !strings.Contains(string(py), `"`+bin+`"`) {
		t.Error("hermes plugin must point at the installed binary")
	}
	if _, err := os.Stat(e.path(".hermes", "skills", "membraid", "SKILL.md")); err != nil {
		t.Error("skill not installed for hermes")
	}

	r.calls = nil
	apply(t, e, "hermes")
	for _, c := range r.calls {
		if strings.Contains(c, "mcp add") || strings.Contains(c, "mcp remove") {
			t.Errorf("a current entry must not be re-added: %v", r.calls)
		}
	}
}

// Hermes saying it added the server is not the same as the server being there.
func TestHermesAddIsReadBack(t *testing.T) {
	e, _ := newEnv(t)
	e.Run = func(string, string, ...string) (string, error) { return "Cancelled.", nil }
	err := Apply(e, target("hermes"))
	if err == nil || !strings.Contains(err.Error(), "not in") {
		t.Fatalf("want a read-back failure, got %v", err)
	}
}

func TestOmarchyWidget(t *testing.T) {
	e, r := newEnv(t)
	apply(t, e, "omarchy-widget")
	qml, _ := os.ReadFile(e.path(".config", "omarchy", "plugins", "shockalotti.membraid", "Panel.qml"))
	if !strings.Contains(string(qml), `"`+bin+`"`) {
		t.Error("widget must point at the installed binary")
	}
	want := "|omarchy plugin enable shockalotti.membraid\n|omarchy bar put shockalotti.membraid --before omarchy.power"
	if got := strings.Join(r.calls, "\n"); got != want {
		t.Errorf("want enable then put, never move (it would undo the user's placement):\n%s", got)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	e, r := newEnv(t)
	e.DryRun = true
	for _, tg := range Targets() {
		if err := Apply(e, tg); err != nil {
			t.Fatalf("%s: %v", tg.ID, err)
		}
	}
	entries, _ := os.ReadDir(e.Home)
	if len(entries) != 0 || len(r.calls) != 0 {
		t.Errorf("dry run wrote %d entries and ran %v", len(entries), r.calls)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// Embeddings are optional: never preselected, and they use Ollama only when a
// server is actually running.
func TestEmbeddingsTarget(t *testing.T) {
	e, r := newEnv(t)
	tg := target("embeddings")
	if tg.Detect(e) {
		t.Error("embeddings must never be preselected")
	}

	e.OllamaUp = func() bool { return true }
	apply(t, e, "embeddings")
	want := "|ollama pull embeddinggemma:300m-qat-q4_0\n|" + bin + " config set embeddings ollama\n|" + bin + " embed"
	if got := strings.Join(r.calls, "\n"); got != want {
		t.Errorf("with Ollama running:\n%s\nwant:\n%s", got, want)
	}

	r.calls = nil
	e.OllamaUp = func() bool { return false }
	apply(t, e, "embeddings")
	want = "|" + bin + " config set embeddings builtin\n|" + bin + " embed"
	if got := strings.Join(r.calls, "\n"); got != want {
		t.Errorf("without Ollama:\n%s\nwant:\n%s", got, want)
	}

	r.calls = nil
	e.DryRun = true
	e.OllamaUp = func() bool { return true }
	apply(t, e, "embeddings")
	if len(r.calls) != 0 {
		t.Errorf("dry run must run nothing, ran %v", r.calls)
	}
}

// Grok gets its session digest from a grok() function in the shell startup
// file. Installing adds it once, after the user's own lines, backed up.
func TestGrokDigestFunctionInBashrc(t *testing.T) {
	e, _ := newEnv(t)
	e.Shell = "/usr/bin/bash"
	rc := e.path(".bashrc")
	write(t, rc, "export FOO=1\nalias ll='ls -l'\n")

	apply(t, e, "grok")
	got, _ := os.ReadFile(rc)
	s := string(got)
	if !strings.HasPrefix(s, "export FOO=1\nalias ll='ls -l'\n") {
		t.Errorf("the user's lines must stay first and unchanged:\n%s", s)
	}
	if strings.Count(s, grokBlockBegin) != 1 || !strings.Contains(s, "'"+bin+"' context") || !strings.Contains(s, `command grok --rules "$digest" "$@"`) {
		t.Errorf("want one digest block calling %s:\n%s", bin, s)
	}
	if b, err := os.ReadFile(rc + ".membraid.bak"); err != nil || string(b) != "export FOO=1\nalias ll='ls -l'\n" {
		t.Error("the original .bashrc must be backed up")
	}

	apply(t, e, "grok")
	again, _ := os.ReadFile(rc)
	if string(again) != s {
		t.Errorf("a second install must change nothing:\n%s", again)
	}
}

// The block written by hand before the installer managed it is replaced, not
// duplicated, and lines after it survive.
func TestGrokDigestMigratesHandWrittenBlock(t *testing.T) {
	e, _ := newEnv(t)
	e.Shell = "/bin/bash"
	rc := e.path(".bashrc")
	handWritten := `export PATH="$PATH:$HOME/go/bin"

# membraid: start every grok session with the membraid digest for this project.
# Grok ignores what SessionStart hooks print, so the digest goes in through
# --rules, which appends it to that session's system prompt. Steps aside if you
# pass your own rules or system prompt, or if there is no digest.
grok() {
  case " $* " in
    *" --rules"*|*" --append-system-prompt"*|*" --system-prompt"*) command grok "$@"; return ;;
  esac
  local digest
  digest="$("${MEMBRAID_BIN:-$HOME/go/bin/membraid}" context 2>/dev/null)"
  if [ -n "$digest" ]; then
    command grok --rules "$digest" "$@"
  else
    command grok "$@"
  fi
}
alias after=true
`
	write(t, rc, handWritten)
	apply(t, e, "grok")
	got, _ := os.ReadFile(rc)
	s := string(got)
	if strings.Contains(s, legacyGrokMarker) || strings.Contains(s, "MEMBRAID_BIN") {
		t.Errorf("the hand-written block must be removed:\n%s", s)
	}
	if strings.Count(s, "grok() {") != 1 || strings.Count(s, grokBlockBegin) != 1 {
		t.Errorf("want exactly one grok function, the managed one:\n%s", s)
	}
	if !strings.Contains(s, `export PATH="$PATH:$HOME/go/bin"`) || !strings.Contains(s, "alias after=true") {
		t.Errorf("lines before and after the old block must survive:\n%s", s)
	}
}

// Moving the binary rewrites the block rather than adding a second one.
func TestGrokDigestFollowsTheBinary(t *testing.T) {
	e, _ := newEnv(t)
	e.Shell = "/usr/bin/zsh"
	apply(t, e, "grok")
	e.Bin = "/new/place/membraid"
	apply(t, e, "grok")
	got, err := os.ReadFile(e.path(".zshrc"))
	if err != nil {
		t.Fatal("zsh users get the function in ~/.zshrc")
	}
	s := string(got)
	if strings.Count(s, grokBlockBegin) != 1 || !strings.Contains(s, "'/new/place/membraid' context") || strings.Contains(s, bin) {
		t.Errorf("want one block pointing at the new binary:\n%s", s)
	}
}

// Shells without sh-style functions get nothing written, and a dry run writes
// nothing either.
func TestGrokDigestSkipsOtherShellsAndDryRuns(t *testing.T) {
	e, _ := newEnv(t)
	e.Shell = "/usr/bin/fish"
	apply(t, e, "grok")
	for _, f := range []string{".bashrc", ".zshrc"} {
		if _, err := os.Stat(e.path(f)); err == nil {
			t.Errorf("fish users must not get a %s", f)
		}
	}

	d, _ := newEnv(t)
	d.Shell = "/usr/bin/bash"
	d.DryRun = true
	apply(t, d, "grok")
	if _, err := os.Stat(d.path(".bashrc")); err == nil {
		t.Error("a dry run must not write .bashrc")
	}
}
