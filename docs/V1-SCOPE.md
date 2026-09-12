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

Claude Code, Codex and OpenCode users. In that order. Those are the clients
most developers actually drive, and the engine is one MCP server all of them
point at.

Everything else - DSH, Grok, a web UI, team deployments - is one more consumer
of the same surface and gets no special treatment in the design. The earlier
spec named DSH as a first-class target; it is not, and any sentence that reads
that way is stale.

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

## v1 build order

Each slice is useful on its own and testable before the next starts.

| # | Slice | SPEC ref | Why now |
|---|---|---|---|
| 1 | Wire log: line types, append, replay | §3.4, §5.3 | **done** - the only non-rebuildable artifact, so it is built and tested first |
| 2 | Vault: markdown + frontmatter, `init` | §4 | The source of truth. Files are the simple-interaction story |
| 3 | Hot index: `memories` + `concepts` + FTS5 | §5.1, §5.2 | The working set |
| 4 | **Keyed supersession** | §6.2, §6.3 | The validated core. Independently confirmed by MemStrata (arXiv 2606.26511) |
| 5 | Retrieval: scope filter, RRF, decay | §7.2 | Where "better with use" is actually delivered |
| 6 | stdio MCP server + Claude Code | §10, §14 | First real user |
| 7 | Distillation | §8 | Consolidation; produces drafts whether or not anyone promotes them |
| 8 | Sweep | §9 | Bounded store, zero maintenance |

Single process. Local only. One client at a time. No daemon, no shim.

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

Not by feature count. After a month of daily use:

1. **Do supersession chains grow?** If every chain is length 1, the `key`
   convention did not survive contact with real agents and distillation never
   fires. Tune or rethink §6.2.
2. **Does retrieval stay sharp as the store grows?** Decay either works or the
   store degrades like every other memory system.
3. **Is the store bounded without anyone tending it?** Sweep either runs or the
   zero-maintenance claim is false.

Note what is absent: nothing here requires a human to promote a draft. If
curation turns out to be something you do, good. The system is not allowed to
need it.
