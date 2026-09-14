# membraid - v1 build scope

**Read this before SPEC.md.** SPEC.md is the design reference: 1,450 lines,
fourteen rounds of adversarial review, and correct about things v1 will not
build for months. This file says what v1 actually is and why the rest waits.

---

## What this is

**Memory for coding agents that gets better with use instead of worse, stored
in files you can read.**

One sentence more: an agent writes a fact, the engine appends it to a plain-text
log in the vault and indexes it (markdown concept files are what distillation
will produce); a later fact on the same subject supersedes the earlier one instead
of competing with it; what nobody retrieves fades; and you can `grep` the whole
thing or fix a wrong line in your editor.

## Who it is for

The first user's actual stack, in order: **Claude Code, OpenCode, Grok Bot,
Hermes, DSH.** Not a guess at what most developers use - the person who will
find out whether this helps.

**All five speak MCP.** Hermes Agent supports stdio and remote HTTP servers
with discovery at startup; Grok CLI configures them in `~/.grok/config.toml`
(written by `grok mcp add`); DSH does too, and the spec line claiming otherwise
was stale. So one stdio MCP server reaches the entire stack, and that is the integration path.

The CLI stays as a second surface anyway, because it costs almost nothing and
covers anything that can run a shell command - a script, a cron job, a harness
nobody has written yet. REST waits for a consumer that can do neither.

A web UI, team deployments and anything else are consumers of the same two
surfaces and get no special treatment. The earlier spec named DSH as a
first-class target; it is not - it is last in the stack and reachable by CLI
like anything else.

## The design premise, corrected

The spec is written around a human who opens Obsidian, reads drafts, promotes
them to `stable`, and reads sweep reports. **Most people will never do that.**
Assume 98% will not, and design so that the 98% still win.

That is not a concession, it is the product:

| Behaviour | Needs the human? | In v1 |
|---|---|---|
| Keyed supersession - a newer fact retires the older one | no | **yes** |
| Decay - unretrieved material sinks in ranking | no | **yes** |
| Distillation - a subject seen repeatedly becomes one concept | no | **yes** |
| Sweep - stale material archives itself, store stays bounded | no | **yes** |
| Files you can read, grep, diff and delete | no | **yes** |
| Promotion of `draft` to `stable` | **yes** | supported, never required |

**It fails safe.** Curate nothing and everything stays `draft`: rank-penalized,
decaying, consolidated, greppable. That is already better than the alternatives.
Curate, and you get a trust tier on top. The habit is upside, never a
prerequisite - and any feature that only pays off if someone maintains it is a
feature for the 2%, so it ships last or not at all.

## What v1 is actually testing

Not "is the memory good." **Is the coherence worth having.**

The situation this exists for: Claude Code, Codex, OpenCode, Grok, Hermes,
OpenClaw - none of them talk to each other. Two machines, Windows and Omarchy.
Work on one thing Monday, something else Thursday. Keeping that coherent is
the hard part, and the daily cost of not doing it is not a wrong fact retrieved
six months later, it is opening a terminal and not knowing where you were.

So v1 is one shared brain across every harness and both machines, and nothing
more until that is proven useful.

**Cross-machine costs nothing.** The vault is a git repo. `git pull` is the
sync protocol. No code.

## v1 build order

Reordered around coherence. Each slice is useful alone and testable before the
next starts; the goal is the **usable** line below, not the bottom of the table.

| # | Slice | SPEC ref | Why now |
|---|---|---|---|
| 1 | Wire log | §3.4, §5.3 | **done** - the only non-rebuildable artifact, so it is built and tested first |
| 2 | Vault: markdown, frontmatter, `init` | §4 | **done** as a layout: `init`, `index.md`, `log.md`, and a concept-file writer. Memories themselves live in the append-only log in `.hot/`; nothing writes a concept file until distillation (slice 9), so readable per-subject files do not exist yet |
| 3 | Hot index: tables + FTS5 | §5.1, §5.2 | **done** - search is what makes one brain feel like one brain |
| 4 | **Keyed supersession** | §6.2, §6.3 | **done** - without it the shared brain holds five contradictory opinions |
| 5 | CLI: `write`, `search`, `get` | §10 | **done** - the universal adapter; anything that can shell out is connected |
| 6 | stdio MCP server | §10 | **done** - now six tools: write, search, get, used, done, forget |
| 7 | Wire into the stack, both machines | §14 | **done** - Claude Code, OpenCode, Grok, Crush, Pi, Gemini CLI and Copilot CLI on omarchy (Codex and Cursor CLI installed, not yet tested end to end); Hermes on the wynneclaw1 minipc. **Usable from here** |
| 7a | **Sync**: per-machine logs, push after writes, pull at session start, systemd timer | §3.3, §4.5 | **done** - switching machines is the case the whole premise rests on |
| 7b | Finishing tasks (`done`, `close` line) | §6.1 | **done** - without it an unkeyed task stayed in *where you left off* forever |

