package install

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Grok ignores what SessionStart hooks print, so its session digest cannot come
// from a hook. It does take --rules, which appends text to one session's system
// prompt. A grok() function in the shell's startup file passes the digest for
// the current project that way. It only reaches grok started from an
// interactive shell, which is how grok is normally run.

const (
	grokBlockBegin = "# >>> membraid: grok session digest >>>"
	grokBlockEnd   = "# <<< membraid: grok session digest <<<"
	// legacyGrokMarker starts the block written by hand before the installer
	// managed it. It has no end marker; its function ends at the first "}" line.
	legacyGrokMarker = "# membraid: start every grok session"
)

// shellRC returns the startup file of a shell whose function syntax the block
// uses (bash, zsh), or "" for any other shell (fish, PowerShell, unknown).
func (e *Env) shellRC() string {
	switch filepath.Base(e.Shell) {
	case "bash":
		return e.path(".bashrc")
	case "zsh":
		return e.path(".zshrc")
	}
	return ""
}

func grokDigestBlock(bin string) string {
	return grokBlockBegin + `
# Grok ignores what SessionStart hooks print, so the membraid digest for this
# project goes in through --rules, which appends it to the session's system
# prompt. Steps aside if you pass your own rules or system prompt, or if there
# is no digest. Managed by membraid install: edits between these markers are
# replaced on the next install.
grok() {
  case " $* " in
    *" --rules"*|*" --append-system-prompt"*|*" --system-prompt"*) command grok "$@"; return ;;
  esac
  local digest
  digest="$(` + shellQuote(bin) + ` context 2>/dev/null)"
  if [ -n "$digest" ]; then
    command grok --rules "$digest" "$@"
  else
    command grok "$@"
  fi
}
` + grokBlockEnd + "\n"
}

// shellQuote single-quotes a path for sh-family shells.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// upsertGrokDigest leaves exactly one current block in the shell startup file:
// a marked block is replaced in place, a hand-written one is migrated, and a
// file that already holds the current block is not touched. The original is
// backed up to <file>.membraid.bak before any change.
func upsertGrokDigest(e *Env) error {
	rc := e.shellRC()
	if rc == "" {
		return nil
	}
	raw, err := os.ReadFile(rc)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	existed := err == nil
	current := string(raw)
	rest := removeGrokBlocks(current)
	if rest != "" && !strings.HasSuffix(rest, "\n") {
		rest += "\n"
	}
	if rest != "" {
		rest += "\n"
	}
	next := rest + grokDigestBlock(e.Bin)
	if next == current {
		return nil
	}
	mode := os.FileMode(0o644)
	if existed {
		if fi, err := os.Stat(rc); err == nil {
			mode = fi.Mode().Perm()
		}
		if err := os.WriteFile(rc+".membraid.bak", raw, 0o600); err != nil {
			return err
		}
	}
	return os.WriteFile(rc, []byte(next), mode)
}

// removeGrokBlocks strips membraid's grok blocks, marked or hand-written, and
// the blank lines left where they were at the end of the file.
func removeGrokBlocks(s string) string {
	for {
		i := strings.Index(s, grokBlockBegin)
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], grokBlockEnd)
		if j < 0 {
			break
		}
		end := i + j + len(grokBlockEnd)
		if end < len(s) && s[end] == '\n' {
			end++
		}
		s = s[:i] + s[end:]
	}
	if i := strings.Index(s, legacyGrokMarker); i >= 0 {
		start := strings.LastIndex(s[:i], "\n") + 1
		if j := strings.Index(s[i:], "\n}\n"); j >= 0 {
			s = s[:start] + s[i+j+3:]
		} else if strings.HasSuffix(s, "\n}") {
			s = s[:start]
		}
	}
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return strings.TrimRight(s, "\n") + "\n"
}
