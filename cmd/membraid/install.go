package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/shockalotti/membraid/internal/install"
	"github.com/shockalotti/membraid/internal/vault"
)

type choice struct {
	id, name string
	detected bool
	notes    []string
	apply    func(dryRun bool) error
}

var stdin = bufio.NewReader(os.Stdin)

// runInstall sets membraid up in the harnesses the user picks: asks which
// (defaulting to the ones found on this machine), shows exactly what will
// change, and applies it. Safe to run again after upgrading or moving the
// binary: every step converges rather than duplicating.
func runInstall(v *vault.Vault, harnessFlag string, yes, dryRun bool, binFlag string) error {
	bin, err := installedBinary(binFlag)
	if err != nil {
		return err
	}
	env, err := install.DefaultEnv(bin, os.Stdout)
	if err != nil {
		return err
	}

	var choices []choice
	for _, t := range install.Targets() {
		t := t
		choices = append(choices, choice{
			id: t.ID, name: t.Name, detected: t.Detect(env), notes: t.Notes,
			apply: func(dry bool) error {
				e := *env
				e.DryRun = dry
				if !dry {
					e.Out = io.Discard
				}
				return install.Apply(&e, t)
			},
		})
	}
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("systemctl"); err == nil {
			choices = append(choices, choice{
				id: "sync-timer", name: "Sync timer (systemd, every 5 minutes)", detected: true,
				apply: func(dry bool) error {
					if dry {
						fmt.Println("  - systemd user timer membraid-sync.timer")
						return nil
					}
					return quietly(func() error { return timerCmd("install", v) })
				},
			})
		}
	}

	selected := make([]bool, len(choices))
	for i, c := range choices {
		selected[i] = c.detected
	}
	switch {
	case harnessFlag != "":
		if err := selectByID(choices, selected, harnessFlag); err != nil {
			return err
		}
	case yes:
		// every detected harness, as preselected
	case isTerminal():
		if err := pick(choices, selected); err != nil {
			return err
		}
	default:
		return fmt.Errorf("no terminal to ask which harnesses to set up: pass --harness %s, or --yes for every detected one", ids(choices))
	}

	var picked []choice
	for i, c := range choices {
		if selected[i] {
			picked = append(picked, c)
		}
	}
	if len(picked) == 0 {
		fmt.Println("nothing selected")
		return nil
	}

	fmt.Printf("\nHarness configs will point at %s\n\nPlan:\n", bin)
	for _, c := range picked {
		fmt.Printf("\n%s\n", c.name)
		_ = c.apply(true)
		for _, n := range c.notes {
			fmt.Printf("  note: %s\n", n)
		}
	}
	if dryRun {
		fmt.Println("\ndry run: nothing was changed")
		return nil
	}
	if !yes && isTerminal() {
		fmt.Print("\nProceed? [Y/n] ")
		answer, _ := stdin.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a == "n" || a == "no" {
			fmt.Println("cancelled")
			return nil
		}
	}

	if _, err := os.Stat(v.Root()); os.IsNotExist(err) {
		if err := v.Init(); err != nil {
			return err
		}
		fmt.Printf("\ncreated vault %s\n", v.Root())
	}

	// The plan was just shown step by step; applying reports one line per
	// harness so a failure stands out instead of hiding in a repeat of the plan.
	fmt.Println()
	var failed []string
	for _, c := range picked {
		if err := c.apply(false); err != nil {
			fmt.Printf("  failed  %s: %v\n", c.name, err)
			failed = append(failed, c.name)
			continue
		}
		fmt.Printf("  done    %s\n", c.name)
	}

	if out, err := exec.Command("git", "-C", v.Root(), "remote", "get-url", "origin").Output(); err != nil || strings.TrimSpace(string(out)) == "" {
		fmt.Printf("\nTo sync across machines, give the vault a private git remote:\n  cd %s && git init && git remote add origin <private repo url>\n", v.Root())
	}
	fmt.Println("\nRestart each harness you set up so it loads membraid.")
	if len(failed) > 0 {
		return fmt.Errorf("could not finish: %s (the rest were set up)", strings.Join(failed, ", "))
	}
	return nil
}

func pick(choices []choice, selected []bool) error {
	for {
		fmt.Println("\nWhich of these should membraid set up?")
		fmt.Println()
		for i, c := range choices {
			mark, tag := " ", "not found"
			if selected[i] {
				mark = "x"
			}
			if c.detected {
				tag = "detected"
			}
			fmt.Printf("  %d) [%s] %-38s %s\n", i+1, mark, c.name, tag)
		}
		fmt.Print("\nToggle with numbers (e.g. 2 4), a = all, n = none, Enter to continue: ")
		line, err := stdin.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return errors.New("no answer given")
		}
		if strings.TrimSpace(line) == "" {
			return nil
		}
		if err := toggle(line, selected); err != nil {
			fmt.Println("  " + err.Error())
		}
	}
}

// toggle applies a line like "2 4" or "a". It validates the whole line before
// changing anything, so a typo leaves the selection as it was.
func toggle(line string, selected []bool) error {
	fields := strings.Fields(strings.ReplaceAll(line, ",", " "))
	var flips []int
	for _, f := range fields {
		switch strings.ToLower(f) {
		case "a", "all", "n", "none":
			continue
		}
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > len(selected) {
			return fmt.Errorf("%q is not a number from 1 to %d", f, len(selected))
		}
		flips = append(flips, n-1)
	}
	for _, f := range fields {
		switch strings.ToLower(f) {
		case "a", "all":
			for i := range selected {
				selected[i] = true
			}
		case "n", "none":
			for i := range selected {
				selected[i] = false
			}
		}
	}
	for _, i := range flips {
		selected[i] = !selected[i]
	}
	return nil
}

func selectByID(choices []choice, selected []bool, list string) error {
	want := map[string]bool{}
	for _, id := range strings.Split(list, ",") {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	for i, c := range choices {
		selected[i] = want[c.id]
		delete(want, c.id)
	}
	if len(want) > 0 {
		var unknown []string
		for id := range want {
			unknown = append(unknown, id)
		}
		sort.Strings(unknown)
		return fmt.Errorf("unknown harness %s (choose from %s)", strings.Join(unknown, ", "), ids(choices))
	}
	return nil
}

func ids(choices []choice) string {
	var out []string
	for _, c := range choices {
		out = append(out, c.id)
	}
	return strings.Join(out, ",")
}

// isTerminal checks for a real terminal on both ends. A character-device test
// is not enough: /dev/null is one, and a harness or CI running install with
// stdin closed would sit at a prompt nobody can answer.
func isTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// installedBinary is the path every harness config will point at. Running
// install from `go run` would point them at a temporary build that is deleted
// when the command exits, so that is refused.
func installedBinary(override string) (string, error) {
	p := override
	if p == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		p = exe
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if override == "" && (strings.Contains(abs, "go-build") || strings.HasPrefix(abs, os.TempDir())) {
		return "", errors.New("install must run from an installed binary, since every harness config will point at it: go install ./cmd/membraid, then membraid install")
	}
	return abs, nil
}

// quietly runs fn with stdout discarded, for steps that report their own
// progress when run as a command but not as one line of install's summary.
func quietly(fn func() error) error {
	saved := os.Stdout
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return fn()
	}
	os.Stdout = devnull
	defer func() { os.Stdout = saved; devnull.Close() }()
	return fn()
}