### Built beyond the plan, because use needed it

| What | Why |
|---|---|
| Omarchy bar widget + `status --json` | A visible reminder that memory exists, and one click to finish a task |
| Scope identity: git root commit (`g` + 8 hex), else path hash (`p` + 8 hex); scope registry, moved-project detection, logged `rescope` | People move folders; a path-based scope silently started an empty second brain |
| Session digest (`membraid context`) + MCP server instructions | Tools alone are passive; sessions now start knowing where you left off, and every harness is told when to write |
| Agent skill + `membraid install` (asks which harnesses, migrates the old `memory` server name) | How to write well, and one command to set up every harness |
| `forget` + `memory_forget` | Only tasks could be closed; a mistaken memory stayed in every digest for good |
| Mid-session refresh: import before every tool call, background pull once the pull interval elapses | Long-lived servers (Hermes's gateway runs for days) never saw memories pulled after they started |

### The quality layer: in progress

The original plan held these back until the store was big enough to hurt. The
decision now is to build them rather than wait. Agreed sequence:

1. **8a** - fix keyword search: stopwords dropped, identifiers kept whole, OR, bm25. **Done**
2. **8b** - retrieval tracking, decay in ranking, digest scoring (`membraid context --explain`), cross-machine retrieval checkpoints, and guidance for agents to rephrase a search that misses. **Done**
3. **Hybrid search** (§16 M11) - pure Go vector search, embeddings optional ([SEARCH-EVALUATION.md](SEARCH-EVALUATION.md)). **done** - vector store, local embedders (Ollama EmbeddingGemma or built-in all-MiniLM), `membraid embed`, vector-only search with keyword fallback, installer option 
4. **10** - sweep **Done**: `membraid sweep`, weekly from the sync timer: counts memories unused for 90+ days (left to fade, never deleted), flags open tasks untouched for 14+ days in the digest and status, checkpoints retrieval state, reports to log.md. Concept archival waits for distillation.
5. **9** - distillation, kept for its readable per-subject markdown files **Done**: `membraid distill`, every 30 minutes from the sync timer: subjects written twice or in two sessions become readable draft notes with their history; a note a person edits is never overwritten. Unkeyed clustering waits.

| # | Slice | SPEC ref |
|---|---|---|
| 8 | Decay in ranking | §7.2 |
| 9 | Distillation | §8 |
| 10 | Sweep | §9 |

Slices 8-10 make memory better. They do not make seven tools into one brain,
so they are not what the coherence test below measures.

### Concurrency is real

The plan assumed one process and one client at a time. In practice several
harnesses run their own MCP server against one vault at once (on the minipc,
Hermes alone runs two). What holds that together today: SQLite in WAL mode with
a busy timeout, one append-only log file per host, and a file lock around git
sync. `TestStressManyClientsOneVault` drives that shape hard: 4 long-lived MCP
servers and 4 streams of CLI processes writing 400 memories, half racing on
one key, while searches and syncs run throughout; then it rebuilds the index
from the log and clones the remote, and both must match exactly. Its first run
found three real bugs, all fixed: write transactions failed with "database is
locked" instead of waiting, a per-connection WAL switch failed the same way,
and a crash fragment in the log made every later write from that machine
unimportable (SPEC §5.3, amended in v1.14.2). It runs in CI.

Local only: each machine works on its own clone, and git is the only thing that
crosses the network.

## Deliberately deferred

Not cut, not wrong, just not yet. Each is correct design for a deployment that
does not exist on day one, and each is cheap to add when it does.

| Deferred | SPEC ref | Wait for |
|---|---|---|
| Daemon + stdio shim, flock, spawn races | §3.1 | Concurrent clients run without a daemon, and the stress test passes: SQLite serialises writers and a flock guards git sync. Revisit only if a daemon is needed for something else |
| Windows loopback, pid+nonce handshake | §3.1 | Windows support. The binary builds for Windows and has a Windows lock file, but is untested, and the Claude Code hook `install` writes uses bash syntax. Deferred by choice |
| REST API, TLS, bearer tokens, token->source | §11, §12 | Remote or non-MCP consumers |
| `backup` online API + capture order | §3.3 | Data worth backing up |
| `reindex` flock gating, `init` refusal | §3.3 | A resident daemon to race |
| Schema + protocol version skew | §5.3 | Releases now ship (`membraid update`), so machines can run different versions. The wire log is the only thing that crosses machines and its format is locked; each machine's index is its own and is rebuilt from the log. What remains is a guard for an older binary opening a newer index, before any schema change after v3 |
| Full scope ladder, `unscoped` quarantine | §17 | More than a `--scope` flag needs |
| Obsidian watcher | §16 M9 | The `reindex`-after-edit workflow to annoy someone |
| Vector search | §16 M11 | Decided, no longer deferred: pure Go over the existing SQLite index, optional local embeddings (EmbeddingGemma via Ollama, or built-in all-MiniLM), vector-only when on. Measurements and rejected alternatives in [SEARCH-EVALUATION.md](SEARCH-EVALUATION.md) |
| Embedding speed on low-end machines | - | Deferred by choice. The embedding bake-off picks a default model on a fast laptop first; the weakest supported machine (e.g. a 4-core minipc) is tested before that model is recommended to anyone |
| Cloud embedding APIs (Gemini, Voyage, Mistral, Jina and similar) | - | Deferred by choice: memory stays on the user's own machines. If ever added, strictly opt-in, with a clear warning that memory content leaves the machine |
| Folders as knowledge sources (indexing Obsidian vaults, docs or notes folders for search) | - | Dropped for now, on purpose: searching documents people wrote is a different problem from sharing what agents learn, agents can already read files and use filesystem or Obsidian MCP servers, and indexing arbitrary folders is the riskiest thing membraid could do with secrets. Instead, a memory records where knowledge lives ("the API specs are in ~/Work/specs/api"), and the skill tells agents to write those. Revisit only if daily use shows agents repeatedly pointed at the same outside folders by hand |

### Loose ends to pick up later

| What | State |
|---|---|
| Codex, live session | Installed and loads the skill and MCP server; never run against a model. Needs an OpenAI login or a local model: Codex no longer accepts the chat completions API that Gemini's compatible endpoint offers |
| Cursor CLI, live session | Installed; `cursor-agent mcp list-tools` shows the tools. Needs a Cursor login, and whether Cursor shows hook context to the model is decided on its servers |
| Embedding speed on the minipc | Measured on wynneclaw1 (4-core, Ollama EmbeddingGemma q4_0): 8.6 s to embed 9 memories cold, about 0.16 s per search. Write it up in SEARCH-EVALUATION.md with a larger store |

**The wire-log line format is not deferred and not provisional.** It is the one
artifact nothing can rebuild, so its format is locked now (§5.3, writer-side
rules). Everything above can change freely; that cannot.

## How v1 is judged

By whether it makes the day easier. After two weeks of daily use across at
least two harnesses and both machines:

1. **Did you stop re-explaining yourself?** Something told to Claude Code on
   Monday should be there for OpenCode on Thursday, and for Grok Bot after
   that. If it is not, the shared brain is not shared.
2. **Did `git pull` on the other machine just work?** If cross-machine
   continuity needs thought, it is not continuity.
3. **Did you ever open the vault directly?** Not to curate - just to look
   something up, or delete something wrong. If the files never earn a visit,
   markdown-as-truth is decoration and Engram is the better answer.
4. **Did the store stay coherent?** If `deploy.target` has five live values,
   keyed supersession is not firing and slice 4 needs work.

None of this measures memory quality, and that is deliberate. Quality is what
slices 8-10 buy, and buying it before coherence is proven is building the wrong
thing carefully.

If after two weeks the answer is "I did not notice it", that is a real result.
Stop, and use Engram.
