package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shockalotti/membraid/internal/config"
	"github.com/shockalotti/membraid/internal/embed"
	"github.com/shockalotti/membraid/internal/index"
	"github.com/shockalotti/membraid/internal/scope"
	"github.com/shockalotti/membraid/internal/vault"
)

// MCP over stdio is newline-delimited JSON-RPC 2.0. stdout belongs to the
// protocol: every diagnostic in this file goes to stderr, because anything else
// printed on stdout corrupts the stream.

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type mcpServer struct {
	v      *vault.Vault
	cfg    config.Config
	ix     *index.Index
	source string
	out    *json.Encoder
	// session identifies this server process in every write, so distillation
	// can tell a subject restated across sessions from one written twice in one.
	session string

	mu       sync.Mutex
	timer    *time.Timer
	pending  bool
	lastPull time.Time

	// emb is nil when embeddings are off. embedding is set while a background
	// pass runs, so writes arriving during one do not start another.
	emb       embed.Embedder
	embedding atomic.Bool
}

func runMCP(v *vault.Vault, cfg config.Config, source string) error {
	ix, closeIx, err := openIndex(v, cfg)
	if err != nil {
		return err
	}
	defer closeIx()

	s := &mcpServer{v: v, cfg: cfg, ix: ix, source: source, out: json.NewEncoder(os.Stdout), session: "s-" + index.NewID()[:12]}
	if e, err := newEmbedder(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "membraid: embeddings unavailable, searching by keywords:", err)
	} else if e != nil {
		s.emb = &lockedEmbedder{e: e}
		defer s.emb.Close()
		// A session searches many times: keep vector bits in memory.
		ix.EnableVectorCache()
		go s.embedBacklog("session start")
	}
	fmt.Fprintf(os.Stderr, "membraid: mcp ready (vault %s, source %s, auto_sync %v)\n", v.Root(), source, cfg.AutoSync)

	// Pull when a session starts: starting an agent is the moment you most
	// likely just changed machines. In the background, so the harness is not
	// kept waiting on the network before it can list tools.
	if cfg.AutoSync {
		s.lastPull = time.Now()
		go s.syncNow("session start")
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.fail(nil, -32700, "parse error: "+err.Error())
			continue
		}
		s.dispatch(req)
	}
	// The session is ending. Writes still waiting out the push delay would
	// otherwise sit unpushed until the timer's next tick.
	s.flush()
	if err := in.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

// scheduleSync pushes once writes have been quiet for push_delay_sec, so a
// burst of agent writes becomes one commit rather than one per write.
func (s *mcpServer) scheduleSync() {
	if !s.cfg.AutoSync {
		return
	}
	delay := time.Duration(s.cfg.PushDelaySec) * time.Second
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = true
	if s.timer == nil {
		s.timer = time.AfterFunc(delay, func() {
			s.mu.Lock()
			s.pending = false
			s.mu.Unlock()
			s.syncNow("after writes")
		})
		return
	}
	s.timer.Reset(delay)
}

func (s *mcpServer) flush() {
	s.mu.Lock()
	wasPending := s.pending
	s.pending = false
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
	if wasPending {
		s.syncNow("session end")
	}
}

// refresh brings a long-running server up to date before it answers.
//
// A harness keeps one server for a whole session, and Hermes's gateway keeps
// one for days. Importing only at startup meant memories that arrived since -
// pulled by the sync timer, or written by another session on this machine -
// were invisible to search until this server happened to sync for itself.
//
// The import is local and incremental, so it runs before every call. If the
// last successful pull, by anyone on this machine, is older than
// pull_interval_min, a pull also starts in the background: this answer uses
// what is already on disk rather than waiting on the network, and a server
// stays current even where no timer runs. At most one pull is started per
// interval, so an offline machine is not hammered with fetches.
func (s *mcpServer) refresh() {
	if n, err := s.ix.ImportAll(); err != nil {
		fmt.Fprintf(os.Stderr, "membraid: import: %v\n", err)
	} else if n > 0 {
		go s.embedBacklog("imported")
	}
	if !s.cfg.AutoSync {
		return
	}
	interval := time.Duration(s.cfg.PullIntervalMin) * time.Minute
	if age, ok := config.LoadState().SinceLastSuccess(time.Now()); ok && age < interval {
		return
	}
	s.mu.Lock()
	due := time.Since(s.lastPull) >= interval
	if due {
		s.lastPull = time.Now()
	}
	s.mu.Unlock()
	if due {
		go s.syncNow("pull interval elapsed")
	}
}

