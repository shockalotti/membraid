---
name: membraid
description: Use when recording, retrieving or maintaining the user's shared memory through membraid (memory_write, memory_search, memory_get, memory_used, memory_done, or the membraid CLI) - deciding whether something is worth remembering, choosing a kind, key and scope, recording, updating or finishing a task, correcting a memory that is wrong, or dealing with membraid sync, scopes or the vault.
---

# membraid

membraid is the user's memory, stored centrally for every agent they run and
every machine they work on. What you write is surfaced in this project's
digest and searchable from anywhere - it does not notify or interrupt other
agents. The session digest holds this project plus shared; other projects are
found with memory_search, not automatically.

You already have the basics: the server's instructions tell you when to write,
and the session starts with a digest of open tasks and known facts. This skill
is for the judgment calls and the occasional procedures.

## The one test

Before writing, ask: **would a future session - possibly a different agent,
on a different machine, with none of this conversation - do better work for
knowing this?** If not, do not write it.

## What is worth remembering

| Write it | Do not write it |
|---|---|
| The user corrected how you work: "don't use `pnpm dlx`, use `pnpm exec`" | That you ran the tests and they passed |
| A decision and its reason: "chose Postgres over SQLite for concurrent writers" | Anything already in the code, a README or the git log |
| A concrete project value you had to discover: deploy target, required Node version | Your plan for the next five minutes |
| A non-obvious cause that cost time: "build fails when the lockfile is stale" | Chatter, greetings, restating the question |
| Work left unfinished at the end of a session | Secrets, tokens, passwords, keys, personal data |
| Where knowledge lives outside this project: `membraid source add` with a folder, git repo or web address, e.g. `membraid source add https://github.com/you/api --about "API specs; read before changing endpoints"` (a "Knowledge location" memory, key `source.*`, that says who can reach it) | The documents' contents: point to them, and read them with your own tools |
| A standing preference that applies everywhere | Large pastes: logs, stack traces, code blocks |

Memory syncs to a git remote. **Never store credentials of any kind**, even
briefly, even if the user shares them in the conversation.

## Choosing a kind

`kind` is part of a memory's identity: the same key under two kinds is two
separate subjects. Pick deliberately.

- **preference** - how the user wants things done. "Prefers ISO dates", "wants
  small commits". If the user said it about themselves or their taste, this.
- **project_param** - a concrete value or choice for this project. "Deploys to
  Railway", "Go 1.27", "API versioned under /v2".
- **insight** - something observed or concluded that is not a choice. "Tests
  flake under parallel load because of a shared temp dir."
- **task_state** - work in progress, shown to the user as *where they left
  off*. If it will still be true at the end of the session, it is not a task.

Unsure between preference and project_param? If it would still hold in the
user's *next* project, it is a preference.

## Choosing a key

A key names the subject, so a later write on the same subject **replaces** the
old answer instead of piling up beside it. Give one whenever the subject can
change. Omit it only for one-off observations.

- Dotted, lowercase, general to specific: `editor.theme`, `deploy.target`,
  `db.migrations.tool`, `task.auth.fix`.
- Punctuation is normalised: hyphens, underscores and spaces become dots, so
  `task.auth-fix` is stored as `task.auth.fix`. Results show the stored form.
- Name the subject, not the value: `deploy.target`, never `deploy.railway`.
- **Reuse before you invent.** Search first, and write to the key that already
  exists. Two agents inventing `deploy.target` and `deploy.platform` for the
  same thing is how memory quietly splits into contradictions.

```
memory_search("deploy")          # find the existing key
memory_get("deploy.target")      # confirm its current answer
```

## Choosing a scope

Leave scope empty and the memory belongs to the current project. Use
`"shared"` for what is true regardless of which repo you are in: the user's
standing preferences, their conventions, facts about their machines. Project
facts stay in the project - a deploy target in `shared` would surface, wrongly,
in every other project.

## Writing a good memory

- **One fact per write.** Two facts in one sentence cannot be superseded
  separately.
