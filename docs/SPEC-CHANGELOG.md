# Memory Engine - Spec Changelog

A running log of what changed in `docs/SPEC.md` and *why*. Newest first,
mirroring the vault's `log.md` convention. Findings and verdicts live in
`docs/REVIEW.md` (one row per change); this file carries the rationale a table
row cannot. Each review round prepends its entry here before handing off to the
next agent in the loop.

---

## v1.18 (closing the open items from the spec review)

**Import order no longer changes the result (§3.3, §5.3).** The review found
that incremental import could leave one machine's index different from
another's and from a rebuild: a rescope applied whenever it arrived moved
whatever was in the scope then, and a close, note link or checkpoint row naming
a memory not imported yet did nothing and was never retried. A rescope now
leaves an alias from the old scope id, out-of-order rescopes trigger a replay
of the whole log, and lines naming a missing memory wait for it. Import
rereads a file whose bytes before the saved position changed, checkpoints carry
the project registry, and a rebuilt index keeps each day's full use counts.

---

## v1.17.1 (notes are a view of memory)

**The concept mirror is not built, by decision (§5.2, §7.3, §4.4).** The spec
reads distilled notes back into search, with a trust tier: drafts ranked lower,
promotion to `stable`, deprecated and archived notes excluded. None of it was
built, yet the docs told people to edit, promote or delete notes, which
changed nothing agents see. Following the design premise that almost nobody
curates, notes are now a readable view: agents read memories, and correcting
what they know means writing the memory again. The docs, the vault's index.md
and every note's footer say so.

**The note bugs the review found are fixed without the mirror:**

- **Ownership travels with the note.** Whether membraid may rewrite a note was a
  hash kept only in the writing machine's index, so a note stopped updating on
  every other machine and after any rebuild. Each note now ends its frontmatter
  with a `membraid:` line: a SHA-256 of the rest of the file, so any machine can
  tell an untouched note from an edited one. Notes written before this still use
  the local hash until membraid next rewrites them.
- **A deleted note stays deleted.** A subject whose memories are linked to a note
  that no longer exists under any name is not written again.
- **A retired subject's note says so.** When every memory on a keyed subject is
  forgotten or finished, its note is rewritten `deprecated`, stating at the top
  that the last answer is no longer current (§6.2, cold deprecation).
- **One unreadable note no longer stops distillation.** A note whose frontmatter
  cannot be parsed is skipped and named, and never overwritten.
- **Same-named projects no longer share a note.** When a new note's path already
  holds another subject's note, the project folder carries the scope id too.

---

## v1.17 (implementation amendments from the spec review)

A review of every section against the code found places where the build broke
the spec's own reasoning. Fixed:

**Sync no longer lets two machines block each other (§4.5, §3.3).** Every
machine inserts log.md entries under its header, and distillation writes notes
at paths derived from keys, so any two changes to either between syncs
conflicted, and the second machine's sync failed on every run after. The
rebase now settles conflicts on the files membraid itself maintains: log.md
keeps both machines' entries, and a note still an untouched distilled draft
(status `draft`, with the distillation footer) on both sides takes the
remote's version, for the next distill pass to rewrite. Any other conflict,
including a note a person edited, still aborts cleanly for a human.

**`memory_get` reads the effective scopes (§7.2, §17).** It matched the exact
scope only, so a standing preference in `shared` was invisible from every
project. It now returns the current answer from the project and from
`shared`, each labelled.

**Unresolved writes go to `unscoped`, never `shared` (§17 step 5).** A write
with no resolvable project fell back to `shared`, the one bucket every project
reads, which is exactly the pollution §17 forbids. It now lands in `unscoped`,
which no read includes by default, which cannot be requested by parameter or
`MEMBRAID_SCOPE`, and whose size `status`, the sweep report and the widget
show. Reads with no project still read `shared`. Not built: `memory_stats`.

**Schema version skew is guarded (§5.3).** Any version mismatch ran the
additive schema and stamped the binary's own version, so an older binary
silently downgraded a newer index. An index from a newer membraid is now
refused; schemas from 3 up upgrade in place (every change since only adds
tables); anything older is set aside and rebuilt from the log. `membraid
reindex` does the same on demand, keeping the old index as a `.bak`.

**Checkpoints carry changes only (§3.4).** Each checkpoint restated every row
ever retrieved and every heat subject into a log that is never pruned, so its
size grew with the store. It now carries what changed since this index's last
checkpoint; replay already keeps the newest value per row and per machine, so
the result is identical.

**Ranking cannot reinforce itself (§7.2).** v1.16 promised use could not beat a
clearly more relevant match, but cosine relevance sits in a narrow band, so a
large boost did. Search now scores relevance relative to the best match times a
bounded use factor (0.6 to 1.3). The digest, which agents follow and so feed
with use, caps boost at 2 and keeps three places for the newest memories. A
`memory_get` is a retrieval, no longer a use, and a `scope: "*"` search records
no retrieval, restoring §7.2's rule that popular rows must not refresh
themselves.

**The principles now say what was decided (§1, §2, §18).** The adapters ship
in the binary, and v1 has several writer processes rather than one; both were
recorded in V1-SCOPE but contradicted by unrewritten principles. V1-SCOPE no
longer claims the store archives itself, that §5.2 is done, or that git is the
only thing that uses the network.

**Still open, awaiting a decision:** the concept mirror (§5.2, §7.3). Notes are
written but never read back into search, so editing, promoting or deleting a
note changes nothing agents see, and notes stop updating on a second machine.

---

## v1.16 (ranking by use, and ranking settings that follow the user)

**Use, not appearance, is the ranking signal (§7.2).** Rank used to decay from
the last time a memory was retrieved, and every search result counted as
retrieved: a search returning ten memories kept all ten fresh, and a memory
used fifty times ranked like one used once. Now each subject carries *heat*:
1 per write, history included, and 1 per reported use, each halving every
`halflife_days`. A use is `memory_used` (new tool; `membraid used` on the CLI),
which agents are told to call when a memory changed what they did, or a
`memory_get` of that exact subject. `boost(heat)` is the heat up to one use,
then `1 + frequency_boost x ln(heat)`, so repetition lifts a memory without
letting it beat a clearly more relevant one. Search ranks by
`relevance x boost` and the digest by `kind x scope x boost`; `search --explain`
and `context --explain` print the factors. A never-used memory's heat is its
own write fading, which ranks as before. Unkeyed facts in the digest carry a
short id (`#1a2b3c4d`) so they can be reported.

**Heat crosses machines in checkpoint lines, without a new line type or
version (§5.3).** Readers refuse unknown `t` and `v` values, so either would
break every older binary still syncing the vault. Checkpoint lines gain two
optional arrays instead, which older readers skip: `heat` (this machine's
heat per subject, with the time it was last updated) and `uses` (use counts
per agent per day for two weeks). A reader keeps the newest value per machine
and sums across machines; because heat fades in proportion, each machine's
value can be faded to now separately, so the sum is exact and no use is
counted twice. This is an amendment to the writer-side rule that any field
change bumps `v`: an optional field that an older reader ignores without
misreading anything may be added in place, and must be recorded here.

**Ranking settings live in the vault.** `halflife_days`, `frequency_boost`,
`digest_items` and `digest_shared_weight` are set with `membraid config set`
as before but stored in `.hot/settings-<host>.json`, one file per machine so
two machines never conflict in git, newest value per key winning. The log
reader only opens `writes-*.jsonl`, so older binaries never see them. A
halflife set locally before this still applies until the vault has one.

---

## v1.15.5 (implementation amendments: Cursor CLI)

**Cursor CLI takes a hook, in its own shape.** The server goes in
`~/.cursor/mcp.json`, where global servers load without approval. The digest
comes from a `sessionStart` hook in `~/.cursor/hooks.json`, whose entries are
flat rather than Claude Code's groups; Cursor ignores plain stdout and reads
`additional_context` from JSON, so `membraid context --format cursor` prints
`{"additional_context": ...}`. The Cursor editor reads the same files. The skill
is the `~/.agents/skills` copy; Cursor also reads `~/.claude/skills` and merges
skills by file path, not name, so both copies appear where both exist.

