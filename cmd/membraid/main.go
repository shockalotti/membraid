// Command membraid is the CLI. v1 is a single process per invocation: no
// daemon, no shim, no socket (see docs/V1-SCOPE.md).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/shockalotti/membraid/internal/config"
	"github.com/shockalotti/membraid/internal/distill"
	"github.com/shockalotti/membraid/internal/embed"
	"github.com/shockalotti/membraid/internal/index"
	"github.com/shockalotti/membraid/internal/scope"
	"github.com/shockalotti/membraid/internal/vault"
	"github.com/shockalotti/membraid/internal/vaultsync"
	"github.com/shockalotti/membraid/internal/wirelog"
)

const usage = `membraid - one memory, shared by every agent you use

Usage:
  membraid init                          create a vault
  membraid write CONTENT [flags]         record a fact
  membraid search QUERY [flags]          search current memory
  membraid get KEY                       the live answer for one subject
  membraid history KEY                   what we used to think
  membraid done KEY | --id ID            mark a task finished
  membraid forget KEY | --id ID          retire a memory with nothing true to replace it
  membraid status [--json]               where you left off + what agents learned
  membraid context [--format claude]     the short digest an agent starts a session with
  membraid context --explain             the digest, and why each memory was chosen
  membraid embed                         make every memory searchable by meaning (embeddings on)
  membraid sweep [--json]                weekly upkeep: count what has gone unused, flag stale tasks
  membraid distill [--json]              write readable notes for subjects agents keep coming back to
  membraid sync [--json]                 commit, pull, push, import - once
  membraid config [set KEY VALUE]        this machine's settings
  membraid timer install|remove|status   periodic sync via systemd (Linux)
  membraid scopes | rescope --from S     projects, and adopting a moved one
  membraid where                         which vault, scope and machine
  membraid ls | cat PATH                 browse the vault
  membraid mcp --source NAME             run as an MCP server (stdio)
  membraid install [--dry-run]           set membraid up in your agent harnesses
  membraid version [--json]              which release this is
  membraid projects [--json] | prune     every project, and forgetting empty ones
  membraid memories [--json] [filters]   browse current memories (--scope, --kind, --source, -n)
  membraid insights [--json] [--days N]  how memory is being used
  membraid update [--check]              replace this binary with the latest release

Write flags:
  --kind   preference | project_param | insight | task_state   (default insight)
  --key    subject slug, e.g. deploy.target - a later write on the same key
           replaces this one instead of competing with it
  --scope  project, or "shared" to surface everywhere (default: this git project)
  --source which agent is writing (default: $MEMBRAID_SOURCE or "cli")

Sync settings (membraid config set ...):
  auto_sync          true   push shortly after writes, pull when a session starts
  push_delay_sec     60     wait for writes to go quiet before pushing
  pull_interval_min  15     how stale a scheduled pull may get
  halflife_days      30     days unused before a memory's search rank halves
  embeddings         off    search by meaning: off, ollama or builtin (local only)
  embed_model        (default)  Ollama model; default embeddinggemma:300m-qat-q4_0
  host               (hostname)  names this machine's log file

The vault is plain text in a git repo you own: every memory is a line in .hot/writes-*.jsonl.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(usage)
		return nil
	}
	cmd, rest := args[0], args[1:]

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	vaultPath := fs.String("vault", defaultVault(), "vault directory")
	kind := fs.String("kind", index.KindInsight, "preference|project_param|insight|task_state")
	key := fs.String("key", "", "subject key, e.g. editor.theme")
	scopeFlag := fs.String("scope", "", "project scope, or shared")
	source := fs.String("source", defaultSource(), "which agent is writing")
	limit := fs.Int("n", 10, "max results")
	jsonOut := fs.Bool("json", false, "machine-readable output")
	from := fs.String("from", "", "source scope for rescope")
	id := fs.String("id", "", "row id, for done")
	scheduled := fs.Bool("scheduled", false, "sync only if due (for the timer)")
	quiet := fs.Bool("quiet", false, "no output on success")
	format := fs.String("format", "text", "context output: text, claude (a SessionStart hook payload), copilot (a sessionStart hook payload with the server instructions), or cursor (a sessionStart hook payload)")
	explain := fs.Bool("explain", false, "context: show why each memory was chosen")
	digestFlag := fs.Bool("digest", false, "mcp: add the session digest to the server instructions")
	harness := fs.String("harness", "", "install: comma-separated harness ids, instead of asking")
	yes := fs.Bool("yes", false, "install: do not ask for confirmation")
	dryRun := fs.Bool("dry-run", false, "install: show the plan and change nothing")
	binFlag := fs.String("bin", "", "install: binary path harness configs should use")
	check := fs.Bool("check", false, "update: only report whether a newer release exists")
	releaseTag := fs.String("version", "", "update: install this release (e.g. v0.4.0) instead of the latest")
	replaces := fs.String("replaces", "", "write: the id of a memory this one corrects, which is retired")
	noTrack := fs.Bool("no-track", false, "search: a person browsing, so results are not counted as used")
	days := fs.Int("days", 7, "insights: how many days to look back")
	list := fs.Bool("list", false, "install: list harnesses and whether membraid is set up in each, as JSON")
	if err := fs.Parse(permute(fs, rest)); err != nil {
		return err
	}
	// Some flags have defaults for writing (kind insight, source cli) but filter
	// when listing, where only a value the user actually gave should count.
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	cfg, cerr := config.Load()
	if cerr != nil {
		fmt.Fprintln(os.Stderr, "membraid: using default settings:", cerr)
	}
	v := vault.Open(*vaultPath)

	switch cmd {
	case "version":
		return runVersion(*jsonOut)

	case "update":
		return runUpdate(*check, *releaseTag, *jsonOut)

	case "init":
		if err := v.Init(); err != nil {
			return err
		}
		fmt.Printf("created %s\n", v.Root())
		fmt.Println("plain text in a folder you own - grep it, diff it, commit it")
		return nil

	case "ls":
		cs, err := v.List()
		if err != nil {
			return err
		}
		if len(cs) == 0 {
			fmt.Println("no concepts yet")
		}
		for _, c := range cs {
			fmt.Printf("%-40s %-10s %-8s %-16s %s\n", c.Path, c.Type, c.Status, dash(c.Key), c.Title)
		}
		return nil

	case "cat":
		if fs.NArg() < 1 {
			return fmt.Errorf("cat needs a path, e.g. rules/never-force-push.md")
		}
		raw, err := os.ReadFile(filepath.Join(v.Root(), fs.Arg(0)))
		if err != nil {
			return err
		}
		os.Stdout.Write(raw)
		return nil

	case "write":
		if fs.NArg() < 1 {
			return fmt.Errorf(`write needs content, e.g. membraid write "deploy target is railway" --key deploy.target --kind project_param`)
		}
		return withIndex(v, cfg, func(ix *index.Index) error {
			res, err := ix.Write(index.Memory{
				Kind: *kind, Key: *key, Content: strings.Join(fs.Args(), " "),
				Scope: scope.Resolve(*scopeFlag), Source: *source,
			})
			if err != nil {
				return err
			}
			fmt.Printf("wrote %s", res.ID)
			if n := len(res.Superseded); n > 0 {
				fmt.Printf(" (replaced %d earlier answer%s)", n, plural(n))
			}
			fmt.Println()
			// A correction to a memory without a key: the new statement cannot
			// supersede the old one by key, so the old one is retired by id. One
			// already superseded above has nothing left to retire.
			if *replaces != "" && *replaces != res.ID && !contains(res.Superseded, *replaces) {
				if _, err := ix.Forget("", "", *replaces); err != nil {
					fmt.Fprintf(os.Stderr, "membraid: wrote the correction, but could not retire %s: %v\n", *replaces, err)
				}
			}
			return nil
		})

	case "done":
		return withIndex(v, cfg, func(ix *index.Index) error {
			k := ""
			if fs.NArg() > 0 {
				k = fs.Arg(0)
			}
			if k == "" && *id == "" {
				return fmt.Errorf("done needs a key or --id; 'membraid status' lists open tasks")
			}
			closed, err := ix.Done(scope.Resolve(*scopeFlag), k, *id)
			if errors.Is(err, index.ErrNothingToClose) {
				return fmt.Errorf("no open task matches; 'membraid status' lists them")
			}
			if err != nil {
				return err
			}
			fmt.Printf("marked %d task%s done\n", len(closed), plural(len(closed)))
			return nil
		})

	case "forget":
		return withIndex(v, cfg, func(ix *index.Index) error {
			k := ""
			if fs.NArg() > 0 {
				k = fs.Arg(0)
			}
			if k == "" && *id == "" {
				return fmt.Errorf("forget needs a key or --id; 'membraid search' shows ids")
			}
			gone, err := ix.Forget(scope.Resolve(*scopeFlag), k, *id)
			if errors.Is(err, index.ErrNothingToForget) {
				return fmt.Errorf("no current memory matches; 'membraid search' shows keys and ids")
			}
			if err != nil {
				return err
			}
			fmt.Printf("forgot %d memor%s (still in history)\n", len(gone), map[bool]string{true: "y", false: "ies"}[len(gone) == 1])
			return nil
		})

	case "search":
		if fs.NArg() < 1 {
			return fmt.Errorf("search needs a query")
		}
		return withIndex(v, cfg, func(ix *index.Index) error {
			sc := scope.Resolve(*scopeFlag)
			warnIfMoved(ix, sc)
			e, err := newEmbedder(cfg)
			if err != nil {
				fmt.Fprintln(os.Stderr, "membraid: embeddings:", err)
			}
			if e != nil {
				defer e.Close()
			}
			hits, _, err := searchMemories(context.Background(), e, ix, strings.Join(fs.Args(), " "), sc, *limit)
			if err != nil {
				return err
			}
			ids := make([]string, len(hits))
			for i, h := range hits {
				ids[i] = h.ID
			}
			if !*noTrack {
				_ = ix.Touch(ids)
			}
			if *jsonOut {
				if hits == nil {
					hits = []index.Hit{}
				}
				return json.NewEncoder(os.Stdout).Encode(hits)
			}
			if len(hits) == 0 {
				fmt.Println("nothing found")
			}
			for _, h := range hits {
				fmt.Printf("%-14s %-16s %-12s %s  [id %s]\n", h.Kind, dash(h.Key), h.Scope, h.Content, h.ID)
			}
			return nil
		})

	case "get", "history":
		if fs.NArg() < 1 {
			return fmt.Errorf("%s needs a key, e.g. membraid %s deploy.target", cmd, cmd)
		}
		return withIndex(v, cfg, func(ix *index.Index) error {
			sc := scope.Resolve(*scopeFlag)
			found := false
			var touched []string
			for _, k := range []string{index.KindPreference, index.KindProjectParam, index.KindInsight, index.KindTaskState} {
				if cmd == "get" {
					m, err := ix.Current(sc, k, fs.Arg(0))
					if err != nil {
						return err
					}
					if m != nil {
						fmt.Printf("%-14s %s  [id %s]\n", m.Kind, m.Content, m.ID)
						found = true
						touched = append(touched, m.ID)
					}
					continue
				}
				rows, err := ix.History(sc, k, fs.Arg(0))
				if err != nil {
					return err
				}
				for i, m := range rows {
					marker := "  "
					if i == 0 {
						marker = "->"
					}
					fmt.Printf("%s %-14s %s [%s]\n", marker, m.Kind, m.Content, m.Source)
					found = true
				}
			}
			_ = ix.Touch(touched)
			if !found {
				fmt.Println("no answer for that subject")
			}
			return nil
		})

	case "projects":
		return withIndex(v, cfg, func(ix *index.Index) error {
			if fs.NArg() > 0 && fs.Arg(0) == "prune" {
				n, err := ix.PruneScopes()
				if err != nil {
					return err
				}
				fmt.Printf("forgot %d project%s that never held a memory\n", n, plural(n))
				return nil
			}
			list, err := ix.Projects()
			if err != nil {
				return err
			}
			if *jsonOut {
				if list == nil {
					list = []index.ProjectInfo{}
				}
				return json.NewEncoder(os.Stdout).Encode(list)
			}
			for _, p := range list {
				note := ""
				if p.Missing {
					note = "  (folder missing)"
				}
				fmt.Printf("%-12s %-20s %4d memories %3d open  %-10s %s%s\n", p.Scope, p.Name, p.Memories, p.OpenTasks, day(p.LastWrite), p.Path, note)
			}
			return nil
		})

	case "insights":
		return withIndex(v, cfg, func(ix *index.Index) error {
			in, err := ix.Insights(*days)
			if err != nil {
				return err
			}
			names, _ := ix.ScopeNames()
			for i := range in.RecentlyUsed {
				in.RecentlyUsed[i].ScopeName = names[in.RecentlyUsed[i].Scope]
			}
			if *jsonOut {
				return json.NewEncoder(os.Stdout).Encode(in)
			}
			total := 0
			for _, d := range in.WritesByDay {
				total += d.Count
			}
			fmt.Printf("last %d days: %d writes\n", in.Days, total)
			for src, n := range in.WritesBySource {
				fmt.Printf("  %-14s %d\n", src, n)
			}
			fmt.Printf("%d current memories, %d never used by an agent\n", in.Current, in.NeverUsed)
			return nil
		})

	case "memories":
		return withIndex(v, cfg, func(ix *index.Index) error {
			sc := "*"
			if given["scope"] {
				sc = scope.Resolve(*scopeFlag)
			}
			k, src := "", ""
			if given["kind"] {
				k = *kind
			}
			if given["source"] {
				src = *source
			}
			n := 50
			if given["n"] {
				n = *limit
			}
			hits, err := ix.Browse(sc, k, src, n)
			if err != nil {
				return err
			}
			names, _ := ix.ScopeNames()
			for i := range hits {
				if name, ok := names[hits[i].Scope]; ok {
					hits[i].ScopeName = name
				} else {
					hits[i].ScopeName = hits[i].Scope
				}
			}
			if *jsonOut {
				return json.NewEncoder(os.Stdout).Encode(hits)
			}
			for _, h := range hits {
				fmt.Printf("%-14s %-16s %-14s %s  [id %s]\n", h.Kind, dash(h.Key), h.ScopeName, h.Content, h.ID)
			}
			return nil
		})

	case "scopes":
		return withIndex(v, cfg, func(ix *index.Index) error {
			list, err := ix.Scopes()
			if err != nil {
				return err
			}
			if *jsonOut {
				return json.NewEncoder(os.Stdout).Encode(list)
			}
			cur := scope.Resolve(*scopeFlag)
			for _, s := range list {
				mark, note := " ", ""
				if s.Scope == cur {
					mark = "*"
				}
				if s.Missing {
					note = "  (folder missing)"
				}
				fmt.Printf("%s %-12s %-20s %4d  %s%s\n", mark, s.Scope, s.Name, s.Count, s.Path, note)
			}
			return nil
		})

	case "rescope":
		if *from == "" {
			return fmt.Errorf("rescope needs --from <scope>; run 'membraid scopes' to see them")
		}
		return withIndex(v, cfg, func(ix *index.Index) error {
			to := scope.Resolve(*scopeFlag)
			n, err := ix.Rescope(*from, to)
			if err != nil {
				return err
			}
			fmt.Printf("moved %d memories from %s to %s\n", n, *from, to)
			return nil
		})

	case "status":
		return withIndex(v, cfg, func(ix *index.Index) error {
			sc := scope.Resolve(*scopeFlag)
			if sc != "*" {
				warnIfMoved(ix, sc)
			}
			st, err := ix.Stats()
			if err != nil {
				return err
			}
			doing, err := ix.Recent(sc, []string{index.KindTaskState}, 5)
			if err != nil {
				return err
			}
			stale, _ := ix.StaleTasks(sc)
			learned, err := ix.Recent(sc, []string{index.KindPreference, index.KindProjectParam, index.KindInsight}, 12)
			if err != nil {
				return err
			}
			// Label rows by project name. The bar asks for every project at once
			// ("--scope *"): it is not standing in any project, and falling back to
			// shared hid every project's tasks from "where you left off".
			names, _ := ix.ScopeNames()
			label := func(h []index.Hit) {
				for i := range h {
					if n, ok := names[h[i].Scope]; ok {
						h[i].ScopeName = n
					} else {
						h[i].ScopeName = h[i].Scope
					}
				}
			}
			label(doing)
			label(learned)
			ss := config.LoadState()
			syncInfo := map[string]any{
				"enabled": cfg.AutoSync, "last_success": ss.LastSuccess, "last_error": ss.LastError,
				"last_skipped": ss.LastSkipped, "interval_min": cfg.PullIntervalMin,
			}
			if *jsonOut {
				return json.NewEncoder(os.Stdout).Encode(map[string]any{
					"scope": sc, "vault": v.Root(), "host": wirelog.SafeHost(cfg.HostName()),
					"stats": st, "doing": doing, "learned": learned, "sync": syncInfo,
					"search":      searchStatus(cfg, ix),
					"stale_tasks": stale, "sweep": ix.LastSweep(), "version": currentVersion(),
				})
			}
			fmt.Printf("scope %s  -  %d current, %d total, %d projects\n", sc, st.Current, st.Total, st.Scopes)
			fmt.Println("sync  " + describeSync(cfg, ss))
			if r := ix.LastSweep(); r != nil {
				fmt.Printf("sweep  %s: %d unused for %d+ days, %d open tasks untouched for %d+ days\n",
					r.At[:10], r.StaleRows, index.SweepUnusedDays, r.StaleTasks, index.StaleTaskDays)
			}
			switch si := searchStatus(cfg, ix); si["mode"] {
			case "vector":
				fmt.Printf("search  by meaning (%s): %d of %d memories embedded\n", si["model"], si["embedded"], si["current"])
			default:
				fmt.Println("search  by keywords (for search by meaning: membraid install, or membraid config set embeddings)")
			}
			if len(doing) > 0 {
				fmt.Println("\nwhere you left off")
				for _, h := range doing {
					note := ""
					if days, ok := stale[h.ID]; ok {
						note = fmt.Sprintf(" (untouched %d days)", int(days))
					}
					fmt.Printf("  %s (%s, %s)%s  [done: membraid done --id %s]\n", h.Content, h.ScopeName, h.Source, note, h.ID)
				}
			}
			if len(learned) > 0 {
				fmt.Println("\nrecently learned")
				for _, h := range learned {
					fmt.Printf("  %-14s %-16s %s (%s, %s)\n", h.Kind, dash(h.Key), h.Content, h.ScopeName, h.Source)
				}
			}
			return nil
		})

	case "context":
		// A session-start hook must never break the session it runs in: any
		// failure produces an empty digest and exit 0, never an error the
		// harness would show or act on.
		text := ""
		var why []index.Scored
		_ = withIndex(v, cfg, func(ix *index.Index) error {
			var err error
			sc := scope.Resolve(*scopeFlag)
			text, err = buildContext(ix, sc, scope.Name(""))
			if *explain {
				why, _ = ix.Digest(sc, digestItems)
			}
			return err
		})
		if *format == "copilot" {
			// Copilot CLI leaves out the instructions of MCP servers it has not
			// allowlisted, so its hook carries them along with the digest.
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"additionalContext": strings.TrimSpace(serverInstructions(cfg.EmbeddingsOn()) + "\n\n" + text),
			})
		}
		if *format == "cursor" {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"additional_context": text})
		}
		if *format == "claude" {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"hookSpecificOutput": map[string]any{
					"hookEventName":     "SessionStart",
					"additionalContext": text,
				},
			})
		}
		fmt.Print(text)
		if *explain {
			fmt.Println("\n### Why these: score = kind x scope x (1 + ln writes) x decay")
			for _, s := range why {
				content := strings.Join(strings.Fields(s.Content), " ")
				if len(content) > 70 {
					content = content[:67] + "..."
				}
				fmt.Printf("%6.3f  %-13s kind %.1f  scope %.1f  writes %-2d unused %5.1fd  decay %.2f  %s\n",
					s.Score, s.Kind, s.KindWeight, s.ScopeWeight, s.Writes, s.UnusedDays, s.Decay, content)
			}
		}
		return nil

	case "sync":
		if *scheduled {
			due, why := syncDue(v, cfg)
			if !due {
				if !*quiet {
					fmt.Println("not due:", why)
				}
				return nil
			}
		}
		return withIndex(v, cfg, func(ix *index.Index) error {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			res, imported, err := syncVault(ctx, v, cfg, ix)
			if *jsonOut {
				out := map[string]any{"result": res, "imported": imported}
				if err != nil {
					out["error"] = err.Error()
				}
				json.NewEncoder(os.Stdout).Encode(out)
				return err
			}
			if err != nil {
				return err
			}
			if !*quiet {
				fmt.Println(describeResult(res, imported))
			}
			embedAfterSync(cfg, ix, *quiet)
			if *scheduled && ix.RunDue("distill", distillEvery) {
				if r, err := runDistill(v, ix); err != nil {
					fmt.Fprintln(os.Stderr, "membraid: distill:", err)
				} else if len(r.Paths) > 0 && !*quiet {
					fmt.Println(distillSummary(r))
				}
			}
			if *scheduled && ix.SweepDue() {
				if r, err := runSweep(v, ix); err != nil {
					fmt.Fprintln(os.Stderr, "membraid: sweep:", err)
				} else if !*quiet {
					fmt.Println(sweepSummary(r))
				}
			}
			return nil
		})

	case "distill":
		return withIndex(v, cfg, func(ix *index.Index) error {
			r, err := runDistill(v, ix)
			if err != nil {
				return err
			}
			if *jsonOut {
				return json.NewEncoder(os.Stdout).Encode(r)
			}
			if !*quiet {
				fmt.Println(distillSummary(r))
				for _, p := range r.Paths {
					fmt.Println("  " + p)
				}
			}
			return nil
		})

	case "sweep":
		return withIndex(v, cfg, func(ix *index.Index) error {
			r, err := runSweep(v, ix)
			if err != nil {
				return err
			}
			if *jsonOut {
				return json.NewEncoder(os.Stdout).Encode(r)
			}
			if !*quiet {
				fmt.Println(sweepSummary(r))
			}
			return nil
		})

	case "embed":
		e, err := newEmbedder(cfg)
		if err != nil {
			return err
		}
		if e == nil {
			return fmt.Errorf("embeddings are off; turn them on with: membraid config set embeddings ollama (or builtin)")
		}
		defer e.Close()
		return withIndex(v, cfg, func(ix *index.Index) error {
			dropped, err := ix.DropOtherVectors(e.Model())
			if err != nil {
				return err
			}
			if dropped > 0 && !*quiet {
				fmt.Printf("removed %d vectors from a previous model\n", dropped)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
			defer cancel()
			n, err := embedPending(ctx, e, ix, 0)
			if err != nil {
				return fmt.Errorf("embedded %d memories before stopping: %w", n, err)
			}
			have, total, err := ix.VectorCoverage(e.Model())
			if err != nil {
				return err
			}
			if !*quiet {
				fmt.Printf("embedded %d new; %d of %d current memories searchable by meaning (%s)\n", n, have, total, e.Model())
			}
			return nil
		})

	case "config":
		if fs.NArg() == 0 && *jsonOut {
			model := cfg.EmbedModel
			if model == "" {
				model = embed.DefaultOllamaModel
			}
			embeddings := cfg.Embeddings
			if embeddings == "" {
				embeddings = "off"
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"auto_sync": cfg.AutoSync, "push_delay_sec": cfg.PushDelaySec, "pull_interval_min": cfg.PullIntervalMin,
				"halflife_days": cfg.HalflifeDays, "embeddings": embeddings, "embed_model": model,
				"host": cfg.HostName(), "path": config.Path(),
			})
		}
		if fs.NArg() == 0 {
			buf, _ := json.MarshalIndent(cfg, "", "  ")
			fmt.Printf("%s\n\n# %s\n", buf, config.Path())
			return nil
		}
		if fs.Arg(0) != "set" || fs.NArg() != 3 {
			return fmt.Errorf("usage: membraid config set KEY VALUE")
		}
		if err := cfg.Set(fs.Arg(1), fs.Arg(2)); err != nil {
			return err
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("%s = %s\n", fs.Arg(1), fs.Arg(2))
		return nil

	case "timer":
		action := "status"
		if fs.NArg() > 0 {
			action = fs.Arg(0)
		}
		return timerCmd(action, v)

	case "install":
		return runInstall(v, *harness, *yes, *dryRun, *binFlag, *list)

	case "mcp":
		return runMCP(v, cfg, *source, *digestFlag)

	case "where":
		fmt.Printf("vault   %s\n", v.Root())
		fmt.Printf("scope   %s (%s)\n", scope.Resolve(*scopeFlag), scope.Name(""))
		fmt.Printf("host    %s\n", wirelog.SafeHost(cfg.HostName()))
		fmt.Printf("config  %s\n", config.Path())
		fmt.Println("\nOne vault holds every project. Scope is a column, not a folder,")
		fmt.Println("so there is one brain and one thing to sync.")
		return nil

	default:
		return fmt.Errorf("unknown command %q (try --help)", cmd)
	}
}

// buildContext is what an agent knows when a session starts: open tasks here,
// what has been decided and learned, and what is waiting in other projects.
//
// Small on purpose. The whole memory would flood the context window and bury
// the few things that matter; this is the digest, and memory_search is there
// for everything else.
// distillEvery is how often the scheduled sync writes concept notes (SPEC §15
// distill_every).
const distillEvery = 30 * time.Minute

// runDistill writes concept notes for qualifying subjects and, when any file
// was created or updated, says so in log.md. The next sync commits both.
func runDistill(v *vault.Vault, ix *index.Index) (*distill.Result, error) {
	names, _ := ix.ScopeNames()
	r, err := distill.Run(ix, v, names)
	if err != nil {
		return r, err
	}
	if err := ix.MarkRun("distill"); err != nil {
		return r, err
	}
	if len(r.Paths) > 0 {
		if err := v.AppendLog(distillSummary(r) + ": " + strings.Join(r.Paths, ", ")); err != nil {
			return r, err
		}
	}
	return r, nil
}

func distillSummary(r *distill.Result) string {
	s := fmt.Sprintf("distill: %d new note%s, %d updated", r.Created, plural(r.Created), r.Updated)
	if r.Kept > 0 {
		s += fmt.Sprintf(", %d edited by you and left alone", r.Kept)
	}
	return s
}

// runSweep runs a sweep and records its summary in the vault's log.md, where a
// person browsing the vault sees it (SPEC §9). The next sync commits it.
func runSweep(v *vault.Vault, ix *index.Index) (*index.SweepReport, error) {
	r, err := ix.Sweep()
	if err != nil {
		return nil, err
	}
	if err := v.AppendLog(sweepSummary(r)); err != nil {
		return r, err
	}
	return r, nil
}

func sweepSummary(r *index.SweepReport) string {
	return fmt.Sprintf("sweep: %d current memories; %d unused for %d+ days, left to fade; %d open tasks untouched for %d+ days",
		r.Current, r.StaleRows, index.SweepUnusedDays, r.StaleTasks, index.StaleTaskDays)
}

// digestItems is how many known facts a session starts with.
const digestItems = 12

func buildContext(ix *index.Index, sc, name string) (string, error) {
	const maxItem = 240
	clip := func(s string) string {
		s = strings.Join(strings.Fields(s), " ")
		if len(s) > maxItem {
			return s[:maxItem-3] + "..."
		}
		return s
	}
	names, _ := ix.ScopeNames()
	label := func(id string) string {
		if n, ok := names[id]; ok {
			return n
		}
		return id
	}

	doing, err := ix.Recent(sc, []string{index.KindTaskState}, 8)
	if err != nil {
		return "", err
	}
	stale, _ := ix.StaleTasks(sc)
	learned, err := ix.Digest(sc, digestItems)
	if err != nil {
		return "", err
	}
	everywhere, err := ix.Recent("*", []string{index.KindTaskState}, 50)
	if err != nil {
		return "", err
	}
	here := map[string]bool{}
	for _, t := range doing {
		here[t.ID] = true
	}
	elsewhere := map[string]int{}
	for _, t := range everywhere {
		if !here[t.ID] {
			elsewhere[label(t.Scope)]++
		}
	}

	var b strings.Builder
	project := name
	if sc == index.ScopeShared {
		project = "no project (shared memory only)"
	}
	fmt.Fprintf(&b, "## Membraid memory - %s\n\n", project)
	b.WriteString("Shared memory across the user's agents and machines. What follows is a digest; ")
	b.WriteString("use memory_search for anything else, memory_write to record, memory_done when a task finishes.\n")

	if len(doing) > 0 {
		b.WriteString("\n### Where the user left off\n")
		for _, t := range doing {
			fmt.Fprintf(&b, "- %s (%s, id %s)", clip(t.Content), t.Source, t.ID)
			if days, ok := stale[t.ID]; ok {
				fmt.Fprintf(&b, " - untouched %d days: if it is finished, memory_done; if not, rewrite it with where it stands", int(days))
			}
			b.WriteString("\n")
		}
	}
	if len(learned) > 0 {
		b.WriteString("\n### Known here\n")
		for _, h := range learned {
			key := ""
			if h.Key != "" {
				key = " " + h.Key
			}
			fmt.Fprintf(&b, "- [%s%s] %s\n", h.Kind, key, clip(h.Content))
		}
	}
	if len(elsewhere) > 0 {
		var parts []string
		for p, n := range elsewhere {
			parts = append(parts, fmt.Sprintf("%s (%d)", p, n))
		}
		sort.Strings(parts)
		fmt.Fprintf(&b, "\nOpen tasks in other projects: %s.\n", strings.Join(parts, ", "))
	}
	if len(doing) == 0 && len(learned) == 0 {
		b.WriteString("\nNothing is recorded for this project yet.\n")
	}
	return b.String(), nil
}

// withIndex opens the index, brings in anything another process or machine has
// appended to the log since this index last looked, and runs fn.
//
// Import on every open is what lets a pull made by the timer, or by the other
// harness's server, show up here without either one telling this process.
func withIndex(v *vault.Vault, cfg config.Config, fn func(*index.Index) error) error {
	ix, closeIx, err := openIndex(v, cfg)
	if err != nil {
		return err
	}
	defer closeIx()
	return fn(ix)
}

func openIndex(v *vault.Vault, cfg config.Config) (*index.Index, func(), error) {
	lg, err := wirelog.Open(v.HotPath(), cfg.HostName())
	if err != nil {
		return nil, nil, err
	}
	ix, err := index.Open(v.IndexPath(), lg)
	if err != nil {
		lg.Close()
		return nil, nil, err
	}
	ix.SetHalflife(cfg.HalflifeDays)
	if _, err := ix.ImportAll(); err != nil {
		fmt.Fprintln(os.Stderr, "membraid: could not import from the wire log:", err)
	}
	_ = ix.TouchScope(scope.Resolve(""), scope.Name(""), scope.Dir())
	return ix, func() { ix.Close(); lg.Close() }, nil
}

// syncVault runs one git sync, imports whatever it pulled, and records the
// outcome where status output and the bar widget read it.
func syncVault(ctx context.Context, v *vault.Vault, cfg config.Config, ix *index.Index) (*vaultsync.Result, int, error) {
	now := func() string { return time.Now().UTC().Format(time.RFC3339Nano) }
	st := config.LoadState()
	st.LastAttempt = now()

	// Record retrievals before committing, so they travel with this sync.
	if _, err := ix.Checkpoint(time.Hour); err != nil {
		fmt.Fprintln(os.Stderr, "membraid: could not write retrieval checkpoint:", err)
	}
	res, err := vaultsync.Run(ctx, v.Root(), vaultsync.Options{Host: wirelog.SafeHost(cfg.HostName())})
	imported := 0
	if err == nil && res.Skipped == "" {
		imported, err = ix.ImportAll()
	}
	switch {
	case err != nil:
		st.LastError = err.Error()
	case res.Skipped != "":
		st.LastSkipped = res.Skipped
	default:
		st.LastSuccess, st.LastError, st.LastSkipped = now(), "", ""
		st.Pushed, st.Pulled, st.Imported = res.Pushed, res.Pulled, imported
	}
	if serr := st.Save(); serr != nil {
		fmt.Fprintln(os.Stderr, "membraid: could not save sync state:", serr)
	}
	return res, imported, err
}

// syncDue decides whether a scheduled run has anything to do: yes if the vault
// has uncommitted changes (a CLI write is waiting to be pushed), or if the last
// successful sync is older than the pull interval.
func syncDue(v *vault.Vault, cfg config.Config) (bool, string) {
	if !cfg.AutoSync {
		return false, "auto_sync is off"
	}
	out, err := exec.Command("git", "-C", v.Root(), "status", "--porcelain").Output()
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		return true, "uncommitted changes"
	}
	age, ok := config.LoadState().SinceLastSuccess(time.Now())
	if !ok || age >= time.Duration(cfg.PullIntervalMin)*time.Minute {
		return true, "pull interval elapsed"
	}
	return false, fmt.Sprintf("last synced %s ago", age.Round(time.Second))
}

func describeResult(r *vaultsync.Result, imported int) string {
	if r.Skipped != "" {
		return "skipped: " + r.Skipped
	}
	var parts []string
	if r.Committed {
		parts = append(parts, "committed")
	}
	if r.Pulled {
		parts = append(parts, "pulled")
	}
	if r.Pushed {
		parts = append(parts, "pushed")
	}
	if imported > 0 {
		parts = append(parts, fmt.Sprintf("imported %d from other machines", imported))
	}
	if !r.Remote {
		parts = append(parts, "no remote configured")
	}
	if len(parts) == 0 {
		return "already in sync"
	}
	return strings.Join(parts, ", ")
}

func describeSync(cfg config.Config, st config.State) string {
	if !cfg.AutoSync {
		return "auto-sync off"
	}
	if st.LastError != "" {
		return "last sync failed: " + st.LastError
	}
	if age, ok := st.SinceLastSuccess(time.Now()); ok {
		return fmt.Sprintf("synced %s ago", age.Round(time.Second))
	}
	if st.LastSkipped != "" {
		return "not syncing: " + st.LastSkipped
	}
	return "not synced yet"
}

func timerCmd(action string, v *vault.Vault) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("the scheduled timer uses systemd, which is Linux-only; on %s run 'membraid sync --scheduled' from Task Scheduler or cron every 5 minutes", runtime.GOOS)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(base, "systemd", "user")
	service, timer := filepath.Join(dir, "membraid-sync.service"), filepath.Join(dir, "membraid-sync.timer")
	systemctl := func(args ...string) error {
		out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	switch action {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil {
			return err
		}
		env := "Environment=MEMBRAID_VAULT=" + v.Root() + "\n"
		if d := os.Getenv("MEMBRAID_CONFIG_DIR"); d != "" {
			env += "Environment=MEMBRAID_CONFIG_DIR=" + d + "\n"
		}
		// The timer ticks every 5 minutes; the command decides whether anything
		// is due. That keeps pull_interval_min a plain setting instead of
		// something that needs the unit file rewritten whenever it changes.
		unit := "[Unit]\nDescription=membraid: sync the memory vault with its git remote\n" +
			"After=network-online.target\n\n[Service]\nType=oneshot\n" +
			"ExecStart=" + exe + " sync --scheduled --quiet\n" + env
		tick := "[Unit]\nDescription=membraid: periodic vault sync\n\n[Timer]\n" +
			"OnBootSec=2min\nOnUnitActiveSec=5min\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(service, []byte(unit), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(timer, []byte(tick), 0o644); err != nil {
			return err
		}
		if err := systemctl("daemon-reload"); err != nil {
			return err
		}
		if err := systemctl("enable", "--now", "membraid-sync.timer"); err != nil {
			return err
		}
		fmt.Println("installed: membraid-sync.timer checks every 5 minutes and syncs when due")
		return nil
	case "remove":
		_ = systemctl("disable", "--now", "membraid-sync.timer")
		os.Remove(service)
		os.Remove(timer)
		_ = systemctl("daemon-reload")
		fmt.Println("removed membraid-sync.timer")
		return nil
	case "status":
		out, _ := exec.Command("systemctl", "--user", "list-timers", "membraid-sync.timer", "--no-pager").CombinedOutput()
		fmt.Print(string(out))
		return nil
	}
	return fmt.Errorf("timer takes install, remove or status")
}

// permute moves flags ahead of positional arguments, because Go's flag package
// stops at the first positional and 'write "content" --key foo' is the natural
// way to type the command.
func permute(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		if !strings.Contains(a, "=") && i+1 < len(args) && !isBoolFlag(fs, a) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

func isBoolFlag(fs *flag.FlagSet, arg string) bool {
	f := fs.Lookup(strings.TrimLeft(arg, "-"))
	if f == nil {
		return false
	}
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}

// warnIfMoved: standing in an empty scope while another scope holds memories
// whose folder is gone looks like a project that moved.
func warnIfMoved(ix *index.Index, current string) {
	known, err := ix.Scopes()
	if err != nil {
		return
	}
	var orphans []index.ScopeInfo
	for _, s := range known {
		if s.Scope == current {
			if s.Count > 0 {
				return
			}
			continue
		}
		if s.Missing && s.Count > 0 {
			orphans = append(orphans, s)
		}
	}
	if len(orphans) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\nmembraid: this looks like a project that moved.\n"+
		"  You are in %s (%s), which has no memories yet.\n", current, scope.Name(""))
	for _, o := range orphans {
		fmt.Fprintf(os.Stderr, "  %d memories are filed under %s (%s), whose folder is gone:\n      %s\n",
			o.Count, o.Scope, o.Name, o.Path)
	}
	fmt.Fprintf(os.Stderr, "  To bring one across:\n      membraid rescope --from %s\n\n", orphans[0].Scope)
}

func defaultSource() string {
	if s := os.Getenv("MEMBRAID_SOURCE"); s != "" {
		return s
	}
	return "cli"
}

func defaultVault() string {
	if p := os.Getenv("MEMBRAID_VAULT"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".membraid/vault"
	}
	return filepath.Join(home, ".membraid", "vault")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// day is the date part of a timestamp, or "-" when there is none.
func day(ts string) string {
	if len(ts) < 10 {
		return "-"
	}
	return ts[:10]
}
