# Engram gap: what we take, what we leave

> Written 2026-09-15 during the delivery-gap discussion, with the Engram repo
> (Gentleman-Programming/engram, MIT, 6.6k★, fetched fresh - not the memory
> summary) open in front of me. This is the shelf read. It pairs with
> docs/V1-SCOPE.md which now names Engram as membraid's closest cousin - the
> reference point for the two-week frame experiment, not a code base to copy.

Engram and membraid are the same architecture (single Go binary, SQLite+FTS5,
MCP server, agent-agnostic install, git vault, supersession-with-history,
heat/use ranking, optional local embeddings). The storage/ranking/sync/write
models are, after the v1.19 review, graded equal. So there is no gap to close
there, and this doc does not relitigate the shelf - it only answers "what does
Engram ship that membraid doesn't, and are we building any of it?"

## The surface table

Assets Engram ships that membraid does not, ranked by what the Sep 29 report
could actually say back.

| Engram surface | membraid today | Verdict | Reason (from the discussion) |
|---|---|---|---|
| **TUI** (`engram tui`: dashboard / recents / observation detail / search results; j-k-Enter-c-/-Esc, Catppuccin) | living CLI browse (`ls`/`cat`/`memories`) + omarchy bar widget | **leave, unless one condition** | A TUI is a fourth read surface - another way a human *reaches* for memory. That is precisely the delivery pattern the two-week frame experiment is measuring (sessions start knowing, versus reaching). A prettier browser does not close the delivery gap; it refines the losing side. The one condition that justifies revisiting: if the Sep 29 report (or the weekly ritual) shows sessions land fine but the human cannot *audit* (browse-to-verify), then a lightweight read surface earns its keep. Otherwise: out of scope by measurement, not by mood |
| **Web dashboard** (`.templ` templ + HTTP API, browser view) | bar widget glance + `status --json` | **leave** | A dashboard is a browser-facing reach surface. membraid's positioning explicitly refuses a second presentation ("notes are a view of memory, never a second source", and the widget already covers glanceability). Adding a dashboard without the delivery question resolved would be polish that hides a gap, not closes one |
| **Engram Cloud** (SaaS or Docker+Postgres, project-scoped replication, browser+Obsidian brain, web/desktop "Engram Bot") | deliberately none; git is the only transport; local-first | **never - positioning, not schedule** | membraid's hard-won delimiter is "memory stays on the machines you run; git is the transport". Engram Cloud moves memory (or a derived model) to a server and adds a Bot that observes remote sessions. Same two refusals as Grok Bot and Hermes dashboard: (a) an LLM distilling context from transcripts, (b) memory leaving the user's machines. This is the boundary we already drew against Honcho/Engram; nothing about Engram being closer-shaped makes the boundary weaker |
| **Obsidian Brain export** | no graph export | **defer** | A knowledge-graph read is a reach surface with distillery overlap. Engram's own docs flag it as an export convenience, not core. If the memory "never used" line in the report trends, a human-facing export becomes the after-spin - cheap to add when the data says read surfaces are what's wanted |
| auto-inject relevant context at session start (Engram digest / Honcho frame) | digest + in-call search, agent must reach | **this is the one take - and it is the experiment** | See below |

## The one take is already instrumented

Every Engram, Honcho and Engram surface above is a delivery *placeholder* -
they are the visible answer to "memory reaches the model" (Honcho auto-injects
distilled context per message; Engram auto-injects a session summary at start).
membraid's own digest and in-call search wait for the agent to reach.

That is the gap. And it is already the live experiment: a relevance-scored frame
(scope + heat + recency top-k) injected at fresh-session start, journaled
(`t:inject`), two weeks of baseline first (baseline live since v1.19,
2026-09-15; report ritual ~weekly; decision ~Sep 29). The frame is Honcho's
*timing* without Honcho's *mechanism* (no LLM, no inference from chats; one
journalled line per injection).

So the doc's job is mostly **not to build the other surfaces while that
experiment runs.** If the frame proves sessions still don't land, Engram is the
honest end - and a TUI/dashboard/cloud built in the meantime would have been
time spent making the losing side prettier.

## Ground rule for this shelf

Anything Engram/Honcho/Engram offers that (a) needs an LLM in the delivery
loop, (b) auto-derives context from conversation transcripts rather than
accepting agent-written memory, or (c) moves memory off machines you own - is
out by positioning, permanently. Everything else is a candidate, gated on the
two-week frame result. The one honest Engram take (session-start delivery) is
already the measured variable, not a plan.
