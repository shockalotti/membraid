// Command memory-engine is the CLI. v1 is a single process: no daemon, no
// shim, no socket (see docs/V1-SCOPE.md).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wynne/memory-engine/internal/vault"
)

const usage = `memory-engine - memory for coding agents, stored in files you can read

Usage:
  memory-engine init [--vault DIR]      create a vault
  memory-engine ls   [--vault DIR]      list concepts
  memory-engine cat  PATH [--vault DIR] print one concept

The vault is plain markdown. You are not required to use this tool to read or
change it: grep it, open it in your editor, delete a file that is wrong.

Default vault: ~/.memory/vault (override with --vault or MEMORY_VAULT).
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
	if err := fs.Parse(rest); err != nil {
		return err
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

	default:
		return fmt.Errorf("unknown command %q (try --help)", cmd)
	}
}

func defaultVault() string {
	if p := os.Getenv("MEMORY_VAULT"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".memory/vault"
	}
	return filepath.Join(home, ".memory", "vault")
}

