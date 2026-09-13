# membraid

One memory shared by every AI agent you use, on every machine, stored as plain
text in a git repo you own.

## The problem

Claude Code, OpenCode, Grok, Hermes: none of them talk to each other. Tell one
agent how you like things done and the next one has never heard of it. Switch
machines and you start over. Come back to a project on Thursday and nobody
remembers where Monday left off.

membraid is one brain behind all of them.

## What it does

- **One answer per subject.** A memory written with a key (`deploy.target`)
  replaces the previous answer instead of piling up beside it. The old one stays
  in history with the agent that wrote it.
- **Where you left off.** Unfinished work is recorded as a task, shown until an
  agent marks it done.
- **Sessions start informed.** A short digest (open tasks, recent facts for this
  project) is put in front of the model before your first message.
- **Every machine, via your own git remote.** The vault is a git repo. membraid
  pushes after writes and pulls when a session starts. Each machine appends to
  its own log file, so machines never conflict over memory.
- **A bar widget for Omarchy**: open tasks, what your agents learned, sync status.

## Supported harnesses

| Harness | MCP tools | Digest at session start | Agent skill |
|---|---|---|---|
| Claude Code | yes | yes | yes |
| OpenCode | yes | yes | yes |
| Grok | yes | yes, from a terminal (a `grok` shell function; bash and zsh) | yes |
| Hermes | yes | yes | yes |
| Anything else | any MCP stdio client: `membraid mcp --source NAME` | | |
| Scripts, cron | the `membraid` CLI | | |

## Install

Needs Go 1.27 or newer (prebuilt binaries are not published yet).

```sh
go install github.com/shockalotti/membraid/cmd/membraid@latest
membraid install
```

`install` detects your harnesses, asks which to set up, shows exactly what it
will change, and does it. It is safe to run again.

To share memory across machines, give the vault a **private** git remote:

```sh
cd ~/.membraid/vault
git remote add origin git@github.com:<you>/membraid-vault.git
git push -u origin main
```

**Never store secrets.** Memory syncs to a git remote. Agents are told not to
record passwords, tokens or keys; do not ask them to.

## Thirty seconds with the CLI

```sh
membraid write "deploys to Railway" --kind project_param --key deploy.target
membraid write "deploys to Fly.io" --kind project_param --key deploy.target
membraid get deploy.target        # project_param  deploys to Fly.io  [id 7c1e...]
membraid history deploy.target    # both answers, newest first
membraid search "deploy"
membraid status                   # open tasks and recent memory
membraid context                  # the digest an agent starts with
```

Scope follows the git project you are in; `--scope shared` makes a memory
visible everywhere.

## Status

Early. Used daily by its author across two Linux machines and four harnesses.

- Linux is tested. Windows builds but is untested.
- Search is keyword full-text by default. Semantic search is optional and
  local: EmbeddingGemma through Ollama, or a model built into membraid.
- Decay and a scored session digest are in; sweep and distillation are next.

## Docs

- [Getting started](docs/GETTING-STARTED.md): install, scopes, sync, what an
  agent actually sees
- [v1 scope](docs/V1-SCOPE.md): what is built, what is next, and why
- [Spec](docs/SPEC.md): the full design reference

## License

[Apache-2.0](LICENSE).