// embedBacklog embeds memories that have no vector yet, in the background, so
// search by meaning covers new writes and imports. One pass at a time.
func (s *mcpServer) embedBacklog(reason string) {
	if s.emb == nil || !s.embedding.CompareAndSwap(false, true) {
		return
	}
	defer s.embedding.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	n, err := embedPending(ctx, s.emb, s.ix, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "membraid: embedding (%s) stopped after %d: %v\n", reason, n, err)
		return
	}
	if n > 0 {
		fmt.Fprintf(os.Stderr, "membraid: embedded %d memories (%s)\n", n, reason)
	}
}

func (s *mcpServer) syncNow(reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res, imported, err := syncVault(ctx, s.v, s.cfg, s.ix)
	if err != nil {
		fmt.Fprintf(os.Stderr, "membraid: sync (%s) failed: %v\n", reason, err)
		return
	}
	fmt.Fprintf(os.Stderr, "membraid: sync (%s): %s\n", reason, describeResult(res, imported))
}

func (s *mcpServer) dispatch(req rpcRequest) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-06-18"
		}
		s.reply(req.ID, map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "membraid", "version": "0.3.0"},
			"instructions":    serverInstructions,
		})
	case "notifications/initialized", "initialized":
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": toolDefs()})
	case "tools/call":
		s.callTool(req)
	case "ping", "shutdown":
		s.reply(req.ID, map[string]any{})
	default:
		if len(req.ID) > 0 {
			s.fail(req.ID, -32601, "unknown method: "+req.Method)
		}
	}
}

