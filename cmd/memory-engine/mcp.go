package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/shockalotti/memory-engine/internal/index"
	"github.com/shockalotti/memory-engine/internal/scope"
	"github.com/shockalotti/memory-engine/internal/vault"
)

// MCP over stdio is newline-delimited JSON-RPC 2.0 on stdin and stdout.
//
// stdout belongs to the protocol. Anything printed there that is not a JSON-RPC
// message corrupts the stream and the harness drops the connection with no
// useful error, so every diagnostic in this file goes to stderr.

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
	ix     *index.Index
	source string
	out    *json.Encoder
}

func runMCP(v *vault.Vault, source string) error {
	ix, closeIx, err := openIndex(v)
	if err != nil {
		return err
	}
	defer closeIx()

	s := &mcpServer{ix: ix, source: source, out: json.NewEncoder(os.Stdout)}
	fmt.Fprintf(os.Stderr, "memory-engine: mcp ready (vault %s, source %s)\n", v.Root(), source)

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
	if err := in.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

func (s *mcpServer) dispatch(req rpcRequest) {
	switch req.Method {
	case "initialize":
		// Echo the client's protocol version: every harness in the stack is on
		// a slightly different one, and this server's surface is identical
		// across all of them.
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
			"serverInfo":      map[string]any{"name": "memory-engine", "version": "0.1.0"},
		})

	case "notifications/initialized", "initialized":
		// Notification: no id, no response.

	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": toolDefs()})

	case "tools/call":
		s.callTool(req)

	case "ping":
		s.reply(req.ID, map[string]any{})

	case "shutdown":
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
		fmt.Fprintln(os.Stderr, "memory-engine:", msg)
		return
	}
	_ = s.out.Encode(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

// text returns a tool result. isError tells the model the call failed without
// failing the RPC itself, which is what lets it retry sensibly.
func (s *mcpServer) text(id json.RawMessage, body string, isError bool) {
	s.reply(id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": body}},
		"isError": isError,
	})
}

const keyGuidance = "Give a `key` whenever the fact has a subject that can change: a later " +
	"write with the same key replaces this one instead of competing with it. " +
	"Use a dotted lowercase noun path, most general part first - editor.theme, " +
	"pkg.manager, deploy.target. Omit it for one-off observations."

func toolDefs() []map[string]any {
	return []map[string]any{
		{
			"name": "memory_write",
			"description": "Record something worth remembering across sessions and across agents. " +
				"This memory is shared: what you write here is visible to every other agent the user runs. " +
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
							"task_state: transient within-task state. " +
							"If it will still be true at the end of the session it is not task_state.",
					},
					"key":   map[string]any{"type": "string", "description": keyGuidance},
					"scope": map[string]any{"type": "string", "description": "Omit for the current project. Use \"shared\" for something true everywhere, like a standing preference."},
				},
			},
		},
		{
			"name": "memory_search",
			"description": "Search memory before assuming you do not know something. Returns current answers " +
				"from this project plus anything marked shared. Worth calling at the start of a task.",
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
		Scope   string `json:"scope"`
		Query   string `json:"query"`
		Limit   int    `json:"limit"`
	}
	_ = json.Unmarshal(p.Arguments, &a)
	sc := scope.Resolve(a.Scope)

	switch p.Name {
	case "memory_write":
		res, err := s.ix.Write(index.Memory{
			Kind: a.Kind, Key: a.Key, Content: a.Content, Scope: sc, Source: s.source,
		})
		if err != nil {
			s.text(req.ID, err.Error(), true)
			return
		}
		msg := fmt.Sprintf("Remembered in %s.", res.Scope)
		if n := len(res.Superseded); n > 0 {
			msg += fmt.Sprintf(" This replaced %d earlier answer on the same subject.", n)
		}
		s.text(req.ID, msg, false)

	case "memory_search":
		hits, err := s.ix.Search(a.Query, sc, a.Limit)
		if err != nil {
			s.text(req.ID, err.Error(), true)
			return
		}
		if len(hits) == 0 {
			s.text(req.ID, "Nothing in memory about that.", false)
			return
		}
		var b strings.Builder
		for _, h := range hits {
			b.WriteString("- [" + h.Kind)
			if h.Key != "" {
				b.WriteString(" " + h.Key)
			}
			b.WriteString("] " + h.Content + " (" + h.Scope + ", via " + h.Source + ")\n")
		}
		s.text(req.ID, strings.TrimRight(b.String(), "\n"), false)

	case "memory_get":
		var b strings.Builder
		for _, k := range []string{index.KindPreference, index.KindProjectParam, index.KindInsight, index.KindTaskState} {
			m, err := s.ix.Current(sc, k, a.Key)
			if err != nil {
				s.text(req.ID, err.Error(), true)
				return
			}
			if m != nil {
				b.WriteString("- [" + m.Kind + "] " + m.Content + " (via " + m.Source + ")\n")
			}
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
