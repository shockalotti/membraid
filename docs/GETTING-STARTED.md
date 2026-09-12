# Getting started

## Install, once

```sh
go install github.com/shockalotti/membraid/cmd/membraid@latest
membraid init
```

`init` creates `~/.membraid/vault` and `~/.membraid/index.db`. **Run it once, ever.**
Not per project.

If `membraid: command not found`, `~/go/bin` is not on your PATH:

```sh
echo 'export PATH="$PATH:$HOME/go/bin"' >> ~/.bashrc && source ~/.bashrc
```

## One vault, every project

A project is a **column value**, not a directory. Scope resolves automatically
from the git root you are standing in, so you almost never type `--scope`:

```sh
cd ~/Projects/api && membraid where
  vault   /home/you/.membraid/vault
  scope   api-3f8a1c20
```

`api-3f8a1c20` is the basename plus a hash of the full path, so `~/work/api` and
`~/personal/api` stay separate brains rather than colliding.

Searches see **your project plus `shared`**. Write with `--scope shared` (or
`scope: "shared"` from an agent) for something true everywhere - a standing
preference, a rule you always want applied.

## One vault or several?

**One is the default and the recommendation.** The whole premise is coherence,
and a second vault is a second brain that does not know about the first.
Project separation already happens *inside* one vault through scope, which
covers most of why anyone reaches for a second.

Several is supported - `--vault` and `MEMBRAID_VAULT` point anywhere - and is
reasonable when the boundary is about *sharing* rather than organisation: a
work vault that syncs to a company repo, a personal one that does not.

The failure to watch for is accidental: `MEMBRAID_VAULT` set in one shell and
not another silently splits your memory in two. `membraid where` and the bar
widget both name the vault in use, which is how you notice.

## Using an existing Obsidian vault

Point membraid at a **new, empty subfolder** of your vault. It owns that
subtree and touches nothing else:

```sh
membraid init --vault "~/Documents/MyVault/Agent Memory"
export MEMBRAID_VAULT="$HOME/Documents/MyVault/Agent Memory"
```

Your notes, agent memory, and Obsidian's graph all live together - wikilinks
work across the boundary - while membraid only ever writes inside its own
folder. Pointing it at a vault root that already has files in it is refused,
with the subfolder command in the error.

Everything engine-owned lives in `.hot/`, which Obsidian hides: the wire log,
and the index. `.hot/.gitignore` keeps the rebuildable index out of git while
the log, which is history, stays in.

## Moving projects around

People move and rename directories constantly, so scope identity does not
depend on the path when it can avoid it.

**A git project is identified by its root commit.** Move it, rename it, clone
it again, check out a worktree - same project, same memories, nothing to do.

```sh
membraid where
  scope   g0c59d778 (memory-engine)     # g = git identity, name is just a label
```

**Anything outside git falls back to the path**, which cannot survive a move.
When that happens membraid notices and tells you, rather than quietly starting
an empty second brain:

```
membraid: this looks like a project that moved.
  You are in p24383357 (w-b), which has no memories yet.
  1 memories are filed under pb43b3cfb (w-a), whose folder is gone:
      /tmp/w-a
  To bring one across:
      membraid rescope --from pb43b3cfb
```

It never migrates on its own: two projects that merely share a name are not
the same project, and guessing there would merge unrelated memory.

```sh
membraid scopes     # every project the vault knows, and which folders are gone
```

## Sync across machines

The vault is a git repo. That is the whole sync protocol.

```sh
cd ~/.membraid/vault && git init && git add -A && git commit -m "memory"
git remote add origin <your-private-repo> && git push -u origin main
```

On the other machine: `git clone <repo> ~/.membraid/vault`, then
`membraid init` is **not** needed - but the index is rebuilt from the vault
and wire log, which live in the repo, so nothing is lost.

> Keep it private. Your memory is your notes.

## Wire it into your harnesses

Every harness in the stack speaks MCP over stdio. The `--source` flag is how
memory records which agent wrote what, so give each one its own name.

**Claude Code** - `~/.claude.json` or `.mcp.json` in a project:

```json
{
  "mcpServers": {
    "memory": {
      "command": "membraid",
      "args": ["mcp", "--source", "claude-code"]
    }
  }
}
```

**OpenCode** - `opencode.json`:

```json
{
  "mcp": {
    "memory": {
      "type": "local",
      "command": ["membraid", "mcp", "--source", "opencode"],
      "enabled": true
    }
  }
}
```

**Grok CLI** - `.grok/settings.json`:

```json
{
  "mcpServers": {
    "memory": {
      "command": "membraid",
      "args": ["mcp", "--source", "grok"]
    }
  }
}
```

**Hermes Agent** and **DSH (DeepSeek Harness)** both take stdio MCP servers in
their own config; the command is the same, with `--source hermes` and
`--source dsh`.

Use an absolute path to the binary if a harness does not inherit your PATH.

## The bar widget (Omarchy)

```sh
omarchy plugin add https://github.com/shockalotti/membraid   # once published
omarchy bar move shockalotti.membraid --section right --before omarchy.power
```

A brain icon in the top right. Click it for where you left off and what your
agents have been learning, across every harness. It reads `membraid status
--json` and writes nothing - if the panel and the CLI disagree, the CLI is
right.

## The three tools an agent sees

| Tool | What it does |
|---|---|
| `memory_write` | Record a fact. With a `key`, a later write on the same subject **replaces** it rather than competing |
| `memory_search` | Search current memory: this project plus `shared` |
| `memory_get` | The live answer for one subject key |

The `key` is what makes this a shared brain rather than a pile. Claude Code
writes `deploy.target = railway`; three weeks later Grok writes
`deploy.target = fly.io`; there is **one** live answer and the old one stays in
history with the agent that wrote it.

## Use it from anything else

Scripts, cron, a harness nobody has written yet:

```sh
membraid write "deploy target is railway" --key deploy.target --kind project_param
membraid search "deploy"
membraid get deploy.target
membraid history deploy.target
```

## Read it without any of this

That is the point:

```sh
cd ~/.membraid/vault
grep -ri "deploy" .
$EDITOR rules/never-force-push.md
rm facts/something-wrong.md
```

Nothing here needs tending. Tending it just makes it better.
