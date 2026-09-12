// Command membraid is the CLI. v1 is a single process: no daemon, no
// shim, no socket (see docs/V1-SCOPE.md).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shockalotti/membraid/internal/index"
	"github.com/shockalotti/membraid/internal/scope"
	"github.com/shockalotti/membraid/internal/vault"
	"github.com/shockalotti/membraid/internal/wirelog"
)

const usage = `membraid - one memory, shared by every agent you use

Usage:
  membraid init                       create a vault
  membraid write CONTENT [flags]      record a fact
  membraid search QUERY [flags]       search current memory
  membraid get KEY [flags]            the live answer for one subject
  membraid history KEY [flags]        what we used to think
  membraid ls | cat PATH              browse the vault
  membraid where                      which vault and scope am I in?
  membraid mcp --source NAME          run as an MCP server (stdio)

Write flags:
  --kind   preference | project_param | insight | task_state   (default insight)
  --key    subject slug, e.g. editor.theme - a later write on the same key
           replaces this one instead of competing with it
  --scope  project slug, or "shared" to surface everywhere.
           Defaults to the current git project, so you rarely pass it.
           Searches always see your project plus shared.
  --source which agent is writing (default: $MEMBRAID_SOURCE or "cli")

Any agent that can run a shell command can use this. That is the point: not
every harness speaks MCP, and all of them can shell out.

The vault is plain markdown. You never need this tool to read or fix it - grep
it, open it in your editor, delete a file that is wrong.

Default vault: ~/.membraid/vault (MEMBRAID_VAULT), index alongside it.
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
	scopeFlag := fs.String("scope", "", "project scope, or shared (default: this git project)")
	source := fs.String("source", defaultSource(), "which agent is writing")
	limit := fs.Int("n", 10, "max results")
	if err := fs.Parse(permute(fs, rest)); err != nil {
		return err
	}
	v := vault.Open(*vaultPath)

	switch cmd {
	case "mcp":
		return runMCP(v, *source)

	case "where":
		fmt.Printf("vault   %s\n", v.Root())
		fmt.Printf("scope   %s\n", scope.Resolve(*scopeFlag))
		fmt.Println("\nOne vault holds every project. Scope is a column, not a folder,")
		fmt.Println("so there is one brain and one thing to sync.")
		return nil

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
			return nil
		}
		for _, c := range cs {
			key := c.Key
			if key == "" {
				key = "-"
			}
			fmt.Printf("%-40s %-10s %-8s %-16s %s\n", c.Path, c.Type, c.Status, key, c.Title)
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
			return fmt.Errorf("write needs content, e.g. membraid write \"deploy target is railway\" --key deploy.target --kind project_param")
		}
		ix, closeIx, err := openIndex(v)
		if err != nil {
			return err
		}
		defer closeIx()
		res, err := ix.Write(index.Memory{
			Kind: *kind, Key: *key, Content: strings.Join(fs.Args(), " "),
			Scope: scope.Resolve(*scopeFlag), Source: *source,
		})
		if err != nil {
			return err
		}
		fmt.Printf("wrote %s", res.ID)
		if n := len(res.Superseded); n > 0 {
			fmt.Printf(" (replaced %d earlier answer", n)
			if n > 1 {
				fmt.Print("s")
			}
			fmt.Print(")")
		}
		fmt.Println()
		return nil

	case "search":
		if fs.NArg() < 1 {
			return fmt.Errorf("search needs a query")
		}
		ix, closeIx, err := openIndex(v)
		if err != nil {
			return err
		}
		defer closeIx()
		hits, err := ix.Search(strings.Join(fs.Args(), " "), scope.Resolve(*scopeFlag), *limit)
		if err != nil {
			return err
		}
		if len(hits) == 0 {
			fmt.Println("nothing found")
			return nil
		}
		for _, h := range hits {
			k := h.Key
			if k == "" {
				k = "-"
			}
			fmt.Printf("%-14s %-16s %-12s %s\n", h.Kind, k, h.Scope, h.Content)
		}
		return nil

	case "get":
		if fs.NArg() < 1 {
			return fmt.Errorf("get needs a key, e.g. membraid get editor.theme")
		}
		ix, closeIx, err := openIndex(v)
		if err != nil {
			return err
		}
		defer closeIx()
		found := false
		for _, k := range []string{index.KindPreference, index.KindProjectParam, index.KindInsight, index.KindTaskState} {
			m, err := ix.Current(scope.Resolve(*scopeFlag), k, fs.Arg(0))
			if err != nil {
				return err
			}
			if m != nil {
				fmt.Printf("%-14s %s\n", m.Kind, m.Content)
				found = true
			}
		}
		if !found {
			fmt.Println("no current answer for that subject")
		}
		return nil

	case "history":
		if fs.NArg() < 1 {
			return fmt.Errorf("history needs a key")
		}
		ix, closeIx, err := openIndex(v)
		if err != nil {
			return err
		}
		defer closeIx()
		for _, k := range []string{index.KindPreference, index.KindProjectParam, index.KindInsight, index.KindTaskState} {
			rows, err := ix.History(scope.Resolve(*scopeFlag), k, fs.Arg(0))
			if err != nil {
				return err
			}
			for i, m := range rows {
				marker := "  "
				if i == 0 {
					marker = "->"
				}
				fmt.Printf("%s %-14s %s [%s]\n", marker, m.Kind, m.Content, m.Source)
			}
		}
		return nil

	default:
		return fmt.Errorf("unknown command %q (try --help)", cmd)
	}
}

// permute moves flags ahead of positional arguments.
//
// Go's flag package stops parsing at the first non-flag argument, so
//
//	membraid write "deploy target is railway" --key deploy.target
//
// silently ignores every flag and folds them into the content. That is the
// natural way to type the command, so the CLI has to accept it rather than
// teach people a rule about argument order.
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
		// A flag written as --name value consumes the next argument, unless it
		// is boolean or already written as --name=value.
		if !strings.Contains(a, "=") && i+1 < len(args) && !isBoolFlag(fs, a) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

func isBoolFlag(fs *flag.FlagSet, arg string) bool {
	name := strings.TrimLeft(arg, "-")
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}

// openIndex puts the index beside the vault, so one --vault moves everything
// and a git clone of the vault carries the wire log with it.
func openIndex(v *vault.Vault) (*index.Index, func(), error) {
	lg, err := wirelog.Open(v.HotPath())
	if err != nil {
		return nil, nil, err
	}
	ix, err := index.Open(filepath.Join(filepath.Dir(v.Root()), "index.db"), lg)
	if err != nil {
		lg.Close()
		return nil, nil, err
	}
	return ix, func() { ix.Close(); lg.Close() }, nil
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
