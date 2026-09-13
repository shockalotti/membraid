# Getting started

## Install, once

```sh
go install github.com/shockalotti/membraid/cmd/membraid@latest
membraid install
```

Run it once per machine, ever. Not per project.

`install` asks which of your harnesses to set up, with the ones it finds on this
machine already ticked, shows exactly what it will change, then does it:

| Harness | MCP server | Session digest | Agent skill |
|---|---|---|---|
| Claude Code | `~/.claude.json` | `SessionStart` hook | `~/.claude/skills/membraid` |
| OpenCode | `opencode.json` | plugin | shares the Claude copy |
| Grok | `grok mcp add` | none (Grok ignores hook output) | shares the Claude copy |
| Hermes | `hermes mcp add` | plugin | `~/.hermes/skills/membraid` |
| Omarchy bar widget | | | |
| Sync timer (systemd) | | | |

It also creates the vault at `~/.membraid/vault` if there is none. Then restart
each harness you picked, and start a **new** conversation: one opened before
membraid was installed keeps the tool list it started with.

**Hermes on a server** runs two services, and each starts its own membraid:
`hermes-gateway` (Discord, Telegram and other messaging) and `hermes-dashboard`
(the web dashboard, which is also the backend the Hermes desktop app connects
to). Restart both after upgrading membraid. Several membraid servers on one
vault at once is normal.

```sh
membraid install --dry-run                     # show the plan, change nothing
membraid install --harness claude-code,grok    # pick without being asked
membraid install --yes                         # every detected harness
```

Run it again whenever you like - after upgrading, or after moving the binary.
It changes only what is out of date, never duplicates an entry, backs up any
JSON config it edits to `<file>.membraid.bak`, and leaves every setting that is
not membraid's alone. Harness configs point at the binary's absolute path, so
harnesses that do not inherit your shell's PATH still find it.

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
  scope   g3f8a1c20 (api)
