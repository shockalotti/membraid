# Memory Engine - v1 build scope

**Read this before SPEC.md.** SPEC.md is the design reference: 1,450 lines,
fourteen rounds of adversarial review, and correct about things v1 will not
build for months. This file says what v1 actually is and why the rest waits.

---

## What this is

**Memory for coding agents that gets better with use instead of worse, stored
in files you can read.**

One sentence more: an agent writes a fact, the engine files it as markdown and
indexes it; a later fact on the same subject supersedes the earlier one instead
of competing with it; what nobody retrieves fades; and you can `grep` the whole
thing or fix a wrong line in your editor.

## Who it is for

The first user's actual stack, in order: **Claude Code, OpenCode, Grok Bot,
Hermes, DSH.** Not a guess at what most developers use - the person who will
find out whether this helps.

**All five speak MCP.** Hermes Agent supports stdio and remote HTTP servers
with discovery at startup; Grok CLI configures them in `.grok/settings.json`;
DSH does too, and the spec line claiming otherwise was stale. So one stdio MCP
server reaches the entire stack, and that is the integration path.

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
| 2 | Vault: markdown, frontmatter, `init` | §4 | **done** - the source of truth, and the whole simple-interaction story |
| 3 | Hot index: tables + FTS5 | §5.1, §5.2 | Search is what makes one brain feel like one brain |
| 4 | **Keyed supersession** | §6.2, §6.3 | Cheap, and without it the shared brain holds five contradictory opinions |
| 5 | CLI: `write`, `search`, `get` | §10 | **The universal adapter.** Anything that can shell out is now connected |
| 6 | stdio MCP: same three tools | §10 | Native surface for Claude Code and OpenCode |
| 7 | Wire into the stack, both machines | §14 | **<- usable here.** Stop and use it |
| 7a | **Sync**: per-machine logs, push after writes, pull at session start, systemd timer | §3.3, §4.5 | **done** - switching machines is the case the whole premise rests on |
| 7b | Finishing tasks (`done`, `close` line) | §6.1 | **done** - without it an unkeyed task stayed in *where you left off* forever |

Then, and only when the store is big enough to hurt:

| # | Slice | SPEC ref | Wait for |
|---|---|---|---|
| 8 | Decay in ranking | §7.2 | Search results getting noisy |
| 9 | Distillation | §8 | Enough repetition to consolidate |
| 10 | Sweep | §9 | The store growing unpleasantly |

Slices 8-10 are the quality layer. They make memory better. They do not make
seven tools into one brain, so they are not what v1 is testing.

Single process. Local only. One client at a time.

## Deliberately deferred

Not cut, not wrong, just not yet. Each is correct design for a deployment that
does not exist on day one, and each is cheap to add when it does.

| Deferred | SPEC ref | Wait for |
|---|---|---|
| Daemon + stdio shim, flock, spawn races | §3.1 | A second concurrent client |
| Windows loopback, pid+nonce handshake | §3.1 | Windows support |
| REST API, TLS, bearer tokens, token->source | §11, §12 | Remote or non-MCP consumers |
| `backup` online API + capture order | §3.3 | Data worth backing up |
| `reindex` flock gating, `init` refusal | §3.3 | A resident daemon to race |
| Schema + protocol version skew | §5.3 | A second shipped version |
| Full scope ladder, `unscoped` quarantine | §17 | More than a `--scope` flag needs |
| Obsidian watcher | §16 M9 | The `reindex`-after-edit workflow to annoy someone |
| Vector search | §16 M11 | FTS5 to demonstrably fall short |

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
slices 7-9 buy, and buying it before coherence is proven is building the wrong
thing carefully.

If after two weeks the answer is "I did not notice it", that is a real result.
Stop, and use Engram.