**Configured and checked, not yet live.** `cursor-agent mcp list-tools` shows
membraid's five tools; a session could not run without a Cursor login, and
whether Cursor's servers show hook context to the model is decided server-side.

---

## v1.15.4 (implementation amendments: Gemini CLI and Copilot CLI)

**Gemini CLI takes the digest the way Crush does.** `gemini mcp add -s user`
registers the server with `--digest`; Gemini places server instructions in the
system context, in trusted folders only, which is also the only place it runs
user MCP servers. Its `SessionStart` hook was rejected: plain stdout does not
reach the model, and after `/clear` the context is not added again. The skill
is the shared `~/.agents/skills` copy.

**Copilot CLI needs the instructions carried by a hook.** Copilot includes MCP
server instructions only for servers on a built-in allowlist, unless every run
passes `--allow-all-mcp-server-instructions`, so the habits in the server
instructions never reached it. `membraid context --format copilot` prints
`{"additionalContext": ...}` holding the server instructions followed by the
digest, run by a `sessionStart` hook in `~/.copilot/hooks/membraid.json`. The
server goes in `~/.copilot/mcp-config.json`; the skill is the `~/.agents/skills`
copy.

**Both verified with real sessions** against a throwaway vault: each stated a
fact only the digest held before calling any tool, then wrote and searched
through membraid.

---

## v1.15.3 (implementation amendments: Codex, Crush and Pi)

**§14 wiring reaches three more harnesses, each through what it actually
offers.** Codex takes the MCP server through `codex mcp add`, the digest from a
`SessionStart` hook in `~/.codex/hooks.json` (Codex asks the user to trust a new
hook once, through `/hooks`), and the skill from `~/.agents/skills`. Crush has
no session-start hook but places MCP server instructions in the system prompt,
so `membraid mcp` gains `--digest`, which appends the digest for the server's
working directory to its instructions. Pi has no MCP by design, so a Pi
extension runs `membraid mcp --source pi --digest` per session, registers its
tools and appends the instructions to the system prompt; it shares the
`~/.agents/skills` copy of the skill with Codex.

**Read tools declare `readOnlyHint`.** `memory_search` and `memory_get` carry the
MCP read-only annotation, so harnesses that gate tool calls, Codex among them,
can let them run without asking.

**Verified by running each harness, not only by config tests.** Crush and Pi
each passed a real session against a throwaway vault with a Gemini model: the
model stated a fact it could only have from the digest before calling any tool,
then wrote and searched through membraid, and the rows landed with the right
source. Codex is installed and loads the skill, but is not yet tested end to
end: it no longer accepts the chat completions API that Gemini's compatible
endpoint provides.

---

## v1.15.2 (implementation amendments: distillation, sweep and staging)

**§8 distillation writes readable notes, keyed subjects first.** A keyed subject
(preference, project_param or insight) qualifies when its answer has been
written at least twice, or in at least two sessions; MCP servers now stamp each
write with a per-process `session_ref` so the second condition can be counted.
Unkeyed clustering by trigram similarity waits. A note goes to the type's folder
(`preferences/`, `projects/`, `facts/`), in a subfolder named for the project
when the subject is not shared, named by its key. It is a `draft` whose body is
the current statement and a History list of every earlier statement with its
date and source. An existing note for the same `(scope, type, key)` is reused
wherever a person moved it. membraid rewrites a note only while its bytes hash
to what membraid last wrote; an edited or promoted note is never overwritten,
but new rows are still linked to it with a `distill` line, which replay now
applies. Runs every 30 minutes from the scheduled sync and on
`membraid distill`, with a log.md line when files change.

**§9 sweep, without concepts.** Counts rows unused for 90 days and unlinked,
flags open tasks untouched for 14 days in the digest and status, checkpoints,
and reports to log.md. Archival of concept files waits.

**§4.5 staging.** Sync keeps `git add -A`; see the implementation note there.

---

## v1.15.1 (implementation amendment: retrieval state and decay)

**§3.4 checkpoints merge by newest time, and sync writes them.** The spec made
each checkpoint a full snapshot superseding all earlier ones. That was written
for one writer. With one log per machine, the last snapshot replayed would
erase every other machine's retrievals, so replay now keeps the newest
`last_retrieved` per row. Sync writes a checkpoint at most hourly when anything
was retrieved since the last, so retrieval state reaches other machines and
survives a rebuild without waiting for sweep. Line format unchanged.

**§7.2 decay is live in keyword search.** bm25 selects a candidate pool five
times the result size (at least 50) and decay reorders it:
`relevance x exp(-ln2 x unused_days / halflife_days)`, halflife 30 days,
unused counted from the later of the write and the last retrieval. Only
intentional reads touch `last_retrieved`.

**The session digest is scored, not newest-first.** `kind weight x scope weight
x (1 + ln writes) x decay`, with preference 1.0, project_param 0.9, insight
0.6, shared 0.7 outside the shared scope, and room kept for three standing
preferences. `writes` counts every write to the subject, history included.
`membraid context --explain` prints each factor. The weights are starting
values, to be tuned from real use.

---

## v1.15 (search decisions, from measurement)

Semantic search moved from "later, if FTS5 falls short" to decided, on
measurements rather than argument. The full record, with every table and the
rejected alternatives, is `docs/SEARCH-EVALUATION.md`. What it changes in §7.2:

- **Keyword query semantics.** Queries no longer AND every token. English
  stopwords are dropped, identifier-like tokens (env vars, paths, versions,
  error codes) are kept whole as phrases, and the remaining terms are ORed and
  ranked by bm25. The AND form matched nothing for natural-language questions:
  0 of 85 ordinary test queries, against 15 of 15 identifiers.
- **With embeddings on, retrieval is vector-only, not fused.** RRF fusion of
  vector and keyword results lowered top-5 recall from 0.91-0.96 to 0.65-0.72
  on the test set, because paraphrased queries still share common words with
  wrong memories. Gated and identifier-boosted variants at best tied
  vector-only. Keyword search is what runs when embeddings are off.
- **Vectors are derived, per-machine index state.** Embeddings and binary codes
  live in the local index and are never written as wire-log lines, so the
  locked log format is untouched. A machine can use a different model from
  another, and changing model re-embeds locally.
- **M11 no longer waits for FTS5 to fall short.** It is scheduled after slice
  8. The store is pure Go over the existing SQLite index (binary codes plus
  exact rescoring: recall 0.9975 at 1.42 ms long-lived at 100K rows), chosen
  over SQLite's vec1 and sqlite-vec. Embeddings are optional and local:
  EmbeddingGemma 300M q4_0 via Ollama, or all-MiniLM-L6-v2 built in.

Decay (§7.2 step 3) still applies to whichever ranking runs.

---

## v1.14.2 (implementation amendment, found by a stress test)

**§5.3: a line that is not JSON is skipped wherever it sits, and writers start
a fresh line after a torn tail.** v1.14 skipped a truncated *final* line and
refused a partial line anywhere else as corruption. That rule was only safe if
nothing is ever appended after a crash, which is false: the next write, from any
process, follows the fragment. Appending straight after it glued the new record
onto the fragment, producing one corrupt line mid-file, and replay then refused
the file forever - so a single crash or full disk made everything that machine
wrote afterwards unimportable, on rebuild and on every other machine. A test
that appends a fragment and then writes again reproduced it.

The writer now checks the last byte before each append and prefixes a newline
when the file ends mid-line, so a fragment is always a line of its own; two
writers racing on the same fragment leave a blank line, which is skipped. The
reader skips any line that is not valid JSON, because such a line can only be a
write that never completed. A complete JSON record that fails to decode still
refuses, as does an unknown `v`: those are records that exist. The line format
itself is unchanged, so no `v` bump.

The same stress test found two index defects that are implementation, not
spec: write transactions began as reads and failed with SQLITE_BUSY_SNAPSHOT
when another process committed first (now `BEGIN IMMEDIATE`), and every open
rewrote the schema and every import took the write lock even with nothing new
(now both skipped when current).

---

## v1.14.1 (decision close-out, not a review round)

Both reviewers concurred that prose review had reached diminishing returns and
that implementation should begin. One change was made first, because it is the
one that becomes expensive the moment real data exists.

