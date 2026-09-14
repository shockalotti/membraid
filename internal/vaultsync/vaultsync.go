// Package vaultsync keeps a vault in step with its git remote.
//
// One run is: commit whatever changed, rebase onto the remote, push. It never
// merges, never leaves a repo mid-rebase, and never runs twice at once.
//
// It knows nothing about the index. Bringing another machine's writes into the
// local index is the caller's job, after a run that pulled.
package vaultsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shockalotti/membraid/internal/vault"
)

type Options struct {
	Host    string        // names the commit
	Timeout time.Duration // whole run, network included; 0 means 90s
}

type Result struct {
	Committed bool   `json:"committed"`
	Pulled    bool   `json:"pulled"`
	Pushed    bool   `json:"pushed"`
	Remote    bool   `json:"remote"`
	Skipped   string `json:"skipped,omitempty"`
}

// ConflictError is returned when both machines edited the same file. The run
// is rolled back to the local commit, which is intact; nothing is lost, it just
// needs a human.
type ConflictError struct{ Files []string }

func (e *ConflictError) Error() string {
	return "sync conflict in " + strings.Join(e.Files, ", ") +
		" - both machines changed it. Your local commit is intact; resolve with git in the vault."
}

// Run syncs once. A vault that is not a git repo, or a sync already running in
// another process, is a skip rather than an error: neither is anything to fix.
func Run(ctx context.Context, vault string, opt Options) (*Result, error) {
	res := &Result{}
	if _, err := os.Stat(filepath.Join(vault, ".git")); err != nil {
		res.Skipped = "vault is not a git repo"
		return res, nil
	}
	if opt.Timeout == 0 {
		opt.Timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()

	// The lock lives inside .git so it is never itself committed. Claude Code
	// and OpenCode each run their own server, and both syncing at once would
	// collide on git's own index.lock.
	unlock, ok, err := tryLock(filepath.Join(vault, ".git", "membraid-sync.lock"))
	if err != nil {
		return res, err
	}
	if !ok {
		res.Skipped = "another sync is running"
		return res, nil
	}
	defer unlock()

	g := &git{ctx: ctx, dir: vault}

	// Commit AND rebase both write commits, and both refuse without an identity.
	// A machine with no global git user is common - a fresh install, a CI box -
	// so fill in whatever half is missing for every command in this run, not
	// just the commit.
	if name, _ := g.run("config", "user.name"); strings.TrimSpace(name) == "" {
		g.env = append(g.env, "GIT_AUTHOR_NAME=membraid", "GIT_COMMITTER_NAME=membraid")
	}
	if email, _ := g.run("config", "user.email"); strings.TrimSpace(email) == "" {
		addr := "membraid@" + opt.Host
		g.env = append(g.env, "GIT_AUTHOR_EMAIL="+addr, "GIT_COMMITTER_EMAIL="+addr)
	}

	if _, err := g.run("add", "-A"); err != nil {
		return res, err
	}
	if _, err := g.run("diff", "--cached", "--quiet"); err != nil {
		if !isExit(err, 1) {
			return res, err
		}
		if _, err := g.run("commit", "-q", "-m", "membraid: sync from "+opt.Host); err != nil {
			return res, err
		}
		res.Committed = true
	}

	if _, err := g.run("remote", "get-url", "origin"); err != nil {
		return res, nil // no remote: committing locally is all there is to do
	}
	res.Remote = true

	branch, err := g.run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return res, err
	}
	branch = strings.TrimSpace(branch)
	if branch == "HEAD" {
		return res, errors.New("vault is on a detached HEAD; check out a branch before syncing")
	}

	if _, err := g.run("fetch", "-q", "origin"); err != nil {
		return res, fmt.Errorf("fetch failed: %w", err)
	}
	remoteRef := "refs/remotes/origin/" + branch
	_, verr := g.run("rev-parse", "--verify", "--quiet", remoteRef)
	remoteExists := verr == nil

	if remoteExists {
		behind, err := g.count("HEAD.." + remoteRef)
		if err != nil {
			return res, err
		}
		if behind > 0 {
			if err := g.rebase(remoteRef); err != nil {
				return res, err
			}
			res.Pulled = true
		}
	}

	ahead := 1
	if remoteExists {
		if ahead, err = g.count(remoteRef + "..HEAD"); err != nil {
			return res, err
		}
	}
	if ahead > 0 {
		if _, err := g.run("push", "-q", "-u", "origin", branch); err != nil {
			return res, fmt.Errorf("push failed: %w", err)
		}
		res.Pushed = true
	}
	return res, nil
}

type git struct {
	ctx context.Context
	dir string
	env []string
}

func (g *git) run(args ...string) (string, error) {
	cmd := exec.CommandContext(g.ctx, "git", append([]string{"-C", g.dir}, args...)...)
	// Never wait for a human: a sync running under a timer or behind an MCP
	// server has nobody to type a password or close an editor.
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true"), g.env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			return out.String(), err
		}
		return out.String(), &gitError{args: args, msg: msg, err: err}
	}
	return out.String(), nil
}