func (s *mcpServer) reply(id json.RawMessage, result any) {
	if len(id) == 0 {
		return
	}
	_ = s.out.Encode(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *mcpServer) fail(id json.RawMessage, code int, msg string) {
	if len(id) == 0 {
		fmt.Fprintln(os.Stderr, "membraid:", msg)
		return
	}
	_ = s.out.Encode(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *mcpServer) text(id json.RawMessage, body string, isError bool) {
	s.reply(id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": body}},
		"isError": isError,
	})
}

// serverInstructions is returned in the MCP initialize result, which harnesses
// place in the system prompt. It is the one place guidance reaches every
// harness at once, with nothing to install per harness - so it carries the
// habits, and the tool descriptions carry the mechanics.
//
// Kept short: it is in the prompt of every session.
const serverInstructions = `membraid is the user's shared memory across every agent they use and every machine they work on. What you record here, their other agents will see.

Read:
- Before asking the user something they may already have told an agent, or starting work on a project, call memory_search.
- To check one specific fact, memory_get with its key is cheapest.
- Search matches words, not meaning. If the results miss, search again with different words: a synonym, the tool or file name, or the key you expect.

Write (memory_write) when:
- the user states a preference or corrects how you work - kind preference, usually scope "shared"
- a decision is made, or you learn a concrete value for the project (deploy target, versions, conventions) - kind project_param
- you discover something non-obvious that would save a future session time - kind insight
- work is left unfinished at the end of a session - kind task_state, so the next session can pick it up

Keys: give one whenever the subject can change - a later write with the same key replaces the old answer instead of competing with it. Dotted lowercase, general to specific: editor.theme, deploy.target, task.auth.fix (hyphens and underscores become dots).

Tasks: every task_state should have a key. Call memory_done when the task is finished; an open task is shown to the user as where they left off.

Wrong: write the right answer under the same key, which replaces it. If nothing true replaces it (a removed tool, an abandoned plan, a mistake), memory_forget.

Do not record: routine chatter, anything already in the code or git history, or secrets. Memory is synced to a git remote - never store passwords, tokens, API keys or credentials.`

const keyGuidance = "Give a `key` whenever the fact has a subject that can change: a later " +
	"write with the same key replaces this one instead of competing with it. " +
	"Use a dotted lowercase noun path, most general part first - editor.theme, " +
	"pkg.manager, deploy.target. Omit it for one-off observations."

func toolDefs() []map[string]any {
	return []map[string]any{
		{
			"name": "memory_write",
			"description": "Record something worth remembering across sessions and across agents. " +
				"This memory is shared: what you write here is visible to every other agent the user runs, on every machine. " +
				keyGuidance,
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"content", "kind"},
				"properties": map[string]any{
					"content": map[string]any{"type": "string", "description": "The fact, stated plainly in one sentence."},
					"kind": map[string]any{
						"type": "string",
						"enum": []string{"preference", "project_param", "insight", "task_state"},
						"description": "preference: how the user wants things done. " +
							"project_param: a concrete value or choice for this project. " +
							"insight: something you observed or concluded. " +
							"task_state: what is in progress, shown to the user as where they left off. " +
							"If it will still be true at the end of the session it is not task_state. " +
							"Give task_state a key such as task.auth.fix, and call memory_done when it is finished.",
					},
					"key":   map[string]any{"type": "string", "description": keyGuidance},
					"scope": map[string]any{"type": "string", "description": "Omit for the current project. Use \"shared\" for something true everywhere, like a standing preference."},
				},
			},
		},
		{
			"name": "memory_done",
			"description": "Mark a task_state entry finished, so it stops showing as where the user left off. " +
				"Call this when a task you recorded is complete. Pass the key it was written with, or the id shown in memory_search results.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":   map[string]any{"type": "string", "description": "The task's key, e.g. task.auth.fix."},
					"id":    map[string]any{"type": "string", "description": "The task's id, for one written without a key."},
					"scope": map[string]any{"type": "string", "description": "Omit for the current project."},
				},
			},
		},
		{
			"name": "memory_forget",
			"description": "Retire a memory that is wrong or no longer true and has nothing true to replace it - a tool that was removed, a plan that was abandoned, something recorded by mistake. " +
				"If there is a correct answer, do not forget: write it with memory_write under the same key, which replaces the old one. " +
				"The memory leaves search and the session digest on every machine, but stays in history. Pass the id shown in memory_search results, or the key.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":    map[string]any{"type": "string", "description": "The memory's id from memory_search. Preferred: it names exactly one memory."},
					"key":   map[string]any{"type": "string", "description": "Retires every current memory with this key in the scope."},
					"scope": map[string]any{"type": "string", "description": "Omit for the current project."},
				},
			},
		},
		{
			"name": "memory_search",
			"description": "Search memory before assuming you do not know something. Returns current answers " +
				"from this project plus anything marked shared. Worth calling at the start of a task. " +
				"It matches words, not meaning: if the results miss, try other words, or memory_get the likely key.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Words you would expect in the memory."},
					"limit": map[string]any{"type": "integer", "description": "Max results, default 10."},
					"scope": map[string]any{"type": "string", "description": "Omit for the current project. \"*\" searches everything."},
				},
			},
		},
		{
			"name":        "memory_get",
			"description": "The current answer for one subject key, across all kinds. The cheapest way to check a specific fact, e.g. deploy.target.",
			"inputSchema": map[string]any{
				"type":       "object",
				"required":   []string{"key"},
				"properties": map[string]any{"key": map[string]any{"type": "string"}, "scope": map[string]any{"type": "string"}},
			},
		},
	}
}