**§5.3 gains the writer-side half of wire-log versioning.** The section already
said what a *reader* does with a `v` it does not recognise (refuse) and with a
truncated final line (skip, added in v1.14). It never said when a *writer* must
produce a new `v`. That asymmetry is only safe while the format never changes.
Now stated: any change to a line type - field added, removed or renamed, a
field's meaning or units changed, an existing field reinterpreted - bumps `v`
for that line type, and the engine keeps a parser for every `v` it has ever
emitted, with no expiry, because the log is never pruned and a binary writing
`v:3` must still read `v:1` lines written years earlier. Replay is a replay,
not a re-derivation: it cannot reconstruct a dropped field or reinterpret one
whose meaning moved, so the obligation belongs on the writer and cannot be
deferred to a migration step that the design deliberately does not have.

Two supporting notes went in with it. First, **why this rule is load-bearing
rather than tidy**: the wire log is the only artifact not rebuildable from
something else - the index is a derived cache healed by `user_version` plus
`reindex`, the vault is human source of truth under git - and within the log
the `checkpoint` line is the sharpest edge, since it alone carries
`last_retrieved` for both tables, state that neither the vault nor a rebuild
can reconstruct. A careless change there has nothing behind it. Second, **the
stamp stays per line, not per file**: a per-file header is the obvious
simplification and is wrong, because `writes-YYYY-MM.jsonl` rotates monthly
while upgrades happen whenever, so one file routinely holds lines from two
binary versions and a header would misdescribe its own tail after the first
mid-month upgrade. Recorded so the simplification is not attempted later.

**Never rules re-checked, none violated:** no heavy machinery (three paragraphs
of rule, no registry, no negotiation, no migration framework - the explicit
rejection of a per-file header removes a mechanism rather than adding one);
history still never overwritten, and this strengthens that by making a
format change unable to silently drop it; nothing else in the body changed.

Rounds 3 through 14 produced everything above this entry. Implementation starts
at M0; the review loop resumes against code rather than prose.

---

## v1.14 (review round 14, reviewer: opus 5/claude)

Round 14 restated R13-1..R13-57 and verified all fifty-seven `closed` against
the v1.13 body - the three R13 fixes by direct inspection of §3.3 and the
M0/M1 acceptance rows rather than from the handoff table - then filed four new
findings (R14-58..R14-61), all folded into v1.14. Two are substantive, one
closes a recurring class rather than another instance of it, and one is a
rendering defect the loop introduced in itself. Changes and why they were
necessary:

1. **Machine columns survive every mirror refresh, not just the rebuild
   (§5.2, §16 M9).** Four rounds - R9-38, R10-44, R11-48, R12-52 - were spent
   making sure a rebuild does not reset the curated tier's decay, and the rule
   they produced is written for `reindex` alone. The mirror has three refresh
   paths: `reindex`, the watcher's per-file pass, and the engine's own refresh
   after propose or sweep archive. The two uncovered ones are the *common*
   case, and the watcher fires on the human edit that promotes a `draft` to
   `stable` - the single curation act the trust model is built around. A
   per-file refresh upserting from frontmatter writes a column frontmatter does
   not contain, so the obvious implementation resets that concept's
   `last_retrieved` every time a human touches the file, undoing on the
   frequent path exactly what the checkpoint protects on the rare one. One rule
   now covers all three: frontmatter fields are replaced, machine columns are
   preserved, carried forward from the existing row or from the checkpoint when
   there is none.

2. **The captured log is read whole-lines-only, and replay tells a truncated
   line from an unknown one (§3.3, §5.3, §16 M1).** R12-51 solved tearing for
   the index - online backup API, never `cp` - and said nothing about the log
   captured beside it. R13-57 then made that captured log *authoritative* and
   had a restore replace the vault's embedded `.hot`, which removed the only
   other copy and so deepened the gap rather than leaving it flat. A capture
   racing an append ends mid-line, and §5.3 covered an unknown `v` while saying
   nothing about a truncated one. The two are opposite failures and must not
   share a handler: a partial final line is a write that never completed, so by
   log-before-index no committed row corresponds to it and skipping loses
   nothing; an unknown `v` is a complete record this binary cannot read, which
   must stop replay rather than silently drop data that exists. A partial line
   anywhere but at the end is neither - that is corruption, and refuses.

3. **The acceptance-lag class is closed structurally (§16 preamble, M1).**
   The M1 row carried R12-51's capture order without R13-57's authority rule,
   and restated the `(scope, type, key)` fallback without its ambiguous-match
   skip. That is precisely the drift R13-55 and R13-56 fixed one round ago, one
   row over, on the very fix R13 had just made - and the class has now recurred
   in rounds 6, 13 and 14. Fixing instances one at a time visibly has not
   stopped it, and nothing said which text wins when a row and its section
   disagree. §16 now states that acceptance rows restate the body and never
   extend it, that the section wins on conflict, and that a row citing a
   section sits inside that section's blast radius when it changes. The M1 row
   was brought current in the same pass.

4. **The M1 acceptance row is one physical line again (§16).** Found while
   making change 3: that cell has been split across eight newlines since a
   mid-loop edit, which stops it being a table row at all. Every other
   milestone renders inside the table; the densest and most-cited one has been
   rendering as loose paragraph text beneath it, in a document whose stated
   virtue is that a human can open it in Obsidian and read it. All twelve rows
   are verified complete, and §16 now requires one physical line per row
   however long it grows. Worth recording rather than quietly repairing,
   because the loop introduced this defect in itself while fixing others.

**Never rules re-checked, none violated:** no heavy machinery (one refresh
rule, one capture rule, one replay branch, one precedence sentence - no new
subsystem); trust still derived rather than self-asserted, and change 1
strengthens it by making a human promote no longer cost the concept its
retrieval history; facts still supersede with history never overwritten; hot
rows still never deleted; `unscoped` still never auto-included and never
requested; `source` still absent from every match and never per-call; promotion
still a human act the engine never performs.

**On convergence.** Not reached by the letter. Four findings against 5, 4, 4, 3
is flat rather than falling, but the mix has changed: R14-61 is the loop's own
formatting damage, R14-60 closes a class instead of an instance, and only
R14-58 and R14-59 are design gaps - both in the narrow seam between a path that
was hardened and an adjacent path that was not. The rounds-12-and-13
recommendation stands and firms up: build M0. Both substantive findings live in
code that does not exist yet, a per-file mirror upsert and a log-capture loop,
and either would have surfaced them on the first day of writing it.

---

## v1.13 (review round 13, reviewer: opencode/big-pickle)

Round 13 restated R12-1..R12-54 and verified all fifty-four `closed` against
the v1.12 body - the four R12 fixes by direct inspection of §3.3, §3.4, §8 and
§17, the same discipline Round 12 applied to the Round 11 fixes - then filed
three new findings (R13-55..R13-57). Two were acceptance rows lagging rules the
body already carries, spotted by that body-vs-table re-verification: the
handoff table would have waved them through as closed. Changes and why they
were necessary:

1. **`backup` names one log, and it is the captured one (§3.3).** R12-51's
   capture order - index, wire log, vault - treats the wire log and the vault
   as separate artifacts, but the wire log lives *inside* the vault
   (`vault/.hot/writes-YYYY-MM.jsonl`, §3.2), and the vault snapshot is a fixed
   commit. The separately captured log is therefore strictly newer than the one
   the restored vault embeds, and the design said nothing about which log is
   authoritative after a restore. The bullet now says the captured log is
   authoritative and a restore *replaces* the restored vault's embedded `.hot`
   rather than appending to it - appending to a stale replica would resurface
   superseded writes as fresh rows, a door past supersede-all.
2. **M1's acceptance reads `(scope, type, key)` like the body does (§16).**
   R12-52 gave §3.4 the full fallback identity and left the M1 row quoting the
   old `(scope, key)` fallback. Row now matches body; no design change.
3. **M0's acceptance refuses a requested `unscoped`, like the body does
   (§16).** R12-53 extended the `*` refusal to any requested `unscoped`, and
   the M0 row enumerated only the `*` half. The row now names both refusals and
   keeps fall-through only.