func (g *git) count(rangeSpec string) (int, error) {
	out, err := g.run("rev-list", "--count", rangeSpec)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// DraftMarker is the footer distillation puts on the notes it writes. A
// conflicted note that still carries it, and is still a draft, on both sides is
// membraid's to settle. It must match internal/distill's footer; a test there
// checks.
const DraftMarker = "_Written by membraid from what your agents recorded."

var draftStatus = regexp.MustCompile(`(?m)^status:[ \t]*draft[ \t]*$`)

// rebase replays this machine's commits onto the remote. Conflicts on the
// files membraid itself maintains on every machine are settled, because two
// machines must never block each other's sync over log.md or a distilled note.
// Any other conflict aborts the rebase and is reported; the local commit is
// intact and the repo is never left mid-rebase.
func (g *git) rebase(ref string) error {
	_, err := g.run("rebase", "-q", ref)
	for attempt := 0; err != nil && attempt < 500; attempt++ {
		out, _ := g.run("diff", "--name-only", "--diff-filter=U")
		files := nonEmptyLines(out)
		if len(files) == 0 {
			if !g.rebasing() {
				break
			}
			// Settling left this commit with nothing to add: its change was
			// already upstream.
			_, err = g.run("rebase", "--skip")
			continue
		}
		settled, serr := g.settle(files)
		if serr != nil || !settled {
			_, _ = g.run("rebase", "--abort")
			if serr != nil {
				return fmt.Errorf("settling a sync conflict: %w", serr)
			}
			return &ConflictError{Files: files}
		}
		_, err = g.run("rebase", "--continue")
	}
	if err != nil {
		_, _ = g.run("rebase", "--abort")
		return fmt.Errorf("rebase failed and was aborted: %w", err)
	}
	return nil
}

func (g *git) rebasing() bool {
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(g.dir, ".git", d)); err == nil {
			return true
		}
	}
	return false
}

// settle resolves the conflicted files it recognises. During a rebase stage 2
// is the remote's version and stage 3 this machine's.
//
//   - log.md keeps both machines' entries: every machine inserts new lines under
//     the header, so any two appends between syncs collide.
//   - A note that both sides still show as an untouched distilled draft takes
//     the remote's version; the next distill pass rewrites it from both
//     machines' memories, which sync has just brought together.
//
// Anything else - a note a person changed, a file deleted on one side, any
// other file - is not membraid's to decide, and settle reports false.
func (g *git) settle(files []string) (bool, error) {
	for _, f := range files {
		remote, rerr := g.run("show", ":2:"+f)
		local, lerr := g.run("show", ":3:"+f)
		if rerr != nil || lerr != nil {
			return false, nil
		}
		var merged string
		switch {
		case f == "log.md":
			merged = unionLog(remote, local)
		case strings.HasSuffix(f, ".md") && untouched(remote) && untouched(local):
			merged = remote
		default:
			return false, nil
		}
		if err := os.WriteFile(filepath.Join(g.dir, filepath.FromSlash(f)), []byte(merged), 0o600); err != nil {
			return false, err
		}
		if _, err := g.run("add", "--", f); err != nil {
			return false, err
		}
	}
	return true, nil
}

// untouched reports a note as membraid wrote it: its ownership line matches,
// whatever its status (a note marked no longer current is not a draft), or,
// for a note from before notes carried that line, it is still a draft with
// distillation's footer.
func untouched(note string) bool {
	return vault.Owned([]byte(note)) || untouchedDraft(note)
}

func untouchedDraft(note string) bool {
	if !strings.Contains(note, DraftMarker) || !strings.HasPrefix(note, "---\n") {
		return false
	}
	end := strings.Index(note[4:], "\n---")
	return end >= 0 && draftStatus.MatchString(note[4:4+end])
}

// unionLog keeps every entry from both sides of a log.md conflict. Entries are
// "- " lines under the header, newest first; this machine's entries that the
// remote lacks go on top.
func unionLog(remote, local string) string {
	header, remoteEntries := splitLog(remote)
	_, localEntries := splitLog(local)
	seen := map[string]bool{}
	for _, l := range remoteEntries {
		seen[l] = true
	}
	var b strings.Builder
	b.WriteString(header)
	for _, l := range localEntries {
		if !seen[l] {
			b.WriteString(l + "\n")
			seen[l] = true
		}
	}
	for _, l := range remoteEntries {
		b.WriteString(l + "\n")
	}
	return b.String()
}

func splitLog(s string) (header string, entries []string) {
	lines := strings.SplitAfter(s, "\n")
	i := 0
	for ; i < len(lines) && !strings.HasPrefix(lines[i], "- "); i++ {
		header += lines[i]
	}
	for _, l := range lines[i:] {
		if l = strings.TrimRight(l, "\n"); strings.TrimSpace(l) != "" {
			entries = append(entries, l)
		}
	}
	return header, entries
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

type gitError struct {
	args []string
	msg  string
	err  error
}

func (e *gitError) Error() string { return "git " + e.args[0] + ": " + lastLine(e.msg) }
func (e *gitError) Unwrap() error { return e.err }

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func isExit(err error, code int) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == code
}
