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
  membraid status [--json]               where you left off + what agents learned
  membraid context [--format claude]     the short digest an agent starts a session with
  membraid sync [--json]                 commit, pull, push, import - once
  membraid config [set KEY VALUE]        this machine's settings
  membraid timer install|remove|status   periodic sync via systemd (Linux)
  membraid scopes | rescope --from S     projects, and adopting a moved one
  membraid where                         which vault, scope and machine
  membraid ls | cat PATH                 browse the vault
  membraid mcp --source NAME             run as an MCP server (stdio)
  membraid install [--dry-run]           set membraid up in your agent harnesses

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
  host               (hostname)  names this machine's log file

The vault is plain markdown. You never need this tool to read or fix it.
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
	format := fs.String("format", "text", "context output: text, or claude (a SessionStart hook payload)")
	harness := fs.String("harness", "", "install: comma-separated harness ids, instead of asking")
	yes := fs.Bool("yes", false, "install: do not ask for confirmation")
	dryRun := fs.Bool("dry-run", false, "install: show the plan and change nothing")
	binFlag := fs.String("bin", "", "install: binary path harness configs should use")
	if err := fs.Parse(permute(fs, rest)); err != nil {
		return err
	}

	cfg, cerr := config.Load()
	if cerr != nil {
		fmt.Fprintln(os.Stderr, "membraid: using default settings:", cerr)
	}
	v := vault.Open(*vaultPath)

	switch cmd {
	case "init":
		if err := v.Init(); err != nil {
			return err
		}
		fmt.Printf("created %s\n", v.Root())
		fmt.Println("it is just markdown - open it, grep it, edit it")
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

	case "search":
		if fs.NArg() < 1 {
			return fmt.Errorf("search needs a query")
		}
		return withIndex(v, cfg, func(ix *index.Index) error {
			sc := scope.Resolve(*scopeFlag)
			warnIfMoved(ix, sc)
			hits, err := ix.Search(strings.Join(fs.Args(), " "), sc, *limit)
			if err != nil {
				return err
			}
			if *jsonOut {
				return json.NewEncoder(os.Stdout).Encode(hits)
			}
			if len(hits) == 0 {
				fmt.Println("nothing found")
			}
			for _, h := range hits {
				fmt.Printf("%-14s %-16s %-12s %s\n", h.Kind, dash(h.Key), h.Scope, h.Content)
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
			for _, k := range []string{index.KindPreference, index.KindProjectParam, index.KindInsight, index.KindTaskState} {
				if cmd == "get" {
					m, err := ix.Current(sc, k, fs.Arg(0))
					if err != nil {
						return err
					}
					if m != nil {
						fmt.Printf("%-14s %s\n", m.Kind, m.Content)
						found = true
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
			if !found {
				fmt.Println("no answer for that subject")
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
				})
			}
			fmt.Printf("scope %s  -  %d current, %d total, %d projects\n", sc, st.Current, st.Total, st.Scopes)
			fmt.Println("sync  " + describeSync(cfg, ss))
			if len(doing) > 0 {
				fmt.Println("\nwhere you left off")
				for _, h := range doing {
					fmt.Printf("  %s (%s, %s)  [done: membraid done --id %s]\n", h.Content, h.ScopeName, h.Source, h.ID)
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
		_ = withIndex(v, cfg, func(ix *index.Index) error {
			var err error
			text, err = buildContext(ix, scope.Resolve(*scopeFlag), scope.Name(""))
			return err
		})
		if *format == "claude" {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"hookSpecificOutput": map[string]any{
					"hookEventName":     "SessionStart",
					"additionalContext": text,
				},
			})
		}
		fmt.Print(text)
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
			return nil
		})

	case "config":
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
		return runInstall(v, *harness, *yes, *dryRun, *binFlag)

	case "mcp":
		return runMCP(v, cfg, *source)

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
	learned, err := ix.Recent(sc, []string{index.KindPreference, index.KindProjectParam, index.KindInsight}, 12)
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
			fmt.Fprintf(&b, "- %s (%s, id %s)\n", clip(t.Content), t.Source, t.ID)
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