Violations of earlier never rules checked - none: supersede-all and the
two-pointer close, write-before-commit, no `*` and no forged quarantine, the
maintenance envelope all still stand.

---

## v1.12 (review round 12, reviewer: opus 5/claude)

Round 12 restated R11-1..R11-50 and verified all fifty `closed` against the
v1.11 body - the four R11 fixes by direct inspection of §3.3, §3.4, §8 and §17
rather than by the handoff table - then filed four new findings
(R12-51..R12-54), all folded into v1.12. Round 11's handoff asked whether each
of its fixes was stated narrowly enough to be side-stepped by an adjacent case.
All four were: each landed correctly on the case it was written for and stopped
at the edge of its neighbour. Changes and why they were necessary:

1. **`backup` gets a method and a capture order; `init` gets a refusal
   (§3.3, §16 M1).** R11-47 gated `reindex` against a resident daemon, and the
   bullet directly beneath it promises `backup` makes "a consistent copy of
   index + vault + wire log" with neither gate nor method. Both halves of that
   promise were unbacked. A plain file copy of `index.db` under WAL is torn -
   committed frames sit in the `-wal` sidecar until a checkpoint, so the copy
   silently lacks its most recent commits - and nothing ordered the three
   captures. A backup taken log-first captures rows whose wire-log lines are
   missing, and a restore from it drops them at the first rebuild: R10-43's
   exact failure, re-entering through the tool meant to recover from it.
   `backup` now uses SQLite's online backup API, snapshots the vault at a fixed
   commit rather than the working tree, and captures index, then log, then
   vault - the backup-side face of log-before-index, chosen for the same
   reason. It still takes no flock, because it writes nothing; it is the one
   read path whose correctness is an ordering rule. `init` refuses on an
   existing vault or index instead of re-initializing under a live writer, and
   a general sentence now requires every artifact-touching subcommand to state
   its stance on a resident daemon. The spec names four CLI entry points and
   v1.11 gated one; gating one and leaving its neighbours unstated is how a
   guarded escape hatch grows an unguarded back door.

2. **The checkpoint entry carries `type`, and an ambiguous restore skips
   (§3.4).** R11-48 was right that a key alone is no identity and added
   `scope`, then landed the fallback on `(scope, key)` - describing it as "the
   mirror's identity axis minus the type". The mirror's identity is
   `(scope, type, key)`: §5.2 indexes it, §6.2 de-dupes on it. One scope may
   legitimately hold a `preference` and a `rule` under the same key, so the
   fallback can match two rows, and nothing said which wins. Dropping `type`
   also bought nothing - the curation R11-48 set out to survive, the
   scope-generalizing edit, changes path and scope and never type. The entry
   now carries all three and the fallback is the full identity; a fallback that
   still matches more than one row skips the entry rather than guessing,
   because writing one concept's decay onto another's is worse than the reset
   being avoided.

3. **A requested `unscoped` is refused like a requested `*` (§17 step 1).**
   R11-50 extended the `*` rejection from the parameter to the resolved scope,
   closing the door a `--scope "*"` pin had opened. `unscoped` walks through
   that same door untouched. §17 has called it "never a deliberate
   destination" since v1.4 and nothing ever refused a deliberate one: a
   `scope: unscoped` call, or a shim pinned `--scope unscoped`, writes straight
   into the quarantine bucket. Its entire worth is that its size means
   "something is misconfigured" - §9 reports it, `memory_stats` surfaces it -
   so a caller able to write there deliberately can forge the only diagnostic
   this design offers about itself, and the bucket stops being evidence of
   anything. It may now be reached, by step 5's fall-through, and never
   requested.

4. **Distillation's de-dupe skip writes its `distill` line (§8 step 5, §3.4,
   §16 M4).** R11-49 ordered the two writes correctly and rested the crash case
   on a recovery that does not recover. §8 step 5 said the de-dupe check would
   "link `related` and skip", which never writes the line, so the cluster's
   rows never regain `source_concept` - and §9's sweep item 1 treats
   `source_concept IS NULL` as an unlinked stale candidate. The skip repeats on
   every later pass, so sweep flags those rows forever while their concept sits
   in the vault the whole time. §3.4 called this "adopted"; skipping is not
   adopting. The skip path now writes the line, which fixes the crash recovery
   and is the correct steady-state behaviour anyway: rows that match a covering
   concept genuinely are sourced from it.

Violations of earlier "never" rules checked and none introduced: no heavy
machinery (two refusals, one field, one ordering rule, one line written on a
path that already ran), no status promotion, no `source` in any match, no
per-call identity, `unscoped` still never auto-included and now never
requested either, hot rows still never deleted.

**On convergence.** Not reached, and the ledger's round note carries the
recommendation in full. In short: these four are real defects rather than
polish - a data-loss path through the recovery tool, a non-unique restore key,
a forgeable diagnostic, and a stated recovery that does not run - but the shape
has repeated for three rounds now, each fix creating the edge the next round
finds. Rounds 10, 11 and 12 produced five, four and four findings with no
downward trend. That is a property of reviewing prose rather than evidence of
an unsound design, and the highest-value next step is probably M0 rather than
round 13.

---

## v1.11 (review round 11, reviewer: opencode/big-pickle)

Round 11 restated R10-1..R10-46 and verified all forty-six `closed` against the
v1.10 body, then filed four new findings (R11-47..R11-50), all folded into
v1.11. Round 10's handoff named the four v1.10 constraints as this round's
adjacency targets - the maintenance lock R10-42, log-first R10-43, the
path+key checkpoint R10-44, and the `*` parameter rejection R10-37 - and each
one answered with a second writer, a missing field, a second ordering, or a
second door. Changes and why they were necessary:

