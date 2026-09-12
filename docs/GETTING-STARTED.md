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

- **Claude Code** - a `SessionStart` hook in `~/.claude/settings.json`:

  ```json
  {
    "hooks": {
      "SessionStart": [
        { "hooks": [ { "type": "command",
                       "command": "/home/you/go/bin/membraid context --format claude 2>/dev/null || true",
                       "timeout": 10 } ] }
      ]
    }
  }
  ```

- **OpenCode** - a plugin at `~/.config/opencode/plugins/membraid.js` (source in
  `opencode-plugin/`). It uses `experimental.chat.system.transform`, which
  OpenCode marks experimental; if it is ever renamed the plugin goes quiet
  and the instructions below still apply.

The digest never breaks a session: an unusable vault produces an empty digest,
not an error.

**Every harness is told when to write.** The MCP server hands harnesses
instructions when they connect, which they place in the system prompt: write
when the user corrects you or a decision is made, key anything that can
change, record unfinished work as a task and mark it done, never store
secrets. It ships inside membraid, so there is nothing to install per harness.

What neither can do is decide *what is worth remembering* for the model. If
*What your agents learned* in the widget stays empty after a week, the habits
need work, not the plumbing.

## The tools an agent sees

| Tool | What it does |
|---|---|
| `memory_write` | Record a fact. With a `key`, a later write on the same subject **replaces** it rather than competing |
| `memory_search` | Search current memory: this project plus `shared` |
| `memory_get` | The live answer for one subject key |
| `memory_done` | Mark a task finished, by key or id, so it leaves *where you left off* |

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