```

A git project's scope is its first commit (`g3f8a1c20`), so it follows the
project when you move or rename the folder, and `api` is just the readable
name. A folder with no git history gets a hash of its path (`p...`) instead.

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

membraid only ever writes inside its own folder. Today that folder shows
little in Obsidian: memories live in the append-only log in `.hot/`, which
Obsidian hides, so the only visible notes are `index.md` and `log.md`.
Readable concept notes, one per subject, are what distillation (slice 9) will
add. Pointing it at a vault root that already has files in it is refused,
with the subfolder command in the error.

Everything engine-owned lives in `.hot/`, which Obsidian hides: the wire log,
and the index. `.hot/.gitignore` keeps the rebuildable index out of git while
the log, which is history, stays in.

## Readable notes

Memories are lines in a log, which nobody wants to read. When agents come back
to the same subject, writing its answer a second time or from a second
session, membraid writes it up as a note you can open in any editor or in
Obsidian:

```
preferences/pkg-manager.md
projects/memory-engine/deploy-target.md
```

Each note is a `draft` holding the current answer and a History list of every
earlier answer, with when and which agent wrote it. It happens every 30
minutes on its own; `membraid distill` runs it now.

**The notes are yours to edit.** membraid rewrites a note only while it is
exactly what membraid last wrote. Once you change one (fix it, add to it, set
`status: stable`, move it to another folder), it is never overwritten again,
though new memories on the same subject are still linked to it. Your edits sync
to your other machines like everything else in the vault.

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

The vault is a git repo, and membraid keeps it in step with its remote on its
own. Set the remote once:

```sh
cd ~/.membraid/vault
git remote add origin https://github.com/<you>/membraid-vault.git   # keep it PRIVATE
git push -u origin main
membraid timer install        # Linux: periodic sync via a systemd user timer
```

On the other machine, clone it and use it. There is nothing to rebuild by hand:
the first command imports the whole history from the log.

```sh
git clone https://github.com/<you>/membraid-vault.git ~/.membraid/vault
membraid status
```

**How it syncs, and why not on a fixed timer.** The thing sync exists for is
switching machines, and a 30-minute timer would cost up to 30 minutes of memory
every switch. So:

- **After writes, it pushes** once they have been quiet for `push_delay_sec`
  (60s). A burst of agent writes becomes one commit.
- **When an agent session starts, it pulls.** Starting an agent is when you most
  likely just changed machines.
- **The timer checks every 5 minutes** and syncs if there are unpushed changes
  or the last sync is older than `pull_interval_min` (15). It covers CLI writes
  and pulls while no agent is open.

**Settings** are per machine and never synced:

```sh
membraid config                          # show them
membraid config set auto_sync false      # stop syncing on its own
membraid config set pull_interval_min 5
membraid sync                            # once, now
```

**Why two machines do not conflict.** Each machine appends to its own log file
(`writes-2026-09-omarchy.jsonl`), so appends never touch the same file. If both
changed the same subject while offline - `deploy.target` on each - the newest
write wins on *both* machines and the other is kept as history. The only real
conflict is a human editing the same markdown file on two machines; sync then
stops, names the file, and leaves your local commit intact.

On Windows there is no systemd: run `membraid sync --scheduled` from Task
Scheduler every 5 minutes.

## Wiring a harness by hand

`membraid install` does this for you. For a harness it does not know, or to see
what it wrote: every harness speaks MCP over stdio, and the server is always
named `membraid` - not `memory`, which collides with Hermes's built-in memory
tool and with the memory features of Claude Code and Grok, so an agent can
write to the wrong one. `--source` records which agent wrote what, so give
each harness its own.

```json
{
  "mcpServers": {
    "membraid": {
      "command": "/home/you/go/bin/membraid",
      "args": ["mcp", "--source", "my-harness"]
    }
  }
}
```

Installs from before the rename used the name `memory`; `membraid install`
migrates those entries, and only those that run membraid.

## The bar widget (Omarchy)

Pick *Omarchy bar widget* in `membraid install`. It goes just left of the power
button, unless you have already placed it somewhere else.

A brain icon in the top right, lit when a task is open or a sync failed. Click
it for:

- **Where you left off** - open tasks. Click one to mark it done.
- **What your agents learned** - recent memory, tagged with the harness that
  wrote it.
- **Sync** - when this machine last synced, *Sync now*, and a toggle for
  auto-sync.

It reads `membraid status --json` and acts only by running `membraid`
commands. If the panel and the CLI ever disagree, the CLI is right.

## How agents use it

Tools on their own are passive: an agent only touches memory if it thinks to.
Two things make it active, and both are set up once.

**Every session starts knowing where you left off.** A short digest - open
tasks in this project, what is known here, open tasks elsewhere - is put in
front of the model before your first message. It is a digest, not a dump:
the whole memory would bury the few things that matter.

```sh
membraid context        # see exactly what an agent starts with
```

- **Claude Code** - a `SessionStart` hook in `~/.claude/settings.json`.
- **OpenCode** - a plugin at `~/.config/opencode/plugins/membraid.js`. It uses
  `experimental.chat.system.transform`, which OpenCode marks experimental; if
  it is ever renamed the plugin goes quiet and the rest still applies.
- **Hermes** - a plugin that adds the digest to the first turn.
- **Grok** - none: Grok discards what a `SessionStart` hook prints.

The sources for all of these are in `assets/`, built into the binary.

The digest never breaks a session: an unusable vault produces an empty digest,
not an error.

**Every harness is told when to write.** The MCP server hands harnesses
instructions when they connect, which they place in the system prompt: write
when the user corrects you or a decision is made, key anything that can
change, record unfinished work as a task and mark it done, never store
secrets. It ships inside membraid, so there is nothing to install per harness.

**Every harness knows how to write well.** Instructions say *when*; the
membraid skill says *how* - whether something is worth remembering at all,
which kind, a key another session will reuse rather than reinvent, shared or
project scope, and how to correct a memory that turned out wrong. Harnesses
load it only when memory comes up, so it costs nothing otherwise. Source:
`assets/skill/SKILL.md`.

What none of this can do is decide *what is worth remembering* for the model. If
*What your agents learned* in the widget stays empty after a week, the habits
need work, not the plumbing.

## The tools an agent sees

| Tool | What it does |
|---|---|
| `memory_write` | Record a fact. With a `key`, a later write on the same subject **replaces** it rather than competing |
| `memory_search` | Search current memory: this project plus `shared` |
| `memory_get` | The live answer for one subject key |
| `memory_done` | Mark a task finished, by key or id, so it leaves *where you left off* |
| `memory_forget` | Retire a memory that is wrong with nothing to replace it; it stays in history |

The `key` is what makes this a shared brain rather than a pile. Claude Code
writes `deploy.target = railway`; three weeks later Grok writes
`deploy.target = fly.io`; there is **one** live answer and the old one stays in
history with the agent that wrote it.

## What an agent actually sees

**At session start**, four things reach the model:

| What | Size | Contains memories? |
|---|---|---|
| MCP server instructions: when to search, when to write, keys, no secrets | ~1,500 chars | no |
| Five tool definitions | ~3,900 chars | no |
| The membraid skill: only its description, until the agent loads it | ~375 chars (~7,800 if loaded) | no |
| The digest | under 4,000 chars | **yes** |

Claude Code and Hermes may keep MCP tools behind a tool search until the model
asks for them, so the definitions are not always in the prompt.

The digest is the only part built from memory. It holds up to 8 open tasks in
this project, the 12 newest facts (preferences, project values, insights) from
this project plus `shared`, each clipped to 240 characters, and a count of open
tasks per other project. It is chosen by **recency, not relevance**: it does not
know what you are about to ask. Claude Code gets it from the `SessionStart`
hook, OpenCode from its plugin (re-attached to every request, but the same text
all session), Hermes from its plugin on the first turn, and Grok not at all.

**During a conversation nothing is injected.** Memory enters the context only
when the agent calls a tool: `memory_search` returns up to 10 hits, one line
each with kind, key, content, scope, the agent that wrote it, and its id.
Whether it searches is the agent's call, guided by the instructions.

**Search is keyword full-text by default**: SQLite FTS5 with porter stemming,
common words dropped, ranked by BM25. It finds "deploy target" in a memory that
says deploy target; it will not connect "where does this app run" to "deploys
to Railway". Reusing keys is what keeps related memories findable. **With
semantic search turned on, search goes by meaning instead** (see below), and
falls back to keywords whenever the embedding model is unavailable.

**How long a memory takes to reach another machine.** A write is pushed about
60 seconds after writes go quiet. The other machine pulls when a session starts
there, when its 5-minute timer finds the last pull 15+ minutes old, and from
inside any running MCP server once that interval has passed. The digest is built
from what is already on disk while the session-start pull runs in the
background, so a memory written elsewhere a minute ago can miss a new session's
digest and still turn up in its searches.

## Semantic search (optional)

Keyword search finds memories that use the words you search with. Semantic
search finds them by meaning. On the search evaluation set, where half the
questions share no words with their answer, the right memory landed in the top
five for 47% of questions with keyword search, 96% with EmbeddingGemma, and 91%
with the model built into membraid. The full measurements and why this design
won are in [SEARCH-EVALUATION.md](SEARCH-EVALUATION.md).

**It stays on your machine.** Each machine computes its own embeddings with a
local model and keeps them in its local index. They are never written to the
synced vault, and nothing is sent to any cloud service.

**Turn it on** by picking *Semantic search* in `membraid install`, or by hand:

```sh
membraid config set embeddings ollama    # EmbeddingGemma through Ollama (most accurate)
membraid config set embeddings builtin   # or the model built into membraid, no Ollama needed
membraid embed                           # embed the memories you already have
```

- **ollama** uses `embeddinggemma:300m-qat-q4_0` (239 MB; `ollama pull` it
  first) unless `embed_model` names another. An idle Ollama server uses about
  60 MB; the model loads when membraid asks for it and unloads after 5 idle
  minutes.
- **builtin** uses all-MiniLM-L6-v2, downloaded once (about 90 MB) to your user
  cache directory under `membraid/models`. Slightly less accurate, and nothing
  else to install.

New memories are embedded in the background by running MCP servers and by the
sync timer; `membraid status` shows how many are covered. If Ollama is not
running when a search happens, that search uses keywords rather than failing.
Changing model re-embeds on the next `membraid embed`.

## Use it from anything else

Scripts, cron, a harness nobody has written yet:

```sh
membraid write "deploy target is railway" --key deploy.target --kind project_param
membraid search "deploy"             # results show each memory's id
membraid get deploy.target
membraid history deploy.target
membraid done task.auth.fix          # finish a task, or: done --id ID
membraid forget --id ID              # retire a memory with nothing true to replace it
```

## Read it without any of this

That is the point. Every memory is a line of plain JSON in the vault's
`.hot/writes-<month>-<host>.jsonl` files, and the vault is a git repo:

```sh
cd ~/.membraid/vault
grep -i "deploy" .hot/writes-*.jsonl
git log -p .hot/                     # who learned what, and when
```

The files are append-only history, so fix memory through `write` (same key) or
`forget` rather than by editing lines: a hand-edited line would not match what
other machines already imported. Markdown notes you drop in the vault are read
by `membraid ls` and `cat`; distillation (in progress) is where memories will
become markdown concept files.

Nothing here needs tending. Tending it just makes it better.