1. **`reindex` is gated on the daemon's flock (§3.3, §16 M1, §15).** The
   rebuild is the escape hatch for every divergence bug, and it is also a
   second writer with no gate: the daemon holds an exclusive flock on
   `~/.membraid/daemon.lock` from spawn until exit, `idle_timeout` defaults to
   `0`, so a daemon is almost always resident when the manual escape hatch is
   used, and a `reindex` that races it collides on git `index.lock` exactly
   like the two-process collision §3.1 spends its opening paragraph on.
   R10-42 serialized the writers *inside* the daemon; this closes the one
   writer that was still outside it. Reindex now takes the same flock
   non-blocking and refuses with a clear message ("stop the daemon, reindex,
   start it again") if the daemon holds it. The upgrade path needs no manual
   stop - there the old daemon is already shutting down before the new one's
   open-time rebuild (§5.3).

2. **The checkpoint's concepts entry carries `scope` (§3.4, §16 M1).**
   R10-44 keyed the restore by `path` then `key` "within the same scope" - but
   the checkpoint line carries only path and key, so "the same scope" was a
   constraint the replay could not see. Keys are scoped subjects: two scopes
   can both hold `editor.theme`, and the scope-generalizing edit (§4.3, the
   human move that promotes a concept to `shared`) changes path and scope
   together - the exact curation a fallback must survive. The entry now
   carries `scope`; restore matches path, then `(scope, key)`, the mirror's
   identity axis minus `type`. A concept moved *and* re-scoped still loses its
   state: bounded, visible, and stated honestly.

3. **Distillation's two writes get an order: file first, line second
   (§3.4).** R10-43 ordered the `memory_write` log against its transaction and
   left distill's two-destination write unspecified: a concept file into the
   vault and a `distill` line into the log. Line-first is the wrong order - a
   crash leaves `source_concept` links pointing at a path the mirror does not
   contain after a rebuild. File-first inverts the failure into one the system
   heals: a crash leaves an unlinked concept file, which the next pass's
   de-dupe check (§8 step 5) finds already covered and links `related` instead
   of duplicating, and the mirror re-discovers the file from the vault on any
   rebuild.

4. **The `*` write rejection is applied to the resolved scope (§17 step 1,
   §16 M0).** R8-37 rejected `*` on the explicit `scope` parameter, but the
   ladder resolves scope in the daemon, not from the per-call parameter: a
   shim launched with `--scope "*"` / `MEMBRAID_SCOPE="*"` pins a literal `*`
   scope through the hint with no parameter passing through the check. The
   write/propose handler now validates the resolved scope after the ladder;
   `*` is never a stored bucket, however it arrives.

Violations of earlier "never" rules checked and none introduced: no heavy
machinery (a flock already in §3.1, one field on an existing line type, one
ordering sentence, one validation point), no status promotion, no `source` in
any match, no per-call identity, `unscoped` still never auto-included, hot
rows still never deleted.

---

## v1.10 (review round 10, reviewer: opus 5/claude)

Round 10 restated R9-1..R9-41 and verified all forty-one `closed` against the
v1.9 body, then filed five new findings (R10-42..R10-46), all folded into
v1.10. Round 9's handoff named three adjacency checks and kept round 8's "what
does the spec assume is never true" lens; all three checks came back positive
and the lens produced two more. The five share one shape: a rule the spec
states for one case and silently assumes for another - one process, one
endpoint kind, one write ordering, one path that never moves, one clock that
only moves forward. Changes and why they were necessary:

1. **One maintenance lock serializes every vault-mutating job (§3.1, §13,
   §16 M3).** "One writer" has been a principle since v1.2 and §3.1 spends its
   opening paragraph on why two *processes* writing one git repo is a whole
   bug class. Then §13 puts MCP handling, the REST server, the distill timer,
   the sweep timer, the hourly wire-log commit and (M9) the watcher in
   concurrent goroutines of one daemon, four of which mutate the vault and run
   `git`. `index.lock` collides between goroutines exactly as between
   processes: the same bug, one level down, in a section that exists to kill
   it. And it is not a rare interleaving - `sweep_every` (168h) is an exact
   multiple of `distill_every` (30m), so every sweep fires on an instant a
   distill is also due, and R9-39 had just widened both to the full index.
   All such work now runs under one mutex; a scheduled job that finds it held
   skips its tick and logs rather than queueing, because a distill half an
   hour late is worth nothing over one that just ran. Read paths never take
   it. One mutex, not a job framework.

2. **The wire log is written before the index (§3.3, §16 M1).** §3.3's entire
   durability argument rests on the log being a superset of the index, and
   nothing ordered the append against the SQLite commit. Index-first means a
   crash in between leaves a row with no line - and since v1.8 made "rebuild
   by `reindex`" the routine upgrade path, that row survives only until the
   next upgrade and then vanishes, weeks after the crash that orphaned it,
   with nothing left to explain it. Log-first inverts the failure into one
   replay already heals. It is the same species as close-before-insert
   (§6.2), which the spec already judged worth a paragraph: one sentence to
   state, silent for months to get wrong.

3. **The concepts checkpoint carries `key` beside `path` (§3.4, §16 M1).**
   R9-38 restored the curated tier's retrieval state across rebuilds and keyed
   the snapshot by `path` - which is the one attribute this design actively
   invites a human to change. §4.1 says folders carry human organization and a
   person navigates the vault by folder, so moving `rules/foo.md` into
   `decisions/` is ordinary curation, and a path-only restore quietly resets
   the decay of every concept anyone reorganized. That reintroduces, for the
   reorganized subset, precisely the loss R9-38 closed for everything else.
   Restore now matches path, then key within scope. A keyless concept renamed
   outside the engine still loses its state: bounded, stated, and the reason
   `key` is worth carrying for the ones that have it.

4. **The Windows loopback path stops being "identical" (§3.1, §16 M0).** The
   spec asserted equivalence with the unix socket path twice, and that blanket
   hid the one difference that matters: a unix socket path cannot be taken
   over by an unrelated program, but a TCP port can be recycled. After a crash
   the port in a stale port file may belong to something else, and a shim that
   connects and sends its hello frame hands `engine.token` to an arbitrary
   local process - a stale-endpoint nuisance on one platform is a credential
   disclosure on the other. The port file now carries pid and nonce, the
   daemon speaks first echoing the nonce with its `protocol_version`, and the
   shim sends nothing until that matches. R9-40's refusal handling waited on
   "the socket", which does not exist here, so its wait clause was generalized
   to the endpoint; the handshake bullet's own "identical" claim was corrected
   in the same pass, since the daemon now speaks first on that platform.

5. **`unused_days` is floored at zero (§7.2).** The decay term assumed a clock
   that only moves forward. A backward step - an NTP correction, a resumed VM -
   makes it negative, and `exp(-ln2 * negative / halflife)` is greater than
   one: a multiplier that boosts precisely the rows decay exists to suppress,
   with the oldest rows boosted hardest.

One finding was withdrawn before it reached the spec. R10-46 originally also
claimed §5.2 described the mirror rebuild as a wholesale reset with no
retrieval-state carve-out; §5.2 already carries it, and the initial check that
said otherwise was a bad grep rather than a gap. The ledger records the
correction rather than the claim.

Violations of earlier "never" rules checked and none introduced: no heavy
machinery (one mutex, one ordering rule, one field on an existing line, a
nonce), no status promotion, no `source` in any match, no per-call identity,
`unscoped` still never auto-included, hot rows still never deleted.

---

## v1.9 (review round 9, reviewer: opencode/big-pickle)

Round 9 restated R8-1..R8-37 and verified all thirty-seven `closed` against the
v1.8 body, then filed four new findings (R9-38..R9-41), all folded into v1.9.
Round 8's handoff asked the reviewer to point at "what the spec assumes is
never true, that time or operations will eventually make true" - the upgrade
question that produced R8-35/36. Three of the four findings are exactly that
question, and they share a shape: each names a situation the mechanism assumes
cannot arise, that ordinary operation produces anyway. Changes and why they
were necessary:

1. **The checkpoint snapshot covers the concepts mirror, not just `memories`
   (§3.4, §5.2, §16 M1/M5).** R7-30 and R8-33 fought hard so a rebuilt index
   does not "look never-retrieved" - the phrase is verbatim in §3.4 - but that
   guarantee, and the checkpoint mechanism that delivers it, are written for
   hot rows only. `concepts` carries its own `last_retrieved`, consumed by the
   same decay rule, and reindex re-mirrors the concepts table wholesale from
   the vault, so every rebuild reset the whole curated tier to
   never-retrieved. That was a latent since-forever bug, and v1.8 made it
   routine: §5.3 turned "rebuild by reindex" into the ordinary upgrade path,
   so the weeks of doubled decay after every upgrade were going to be a
   fixture of the design. The checkpoint line now carries the concepts side
   (path-keyed) as part of the same full snapshot, and replay restores it into
   the rebuilt mirror exactly as it restores hot rows.

2. **Distill and sweep get their scope envelope (§8, §9, §17, §10, §7.2).**
   Both tools take "optional `scope`", and neither the spec nor §17 ever said
   what that means or - more importantly - what a *scheduled* pass covers. A
   scheduled run has no caller and therefore no caller-scope to derive; the
   only coherent envelope is the full index, `unscoped` included - which is
   also the only way the sweep report's quarantine-size line is even reachable.
   An implementer reading "optional scope" would most plausibly wire it to the
   §17 read rule ("absent -> own scope plus `shared`"), which would silently
   starve the report of every other bucket and make scheduled and on-demand
   behaviour diverge. The envelope is now stated once, canonically in §17 next
   to the `*` sentinel, and cross-referenced from §8, §9, and both tool rows:
   a pass covers every bucket unless an explicit `scope` narrows it, and a
   maintenance pass refreshes no retrieval state - closing the one loop-hole
   left in the §7.2 three-path table, which now names distill's enumeration
   alongside the sweep's.

3. **A spawned daemon that refuses to start is specified (§3.1, §16 M0).** The
   spawn-race bullet presupposes a daemon that binds within the retry window -
   but the design has two startup refusals that bind nothing: the §5.3 schema
   refusal (a downgraded binary against a newer index) and config errors. The
   shim's retry loop was the only specified behaviour, so a refused daemon
   meant five silent retries and then nothing - indistinguishable from a hang,
   which is exactly the bug class §3.1 exists to kill. The shim now waits for
   the socket or the daemon's exit, and on exit without binding surfaces the
   reason once and fails the connection. This is the operational counterpart to
   R8-35: that finding made the daemon's side of a version mismatch loud; this
   one makes the upgrade path's other half loud too.

4. **The §15 `scope` row stops describing v1.7's ladder (§15).** It still said
   "default scope ... (remote mode, or unresolved cwd)" after v1.8 renumbered
   the ladder; a shim always sends a `scope_hint`, so "unresolved cwd" is no
   longer how the daemon default is reached from a shimmed connection. The row
   now names step 4, who actually reaches it, and what happens when neither a
   hint nor the default exists.

Violations of earlier "never" rules checked and none introduced: no heavy
machinery (one array on an existing line type, one sentence each in §8/§9/§17,
one failure-mode bullet), no status promotion, no `source` in any match, no
per-call identity, `unscoped` still never auto-included (and now still swept,
so the report stays honest), hot rows still never deleted.

---

## v1.8 (review round 8, reviewer: opus 5/claude)

Round 8 restated R7-1..R7-30 and verified all thirty `closed` against the v1.7
body, then filed seven new findings (R8-31..R8-37), all folded into v1.8. Two
came from the adjacency check round 7 asked for and both landed on the same
v1.7 change; three had been sitting in the spec for several rounds and were
simply never asked. Changes and why they were necessary:

1. **The §17 ladder is renumbered so its order is the order (§17, §3.1,
   §16 M0).** v1.7 numbered the ladder 1 explicit, 2 shim pin, 3 daemon
   default, 4 shim cwd - and then composed it as 1, hint (2 then 4), 3, 5,
   swapping the daemon default and the cwd slug for every shim connection.
   v1.7 noticed the *consequence* (it states that step 3 is unreachable behind
   a shim) without noticing that the numbering is the thing an implementer
   codes from, so the document specified one precedence and described another.
   The executed order was the right one: a global configured default should
   not outrank a specific signal about where the work is happening, and a
   default belongs at the bottom, firing when nothing more specific spoke. So
   the numbering moved rather than the behaviour. The "unreachable step 3"
   caveat is gone because the situation it described no longer exists; the
   daemon default still applies mainly to shim-less callers, but now as a
   consequence of precedence rather than an admitted dead rung. §3.1's
   handshake text carried the old step numbers and was updated with it.

2. **A connection resolved to `unscoped` no longer auto-includes the
   quarantine bucket (§17).** §17 says an absent `scope` means "the caller's
   own scope, plus `shared`", and four lines later that "`unscoped` is
   **never** auto-included". When the ladder falls all the way through, the
   caller's own scope *is* `unscoped`, so those two sentences contradict each
   other for precisely the misconfigured connections the bucket exists to
   isolate - the one case where the invariant has to hold. An absent parameter
   on such a connection now means `shared` only: it can still read curated
   knowledge, and its own writes remain reachable by asking for them by name.

3. **All three read paths are named for the refresh rule (§7.2, §16 M1).**
   R7-28 pinned `last_retrieved` for fusion survivors and exact *key* hits and
   left the rest to inference. `memory_get` by id or path is just as
   intentional a read and was unstated; worse, nothing exempted diagnostics,
   so `memory_stats`, the sweep's own enumeration, and `scope: "*"` triage
   would refresh the very rows they report as stale. That is the failure worth
   preventing: the report resets the clock it just measured, and the next run
   finds nothing wrong. A three-row table now says it outright - retrieval
   refreshes, inspection does not.

4. **Exact lookups span `concepts`, not just `memories` (§7.2, §10, §11).**
   `key` has been a column on `concepts` since v1.4, indexed, and the exact-
   lookup path R6-26 introduced was written entirely in terms of hot rows. So
   "what is the current value of `editor.theme`?" - the question §10 calls the
   cheapest useful one an agent can ask - answers with the working memory
   while hiding the curated concept on the same subject. That is the exact
   inversion `min_concept_results` exists to prevent on the fusion path,
   reappearing on the path that skips fusion. Exact lookups now return both,
   concepts first, with the §7.2 status exclusions still applying.

5. **`protocol_version` gets a meaning (§3.1, §16 M2).** It has ridden in the
   hello frame since v1.6 and appeared exactly once in the whole document,
   specified nowhere. It is not decoration: `idle_timeout` defaults to `0`, so
   the daemon is *designed* to outlive every client, including the binary
   upgrade that replaces it on disk. A new shim meeting a months-old resident
   daemon is the normal upgrade path, and it is the one moment when "one
   writer" can be two different versions of itself. On mismatch the daemon now
   refuses and reports its version; the shim, knowing it came from the newer
   binary, requests shutdown, waits for the flock, and respawns. In-place
   negotiation would be heavier and pointless when both processes are the same
   binary in every supported deployment.

6. **The on-disk formats carry versions (new §5.3, §16 M1).** Nothing in the
   spec versioned the schema or said what replay does with a wire-log `v` it
   does not recognise - on a design whose single guarantee is one writer, and
   whose schema changed in four of the last five revisions. The rebuildable
   index makes the answer cheap: `PRAGMA user_version` older than the binary
   expects triggers a rebuild by `reindex` (the wire log is already the source
   of truth and `reindex` is already the best-tested path in the system, so
   migration DDL would be a second and worse way to do the same job); newer
   refuses to open, because a downgraded binary writing an unknown schema is
   exactly how single-writer stops meaning anything. Unknown wire-log `v` is
   refused rather than guessed: the log is never pruned, so old lines must
   stay readable forever and new ones must never be silently misread.

7. **Smaller (§17 step 1, §5.1).** `*` is a query sentinel and nothing
   rejected it on write, so a literal `*` bucket was reachable - a row no
   ordinary query can see and only a maintenance sweep would ever find; it is
   now rejected on `memory_write` and `memory_propose`. And `valid_from` and
   `created_at` are identical for every row the engine writes, with no stated
   invariant, while decay reads one and the temporal model reads the other; a
   schema comment now records that they diverge only under backdating, which
   nothing here does, and that both readers must be revisited together the day
   someone adds it.

Violations of earlier "never" rules checked and none introduced: no heavy
machinery (one table, one pragma, three refusals - no migration framework, no
negotiation protocol, no registry), no status promotion, no `source` in any
match, no per-call identity, `unscoped` still never auto-included (and now
genuinely so), hot rows still never deleted.

---

## v1.7 (review round 7, reviewer: opencode/big-pickle)

Round 7 restated R6-1..R6-26 all `closed`, verified against the v1.6 body,
then filed four new findings (R7-27..R7-30), all folded into v1.7. Round 6's
handoff asked whether v1.6 "did the same thing to something next to it" - this
round's findings are exactly that check coming back with four small yeses, all
adjacent-gap fixes rather than design changes. Changes and why they were
necessary:

1. **The unkeyed ancestry walk gets an index: `idx_memories_superseded_by`
   (§5.1, §8).** R6-19 indexed the *keyed* path (`idx_memories_key_all`) and
   left the unkeyed half of the same walk unindexed - exactly the adjacency
   R6-19's own reasoning implied. The unkeyed walk's expensive step is the
   child enumeration (`WHERE superseded_by = ?`, needed at every node to reach
   forked branches); the parent step is a primary-key lookup that needs
   nothing. Without the index the walk was a table scan on the hot table at
   every distill pass, and an implementer trimming for "maintainability" might
   reasonably have dropped the fork-enumeration entirely, quietly reintroducing
   the undercounted-chain bug R6-18 fixed. One partial index removes the 
   temptation and matches the keyed path's cost profile.

2. **Exact key lookups refresh `last_retrieved` (§7.2).** R6-26 defined the
   key-only exact-lookup path but never stated what it does to retrieval
   bookkeeping, leaving the survivor rule's fate on that path to whoever
   implements it. An exact hit is the *intentional* read the survivor rule was
   written to distinguish incidental echoes from ("an adapter deliberately
   asking for the current value of a subject"), so it is a retrieval and should
   refresh. Left undefined, the easy implementation plumbs the exact path
   through the same bookkeeping as fusion - or skips it - and either way an
   actively-queried subject ages toward `sweep_unused_days` and decays out of
   fusion rank while being read every session. The rule is now one sentence:
   exact hits refresh, fusion echoes do not.

3. **`scope_hint` composes with the §17 ladder (§3.1, §17).** R6-22's
   handshake carries `scope_hint` but never said what the shim puts in it or
   how it interacts with a ladder that straddles both processes (steps 2 and 4
   are shim facts; 1, 3, and 5 are daemon facts). The natural implementation -
   shim resolves flag/env then cwd into one hint, daemon then applies explicit
   param, hint, configured default, `unscoped` - is now written down, along
   with the consequence that the daemon's own scope default is only reachable
   on shim-less connections (a shim always has a cwd). The alternative
   readings (shim sends raw cwd and daemon applies the flags it cannot see, or
   a daemon default that never fires for the primary consumer) were the kind
   of ambiguity that gets resolved wrongly at 2am.

4. **Unkeyed clustering and near-duplicate detection use the one fuzzy
   threshold (§8, §9, §15).** The fuzzy number for *supersession* has been
   explicit since v1.3, but §8's unkeyed clustering and §9's concept
   near-duplicate check both said "Dice-over-trigrams similarity" with no
   threshold. Three unnamed numbers - or an implementer silently reusing a
   different one for each - would have made the three unkeyed surfaces behave
   inconsistently. Per principle 2, one knob: all three use
   `fuzzy_supersede_threshold` (0.9, provisional), and M4's tuning pass is the
   explicit point where they may diverge if the data shows they should. A
   clustering gate looser than the supersede gate would let rows cluster for
   distillation that could not supersede each other - proposing a concept from
   near-but-not-same subjects.

Violations of earlier "never" rules checked and none introduced: no new heavy
machinery (one index, one sentence, one config note; no second knob
prematurely), no status promotion, no `source` in any match, no per-call
identity, `unscoped` still never auto-included, hot rows still never deleted.

---

## v1.6 (review round 6, reviewer: opus 5/claude)

Round 6 restated R5-1..R5-17 as R6-1..R6-17 and verified all seventeen
`closed` against the v1.5 body, then filed nine new findings (R6-18..R6-26),
all folded into v1.6. Three of the carried items closed correctly but exposed
an adjacent gap - the v1.5 change was right and the thing standing next to it
had never been specified - so those were filed as new rather than reopened.
Changes and why they were necessary:

1. **Supersession gets a second pointer: `superseded_by` (§5.1, §6.2, §3.4).**
   R4-4 made a write close *every* matching row and the wire log has recorded
   `superseded` as an **array** ever since - but `memories.supersedes` stayed a
   single TEXT column. A multi-close write can only point at one parent, so
   every other closed row becomes unreachable from the current row. That is not
   cosmetic: §8 walks exactly that ancestry to decide what is worth proposing,
   so a lost branch silently undercounts the chain and the subject never
   distills. The schema could not represent the behaviour the spec mandated.
   Each closed row now records `superseded_by`, the new row's `supersedes`
   keeps a single linear spine to its most recent parent, and replay sets both
   from the array so a rebuilt index is not a quietly flatter version of the
   live one. Also pinned: multi-close is the *repair* path (fuzzy fallback over
   several unkeyed rows, an explicit `supersedes`, pre-index rows), because the
   partial unique index already limits the normal keyed path to one - which is
   precisely when a lost branch is hardest to notice.

2. **Distillation gathers a keyed subject by index, not by pointer walk
   (§5.1, §8 step 1, §16 M4).** v1.5 correctly fixed R5-8 by reaching the
   closure history, but chose the pointer walk as the access path. That path is
   both fork-blind (change 1) and unindexed: `idx_memories_key_current` is
   partial on `valid_to IS NULL`, so *no* index covered the closed rows the
   walk exists to reach, and it degrades to a scan on the hot table. Added
   `idx_memories_key_all` (deliberately covering closed rows) and made keyed
   subjects gather with one range scan on `(scope, kind, key)`; the pointer
   walk stays for unkeyed rows, which have nothing better.

3. **The shim-to-daemon connection handshake (§3.1, §10).** R5-17 specified
   how `source` reaches the daemon for *remote* callers (token map) and left
   the local half undefined - yet `--source` is a shim launch flag and the
   `session_ref` is minted by the shim, while the daemon is what stamps rows.
   Nothing in v1.5 carried either across the socket. The shim now sends one
   hello frame (`protocol_version`, `source`, `session_ref`, `scope_hint`) and
   the daemon binds it to the connection for its lifetime, so writes are
   stamped from the handshake and never from a call argument. Stated alongside
   it: the socket's trust boundary is the user account (mode 0600 in the user's
   home; a process that can open it already has the user's filesystem rights),
   so `source` is attribution and not authentication. Better to say that
   plainly than to let someone build a security decision on it later.

4. **The scope predicate is pushed into the candidate queries (§7.2).** v1.5
   scope-filtered the pools (R5-10) but did not say *where* the filter runs.
   Taking `candidate_k` from FTS and filtering afterwards lets one busy
   unrelated scope consume the pool and starve in-scope recall - the same
   starvation R3-2 fixed one layer up, failing just as silently. `candidate_k`
   now counts rows that are already in scope.

5. **`insight` versus `memory_propose` has a boundary (§6.1).** The triage
   table routes "stable facts, decisions, rules" to `memory_propose`; the new
   kind table defines `insight` as "a durable observation or conclusion". Same
   input, two destinations, no rule to choose. The natural reading proposes on
   every interesting observation and floods the curation queue - and a queue a
   human stops reading is the failure `draft_ttl_days` exists to bound, not one
   to feed. Rule stated: default to `memory_write`; reserve `memory_propose`
   for a claim the agent would stake right now; everything observed reaches the
   vault through §8 once it recurs, because recurrence *is* the evidence.

6. **`memory_propose` accepts a `key` (§10, §11).** §6.2 de-dupes concepts on
   `(scope, type, key)` and §8 writes keys into distilled frontmatter, but the
   propose surface had no `key` parameter - so agent-proposed concepts were
   structurally exempt from the exact de-dupe that distilled ones get, and fell
   through to the fuzzy path the spec spent three rounds discrediting.

7. **A key-only search is an exact lookup (§7.2, §10).** `memory_search` grew a
   `key` filter in v1.4, but with a key and no `q` there is nothing for FTS to
   rank and the behaviour was undefined against an FTS-shaped pipeline. It now
   bypasses fusion and decay and returns the current rows for that key, which
   makes it agree with `memory_get` by key by construction rather than by luck.

8. **§15 catches up with the body (config completeness).** v1.5 introduced the
   `tokens` token-to-source map and TLS in §10/§11 and neither appeared in the
   configuration table; `socket_path` and `port_path` were listed while
   `daemon.lock` and `engine.token` were not. Added `lock_path`, `token_path`,
   `http_tls_cert`/`http_tls_key`, and `tokens`. A config table that silently
   omits the security-relevant keys is worse than no table.

9. **§16 acceptance rows catch up (M2, M4, M6).** M6 still read "bearer auth;
   localhost default" after v1.5 made TLS and token-bound `source` mandatory,
   and M2 predated `session_ref` and the handshake. Acceptance criteria that
   lag the body are how a requirement gets shipped unimplemented.

10. **Smaller:** the §6.1 selection heuristic read "I/O prefer" (a typo for
    "I prefer"), and the §3.4 checkpoint example carried a stale date while the
    two write examples had been updated.

Violations of earlier "never" rules were checked and none introduced: no heavy
machinery (the second pointer is a column, not a table; no key registry), no
status promotion by agents, no `source` in the supersession match, no per-call
identity anywhere (the handshake is per-connection, the token map per-deploy),
`unscoped` still never auto-included, hot rows still never deleted.

---

## v1.5 (review round 5, reviewer: opencode/big-pickle)

Round 5 restated R4-1..R4-7 as all `closed` (verified in v1.4) and filed ten
new findings, all folded into v1.5. Changes and why they were necessary:

1. **Distillation gathers the supersede ancestry, not just current rows (§8).**
   v1.4 step 1 read "gather current `memories` rows", but step 3a qualified a
   cluster on a "supersession chain of length >= 2". Only the newest row in a
   chain is current; every closed row is invisible to a current-rows-only
   gather. Left as written, the chain condition could never fire on its own
   gathered set - the mechanism the spec leans on most (repeated rewrites make
   a thing worth proposing) was silently dead. Fixed by gathering the current
   row plus everything reachable via `supersedes`, and by defining chain length
   as the walk from the current row back to the root.

2. **Authoritative `kind` vocabulary (§6.1).** `kind` sits in the supersession
   match and the partial unique index, so a `preference` and an `insight` with
   the same `key` are permanently two subjects. Yet v1.4 never defined the four
   kinds it actually uses (§5.1 pointed at "§6.1 classes" which only described
   EPHEMERAL/EVOLVING/ENDURING; the mapping table in §8 was the only place the
   kinds appeared, unstated as a closed set). Agents inventing kinds per call
   would fragment chains the same way unnormalized keys do. Fixed with a
   definitions table plus a selection heuristic, mirrored in the
   `memory_write` tool description (the same cheap in-context place as the key
   convention).

3. **The §17 scope filter is applied to both retrieval candidate pools (§7.2).**
   §17 defines effective-scope semantics for queries, but §7.2's fusion pool
   never referenced scope. Left implicit, the easy implementation ("FTS across
   everything, filter later") would mix another project's working memories into
   this project's recall - the exact cross-contamination the scope axis exists
   to prevent. Both candidate queries now run against the caller's effective
   scope set; only `scope: "*"` bypasses.

4. **`session_ref` provenance is defined (§10).** Every row carries a
   `session_ref`, and distillation's `>= 2 distinct session_ref` rule
   (qualified-via-recurrence) depends on it, but v1.4 never said where it comes
   from. MCP stdio exposes no session id to the server, so the natural answer
   is the shim: it mints one `session_ref` at spawn and stamps every proxied
   write. Since each client spawns its MCP server per conversation, shim
   lifetime approximates session lifetime - good enough, and it makes the
   distillation rule measure what it claims to. REST gets an optional
   `session_ref`, defaulting per-connection.

5. **Key normalization collapses `.` runs (§6.3).** v1.4 collapsed `_`, `-`,
   `/`, and whitespace but not `.` itself, so `editor..theme` and
   `editor .theme` normalized to keys distinct from `editor.theme`. That
   quietly resurrects the exact drift §6.3 claims to kill, at the seams between
   agents' punctuation habits. Now any run of separators - including `.` -
   collapses to a single dot.

6. **Multi-source cluster authorship is pinned (§8).** Distillation defaults
   `author: <source> agent`, but clusters may span `source` values, so "the"
   source was undefined when two agents fed one cluster. That would be silent
   metadata corruption on every such concept. Fixed deterministically: the
   source of the most recent row in the cluster is the author. Also pinned: the
   concept body is drafted from the newest (current) row, with chain/session
   counts only as qualification signals.

7. **`memory_get` by key no longer pretends to be unique (§10, §11).** The
   subject identity is `(scope, kind, key)`, so `key` alone can match several
   current rows across kinds. v1.4 said "the current row for that subject" -
   undefined when the key exists under two kinds. The tool now returns every
   current row matching the key in the caller's effective scopes, with an
   optional `kind` to narrow to one.

8. **`source` for remote/REST callers is bound to the bearer token (§10,
   §11).** `--source` is a shim launch argument, but remote clients talk to the
   daemon directly - no shim. Left unspecified, every REST write would collapse
   to a single daemon-level source (or, worse, someone would invent a per-call
   `source` arg, reopening spoofing). Each deployed token now carries a fixed
   `source` label; attribution stays authoritative and never per-call.

9. **TLS is required for remote transport (§11, §12).** The remote path was
   "bearer token" over plaintext HTTP. A bearer token on a shared network is
   sniffable and replayable; the token only becomes a credential inside a
   tunnel. TLS (reverse proxy or native) is now explicit in both places.

10. **MCP transport terminology updated (Streamable HTTP).** v1.4 said
    "HTTP/SSE" throughout. The MCP spec's "HTTP+SSE" transport was deprecated
    in favour of **Streamable HTTP**; by 2026 the spec should name the current
    transport, not the one being phased out. Wording updated in §3.1, §10, and
    §12, with the legacy name kept in one parenthetical for anyone reading
    older MCP docs.

Violations of earlier "never" rules were checked: no new heavy machinery, no
status promotion by agents, no source in the supersession match, no per-call
identity, `unscoped` still never auto-included.

---

## v1.4 (review round 4, reviewer: opus 5/claude)

Folded R4-1..R4-7 (all closed), recorded in `docs/REVIEW.md`. Summary of the
whys, preserved here for continuity:

1. **`unscoped` quarantine bucket; `shared` restored to workspace queries
   (§17).** v1.3's coupling (unresolved writes landed in `shared`, `shared`
   excluded from queries) made misconfiguration a silent failure: writes went
   to one bucket, reads came from another, empty results forever. v1.4
   quarantines unresolved writes in `unscoped` (a signal, not a destination)
   and always auto-includes `shared` in queries, so the curated bucket stops
   being invisible. The fallback deliberately is *not* `shared`, bounding the
   blast radius of a misconfigured shim.

2. **`scope` and `key` in concept frontmatter (§4.2/§4.3), carried into the
   mirror (§5.2).** Concepts needed the same de-dupe and generalization axes
   as hot rows: `key` makes vault de-dupe exact instead of fuzzy, and editing
   `scope` to `shared` is how a human generalizes a concept to cross-project.

3. **`source` dropped from the supersession match (§6.2).** Keeping it in the
   gate manufactured the contradiction keys exist to prevent: the user switches
   to light mode in OpenCode, and Claude Code's `editor.theme = dark` is never
   closed because the source differs. Cross-agent supersession is the *point*;
   `source` stays pure attribution.

4. **Supersede-all guarded by a partial unique index (§5.1), close-before-
   insert ordering (§6.2).** Ambiguous keys close every matching current row
   (self-healing over false positives), with the invariant enforced in the
   schema so a bug creates a constraint violation instead of a silent second
   current row. Order matters: closing before inserting avoids tripping the
   index the handler is about to vacate.

5. **Key normalization and vocabulary (§6.3).** Agent-chosen keys drift
   (`editor.theme` vs `Editor_Theme` vs `theme`), quietly recreating the
   pre-key world. The engine normalizes on write and lookup; `memory_stats`
   lists the key vocabulary per scope so drift is visible the moment anyone
   looks.

6. **Key lookup on the tool surface (§10).** "What is the current value of
   `editor.theme`?" is the cheapest useful session-start question; it is an
   exact index lookup and both `memory_search` and `memory_get` now express it.

7. **`min_concept_results` floor in ranking (§7.2).** Without a floor, a
   three-slot result set could be three hot rows and no vault knowledge at all;
   curated concepts are the higher-trust tier and deserve at least one reserved
   slot whenever any qualify.

---

## v1.3 (review round 3, reviewer: opus 5/claude)

Folded R3-1..R3-8 (all closed), recorded in `docs/REVIEW.md`. The round that
moved the design from fuzzy to keyed, and from a naive daemon to the
shim/single-writer split:

- Keyed supersession replacing trigram-Dice (measured: the false positive
  scores above the true positive - no threshold can separate them).
- `candidate_k` fusion pool (fusing 3 + 3 lets decay reorder six items and
  nothing else).
- `t:"distill"` wire-log lines so `source_concept` survives reindex.
- Path-scoped engine commits (never `-a`), so a human's unsaved Obsidian work
  is never swept into an engine commit.
- Full daemon lifecycle spec: spawn race (flock), stale socket (unlink-rebind
  under the same lock), Windows loopback-TCP fallback, `idle_timeout`.
- `--source` at shim launch, keeping agent identity authoritative and
  unspoofable per-call.
- Scope slugs with basename + path hash so `~/work/api` and `~/personal/api`
  do not collide.
- Lifecycle straightening: `archived` status, sweep report into `log.md`,
  `task_state` never distills, wire log never pruned, checkpoints as full
  snapshots, hot rows never deleted.

---

*Earlier protocol notes:* the review ledger (`docs/REVIEW.md`) holds the
strictly-serial alternating critic loop, round format, and verdict taxonomy
(`closed`/`open`/`new`). This changelog complements it with rationale. Both
files are shared state between the two reviewer sessions; never edit
simultaneously.