- **Self-contained.** The reader has none of this conversation. Not "use the
  other one" - "use `pnpm exec`, not `pnpm dlx`".
- **Include the why when it is short.** "Chose Postgres: SQLite locked under
  concurrent writers" outlives "use Postgres".
- **No pronouns pointing at the conversation**: this repo, that file, the bug.

## Tasks

A task is how the next session - or the user glancing at their bar widget -
knows where things stand.

1. **Starting** multi-step work that might outlive the session: write a
   `task_state` with a key, e.g. `task.auth.fix`.
2. **Progressing**: write again with the *same key*. It replaces the earlier
   state rather than adding another open task.
3. **Finishing**: call `memory_done` with the key. An unfinished-looking task
   that is actually done misleads the user every time they look.
4. **Ending a session mid-task**: make sure the task says what is done, what is
   next, and anything blocking - enough to resume cold.

The digest lists open tasks with ids. A task written without a key can still
be closed: `memory_done` with its id.

## Reading

- **On resume, verify the digest is in your context.** A resumed session
  should open with membraid's digest. If it is absent - no digest text, no
  memory briefing anywhere above - memory was never loaded here: run
  `membraid context` (or `memory_search` for the task at hand) before
  answering. The hook delivers it; this rule is the backstop for when the
  hook itself failed.
- **At the start of real work**, search for the area you are touching.
- **Before asking the user something**, check whether they already told
  another agent.
- **Before assuming a default** (package manager, test command, style), check
  for a preference.
- **How search matches depends on the machine.** With embeddings on, it
  matches meaning, so describe what you need in plain words; otherwise it
  matches words. The server instructions say which. Either way, if nothing
  relevant comes back, try again: rephrase, use the tool or file name, or
  `memory_get` the key you expect. One miss does not mean memory has nothing.
- **Say what you used.** When a memory actually changes what you do (you follow
  a preference, use a project value, rely on an insight), call `memory_used`
  with its id or key; digest entries without a key show one as `#1a2b3c4d`.
  That is the signal that ranks useful memories higher for every agent.
  Memories you only read, or that merely showed up in results, do not count,
  and reporting them would bury the ones that matter.
- **Memory can be stale. The code is the ground truth.** If memory says Go 1.25
  and `go.mod` says 1.27, trust `go.mod` - then write the correction under the
  same key so the next agent is not misled.

## Correcting a memory

Write the correct fact with the **same kind and key**. It supersedes the wrong
one, which is kept as history, never deleted. Do not write the correction under
a new key: that leaves both answers live.

If the wrong memory had no key, write the correct one *with* a key, then
forget the old one by its id.

When there is **nothing true to replace it** - a tool that was removed, a plan
that was abandoned, something recorded by mistake - call `memory_forget` with
the id from `memory_search`. It leaves search and the session digest on every
machine and stays in history. Do not forget a memory just because it is old,
or because you disagree with it: if the user said it, ask first.

The user can always fix memory by hand - the vault is plain markdown. If they
say memory is wrong, correct it; do not argue with them about what it said.

## When MCP tools are not available

The CLI does the same thing from any shell:

```sh
membraid search "deploy"
membraid get deploy.target
membraid write "deploys to Railway" --kind project_param --key deploy.target
membraid done task.auth.fix
membraid forget --id ID  # retire a memory with nothing to replace it
membraid status          # open tasks and recent memory
membraid context         # the digest a session starts with
```

## Operations

These come up rarely. Do them when the user asks, or when you hit them.

- **Sync** runs on its own. `membraid sync` forces one; `membraid status` shows
  when it last ran.
- **A sync conflict** means a human edited the same vault file on two machines.
  Sync stops and names the file. Do not resolve it silently: tell the user
  which file, and that their local commit is intact.
- **A moved project**: if membraid warns that a project looks like it moved,
  run `membraid scopes`, confirm with the user, then `membraid rescope --from
  <old scope>`. Never rescope without confirming - two projects with the same
  folder name are not the same project.
- **The vault** lives at `membraid where`. It is markdown; editing it by hand
  is supported.
