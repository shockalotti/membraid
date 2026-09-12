# Getting started

## Install, once

```sh
go install github.com/shockalotti/memory-engine/cmd/memory-engine@latest
memory-engine init
```

`init` creates `~/.memory/vault` and `~/.memory/index.db`. **Run it once, ever.**
Not per project.

If `memory-engine: command not found`, `~/go/bin` is not on your PATH:

```sh
echo 'export PATH="$PATH:$HOME/go/bin"' >> ~/.bashrc && source ~/.bashrc
```

## One vault, every project

A project is a **column value**, not a directory. Scope resolves automatically
from the git root you are standing in, so you almost never type `--scope`:

```sh
cd ~/Projects/api && memory-engine where
  vault   /home/you/.memory/vault
  scope   api-3f8a1c20
```

`api-3f8a1c20` is the basename plus a hash of the full path, so `~/work/api` and
`~/personal/api` stay separate brains rather than colliding.

Searches see **your project plus `shared`**. Write with `--scope shared` (or
`scope: "shared"` from an agent) for something true everywhere - a standing
preference, a rule you always want applied.

## Sync across machines

The vault is a git repo. That is the whole sync protocol.

```sh
cd ~/.memory/vault && git init && git add -A && git commit -m "memory"
git remote add origin <your-private-repo> && git push -u origin main
```

On the other machine: `git clone <repo> ~/.memory/vault`, then
`memory-engine init` is **not** needed - but the index is rebuilt from the vault
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
      "command": "memory-engine",
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
      "command": ["memory-engine", "mcp", "--source", "opencode"],
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
      "command": "memory-engine",
      "args": ["mcp", "--source", "grok"]
    }
  }
}
```

**Hermes Agent** and **DSH (DeepSeek Harness)** both take stdio MCP servers in
their own config; the command is the same, with `--source hermes` and
`--source dsh`.

Use an absolute path to the binary if a harness does not inherit your PATH.

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
memory-engine write "deploy target is railway" --key deploy.target --kind project_param
memory-engine search "deploy"
memory-engine get deploy.target
memory-engine history deploy.target
```

## Read it without any of this

That is the point:

```sh
cd ~/.memory/vault
grep -ri "deploy" .
$EDITOR rules/never-force-push.md
rm facts/something-wrong.md
```

Nothing here needs tending. Tending it just makes it better.