func (s *mcpServer) callTool(req rpcRequest) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.fail(req.ID, -32602, "bad params: "+err.Error())
		return
	}
	var a struct {
		Content string `json:"content"`
		Kind    string `json:"kind"`
		Key     string `json:"key"`
		ID      string `json:"id"`
		Scope   string `json:"scope"`
		Query   string `json:"query"`
		Limit   int    `json:"limit"`
	}
	_ = json.Unmarshal(p.Arguments, &a)
	sc := scope.Resolve(a.Scope)
	s.refresh()

	switch p.Name {
	case "memory_write":
		res, err := s.ix.Write(index.Memory{Kind: a.Kind, Key: a.Key, Content: a.Content, Scope: sc, Source: s.source, SessionRef: s.session})
		if err != nil {
			s.text(req.ID, err.Error(), true)
			return
		}
		msg := fmt.Sprintf("Remembered in %s.", res.Scope)
		if n := len(res.Superseded); n > 0 {
			msg += fmt.Sprintf(" This replaced %d earlier answer on the same subject.", n)
		}
		if a.Kind == index.KindTaskState {
			msg += " Its id is " + res.ID + "; call memory_done when the task is finished."
		}
		s.text(req.ID, msg, false)
		s.scheduleSync()
		go s.embedBacklog("after write")

	case "memory_done":
		if a.Key == "" && a.ID == "" {
			s.text(req.ID, "memory_done needs the task's key or id.", true)
			return
		}
		closed, err := s.ix.Done(sc, a.Key, a.ID)
		if errors.Is(err, index.ErrNothingToClose) {
			s.text(req.ID, "No open task matches that. memory_search shows open tasks with their ids.", true)
			return
		}
		if err != nil {
			s.text(req.ID, err.Error(), true)
			return
		}
		s.text(req.ID, fmt.Sprintf("Marked %d task%s done.", len(closed), plural(len(closed))), false)
		s.scheduleSync()

	case "memory_forget":
		if a.Key == "" && a.ID == "" {
			s.text(req.ID, "memory_forget needs the memory's id or key.", true)
			return
		}
		gone, err := s.ix.Forget(sc, a.Key, a.ID)
		if errors.Is(err, index.ErrNothingToForget) {
			s.text(req.ID, "No current memory matches that. memory_search shows ids.", true)
			return
		}
		if err != nil {
			s.text(req.ID, err.Error(), true)
			return
		}
		noun := "memories"
		if len(gone) == 1 {
			noun = "memory"
		}
		s.text(req.ID, fmt.Sprintf("Forgot %d %s. It no longer appears in search or the digest, and stays in history.", len(gone), noun), false)
		s.scheduleSync()

	case "memory_search":
		hits, _, err := searchMemories(context.Background(), s.emb, s.ix, a.Query, sc, a.Limit)
		if err != nil {
			s.text(req.ID, err.Error(), true)
			return
		}
		if len(hits) == 0 {
			s.text(req.ID, "Nothing in memory about that.", false)
			return
		}
		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = h.ID
		}
		if err := s.ix.Touch(ids); err != nil {
			fmt.Fprintf(os.Stderr, "membraid: could not record retrieval: %v\n", err)
		}
		var b strings.Builder
		for _, h := range hits {
			b.WriteString("- [" + h.Kind)
			if h.Key != "" {
				b.WriteString(" " + h.Key)
			}
			b.WriteString("] " + h.Content + " (" + h.Scope + ", via " + h.Source + ", id " + h.ID)
			b.WriteString(")\n")
		}
		s.text(req.ID, strings.TrimRight(b.String(), "\n"), false)

	case "memory_get":
		var b strings.Builder
		var touched []string
		for _, k := range []string{index.KindPreference, index.KindProjectParam, index.KindInsight, index.KindTaskState} {
			m, err := s.ix.Current(sc, k, a.Key)
			if err != nil {
				s.text(req.ID, err.Error(), true)
				return
			}
			if m != nil {
				b.WriteString("- [" + m.Kind + "] " + m.Content + " (via " + m.Source + ", id " + m.ID)
				b.WriteString(")\n")
				touched = append(touched, m.ID)
			}
		}
		if err := s.ix.Touch(touched); err != nil {
			fmt.Fprintf(os.Stderr, "membraid: could not record retrieval: %v\n", err)
		}
		if b.Len() == 0 {
			s.text(req.ID, "No current answer for "+a.Key+".", false)
			return
		}
		s.text(req.ID, strings.TrimRight(b.String(), "\n"), false)

	default:
		s.text(req.ID, "unknown tool: "+p.Name, true)
	}
}
