# Memory Engine - Review Ledger (Herdr loop)

Protocol for the alternating critic round (agreed by both sessions):

- Strictly serial, always on the latest version. Only the current critic edits:
  v1.3 -> v1.4 -> v1.5 -> ...
- Each round opens with the previous round's numbered findings restated as
  open/closed ("R4-3 closed in v1.4; R4-1 open, here is why").
- The findings list is the round's deliverable, not prose.
- The human is the arbiter for unresolved findings. Unresolved items get
  flagged for the human, not debated to exhaustion.
- The vault is not a git repo; both sessions treat SPEC.md as shared state and
  never edit simultaneously.

Round format:

- Reviewer states findings R<N>-<k> against the version it read.
- Each finding lands one of:

| Verdict | Meaning |
|---|---|
| `closed` | Addressed in the next version, verified against the diff. |
| `open` | Not addressed or regressed; carries forward. |
| `new` | Fresh finding, becomes a change in the next version. |

---

## Round 3 (in: v1.2, out: v1.3) - reviewer: opus 5/claude

| ID | Finding | Verdict in v1.3 |
|---|---|---|
| R3-1 | Trigram Dice cannot separate supersession from contradiction; keyed supersession required | closed (§6.2: exact `(scope, kind, source, key)` match, fuzzy floor 0.9) |
| R3-2 | retrieve_top_k=3 per index starves RRF fusion; need candidate_k pool | closed (§7.2 candidate_k=50, truncate after decay; `last_retrieved` only for survivors) |
| R3-3 | reindex drops `source_concept`; duplicates resurrected | closed (§3.4 `t:"distill"` wire-log lines) |
| R3-4 | engine commits on promote contradict path-scoping | closed (§4.5 engine commits engine writes only; promote = index-refresh) |
| R3-5 | daemon lifecycle failure modes unspecified (spawn race, stale socket, Windows) | closed (§3.1 flock, unlink-rebind, loopback fallback, idle_timeout) |
| R3-6 | source derivation undefined but load-bearing | closed (§10/§14 `--source` launch flag) |
| R3-7 | scope slugs collide; `shared` overloaded | closed (§17 basename+hash slugs; `--scope`/`MEMBRAID_SCOPE`; `shared` excluded from default queries) |
| R3-8 | smaller: `archived` status, sweep report destination, task_state determinism, §5.1 comment, wire-log pruning, checkpoint semantics, hot rows never deleted | closed (scattered) |

## Round 4 (in: v1.3, out: v1.4) - reviewer: opus 5/claude

| ID | Finding | Verdict in v1.4 |
|---|---|---|
| R4-1 | `unscoped` quarantine bucket; `shared` restored to workspace queries; never auto-include `unscoped` | closed |
| R4-2 | `scope` and `key` in concept frontmatter, carried into mirror | closed |
| R4-3 | `source` dropped from supersession match -> `(scope, kind, key)` | closed |
| R4-4 | supersede-all guarded by partial unique index; order-insensitive lookups | closed |
| R4-5 | key normalization + key vocabulary | closed (§6.3) |
| R4-6 | key lookup on the tool surface (`memory_search`/`memory_get` by key) | closed |
| R4-7 | `min_concept_results` floor in ranking | closed |

## Round 5 (in: v1.4, out: v1.5) - reviewer: opencode/big-pickle

### R5-1..R5-7: restatement of R4 findings, verdict in v1.5

| ID | Restates | Finding | Verdict in v1.5 |
|---|---|---|---|
| R5-1 | R4-1 | `unscoped` quarantine bucket; `shared` restored to workspace queries; `unscoped` never auto-included | closed (unchanged from v1.4, re-verified) |
| R5-2 | R4-2 | `scope` + `key` in concept frontmatter, carried into mirror | closed (re-verified) |
| R5-3 | R4-3 | `source` dropped from supersession match -> `(scope, kind, key)` | closed (re-verified) |
| R5-4 | R4-4 | supersede-all guarded by partial unique index; close-before-insert ordering | closed (re-verified) |
| R5-5 | R4-5 | key normalization + key vocabulary | closed (re-verified; v1.5 also collapses `.` runs - see R5-12) |
| R5-6 | R4-6 | key lookup on the tool surface | closed (re-verified; v1.5 clarifies cross-kind behaviour - see R5-15) |
| R5-7 | R4-7 | `min_concept_results` floor in ranking | closed (re-verified) |

### R5-8..R5-17: new findings, verdict in v1.5

| ID | Finding | Verdict in v1.5 |
|---|---|---|
| R5-8 | Distillation step 1 gathers only current rows, but step 3a qualifies on chain length >= 2, which needs the closed rows; as written the chain condition can never fire | closed (§8 step 1 gathers current row + `supersedes` ancestry; chain length = walk to root) |
| R5-9 | `kind` is in the supersession match and unique index, so picking a different kind permanently splits a subject's chain - yet the four kinds are never defined as a closed set with selection guidance | closed (§6.1 authoritative kind table + heuristic, mirrored in `memory_write` tool description) |
| R5-10 | §7.2 retrieval never applies the §17 scope filter to its candidate pools; unchecked pools leak cross-project rows | closed (§7.2 both candidate queries scope-filtered to effective scope set; `scope:"*"` bypasses) |
| R5-11 | `session_ref` provenance is unspecified but load-bearing for distillation's distinct-sessions rule | closed (§10 shim mints one per spawn; REST optional, per-connection default) |
| R5-12 | Key normalization misses `.`: `editor..theme`/`editor .theme` normalize to keys distinct from `editor.theme` | closed (§6.3 collapse any run of separators incl. `.`) |
| R5-13 | Distill default `author: <source> agent` is undefined when a cluster spans sources | closed (§8 author = source of most recent row in cluster; body from newest row) |
| R5-14 | MCP transport "HTTP/SSE" is the deprecated transport; current MCP uses Streamable HTTP | closed (§3.1/§10/§12 wording updated, legacy name kept as parenthetical) |
| R5-15 | `memory_get` by key assumes uniqueness, but `(scope, kind, key)` is the subject identity; key alone can match several current rows | closed (§10/§11 return all current rows for key; optional `kind` narrows) |
| R5-16 | Remote auth is bearer-only; no transport security specified | closed (§11/§12 TLS required for remote; token over plaintext is not a boundary) |
| R5-17 | `source` derivation for remote/REST callers is undefined (no shim, so no `--source`); per-call args would reopen spoofing | closed (§10/§11 source bound to bearer token via token->source map at deploy) |

Runner-up findings weighed and deliberately dropped (noted for history, not
carried): FTS5 content-table sync (triggers vs. application-level) and backup
consistency under WAL were judged implementation details, not spec gaps; the
`kind` vocabulary now covers the "how do I choose a kind" gap; `unscoped`/
`shared` coupling was already resolved in R4.

**Round result:** 0 open, 0 for the human. SPEC.md is now v1.5; rationale in
`docs/SPEC-CHANGELOG.md`.

**Status:** complete, handed off to Round 6.

## Round 6 (in: v1.5, out: v1.6) - reviewer: opus 5/claude

### R6-1..R6-17: restatement of R5 findings, verified against the v1.5 body

| ID | Restates | Finding | Verdict in v1.5 |
|---|---|---|---|
| R6-1 | R5-1 | `unscoped` quarantine bucket; `shared` restored to workspace queries | closed (§17 intact; re-verified against the query-semantics list) |
| R6-2 | R5-2 | `scope` + `key` in concept frontmatter, carried into mirror | closed (§4.2/§4.3/§5.2) |
| R6-3 | R5-3 | `source` dropped from supersession match | closed (§6.2 step 2 and `idx_memories_key_current` both on `(scope, kind, key)`) |
| R6-4 | R5-4 | supersede-all guarded by partial unique index; close-before-insert | closed (§6.2) - but see R6-18: supersede-*all* is not representable in the schema it was given |
| R6-5 | R5-5 | key normalization + vocabulary, incl. `.` runs | closed (§6.3) |
| R6-6 | R5-6 | key lookup on the tool surface | closed (§10) - but see R6-25 (`memory_propose` still cannot set one) and R6-26 (key-only search undefined) |
| R6-7 | R5-7 | `min_concept_results` floor | closed (§7.2 step 6, §15) |
| R6-8 | R5-8 | distillation must gather the supersede ancestry, not only current rows | closed (§8 step 1) - the condition can now fire; see R6-19 for the access path it specifies |
| R6-9 | R5-9 | authoritative `kind` vocabulary with selection guidance | closed (§6.1 table + heuristic) - see R6-20 for the boundary it leaves open |
| R6-10 | R5-10 | §17 scope filter applied to both retrieval candidate pools | closed (§7.2 step 1) - see R6-21 for what `candidate_k` now has to mean |
| R6-11 | R5-11 | `session_ref` provenance defined | closed (§10) - see R6-22 for how it reaches the daemon |
| R6-12 | R5-12 | key normalization collapses `.` | closed (§6.3, with the rationale inline) |
| R6-13 | R5-13 | multi-source cluster authorship pinned | closed (§8 step 4: source of the most recent row) |
| R6-14 | R5-14 | Streamable HTTP replaces the deprecated HTTP+SSE naming | closed (§2, §3.1 diagram, §10, §12) |
| R6-15 | R5-15 | `memory_get` by key returns all matching kinds | closed (§10, §11) |
| R6-16 | R5-16 | TLS required for remote transport | closed (§11, §12) - see R6-24, the M6 acceptance row did not follow |
| R6-17 | R5-17 | remote `source` bound to bearer token | closed (§10, §11) - see R6-22, the *local* half of the same mechanism is still unstated |

All seventeen verified closed. Three of them (R6-4, R6-10, R6-17) closed
correctly but exposed an adjacent gap, filed below rather than reopened: the
v1.5 change is right, the thing next to it was never specified.

### R6-18..R6-26: new findings, verdict in v1.6

| ID | Finding | Verdict in v1.6 |
|---|---|---|
| R6-18 | `memories.supersedes` is a single TEXT column, but §6.2 closes *every* match and the wire log records `superseded` as an **array**. A multi-close write can only point at one closed parent; the others become unreachable, so §8's ancestry walk silently undercounts the chain. Schema cannot represent the behaviour the spec mandates | closed (§5.1 `superseded_by` column; §6.2 step 3 defines the primary-parent spine plus the back-pointer on every closed row; §3.4 replay sets both) |
| R6-19 | §8 step 1 reaches ancestry by walking `supersedes`, which is both fork-blind (R6-18) and unindexed: `idx_memories_key_current` is partial on `valid_to IS NULL`, so no index covers closed rows and the walk degrades to a scan | closed (§5.1 `idx_memories_key_all`; §8 step 1 gathers keyed subjects by `(scope, kind, key)` directly, pointer walk retained only for unkeyed rows) |
| R6-20 | The `insight` kind ("a durable observation or conclusion") and the ENDURING class ("stable facts ... -> `memory_propose`") describe the same input with two different destinations. An agent reading §6.1 has no rule for write-vs-propose, and the natural reading floods the draft queue | closed (§6.1 boundary paragraph: default to `memory_write`; propose only a claim the agent would stake now; observation reaches the vault through recurrence) |
| R6-21 | §7.2 scope-filters the candidate pools but does not say where: taking `candidate_k` from FTS and filtering afterwards lets a busy unrelated scope consume the pool and starve in-scope recall. Same starvation class as R3-2, one layer down | closed (§7.2 step 1: predicate pushed into each candidate query; `candidate_k` counts in-scope rows) |
| R6-22 | `--source` is a *shim launch* flag and `session_ref` is minted *by the shim*, but the daemon is what stamps rows. No mechanism carries either across the socket. R5-17 specified the remote half (token map) and left the local half undefined | closed (§3.1 connection handshake; §10 references it as the transport and states the trust boundary) |
| R6-23 | §15 omits configuration that v1.5 introduced in the body: the `tokens` token->source map, TLS cert/key, and the lock/token file paths (while `socket_path` and `port_path` are present) | closed (§15 rows added) |
| R6-24 | M2 and M6 acceptance rows predate v1.5: M6 omits TLS and token->source binding, M2 omits `session_ref` minting and the handshake | closed (§16 M2, M6) |
| R6-25 | `memory_propose` accepts no `key`, yet §6.2's uniqueness discipline de-dupes concepts on `(scope, type, key)` and §8 writes keys into distilled frontmatter. Agent-proposed concepts are structurally exempt from exact de-dupe | closed (§10, §11 accept optional `key`) |
| R6-26 | Smaller: key-only `memory_search` (no `q`) is undefined against an FTS-shaped pipeline; §6.1 heuristic reads "I/O prefer"; §3.4 checkpoint example carries a stale date | closed (§7.2/§10 exact-lookup route; typo; date) |

**Round result:** 17 carried, 17 closed, 9 new, 0 open, 0 for the human.
SPEC.md is now v1.6; rationale in `docs/SPEC-CHANGELOG.md`.

Not filed, weighed and dropped: FTS5 external-content synchronisation stays an
implementation detail (agreed in round 5 and unchanged); `session_ref`
undercounting when one client process spans several conversations is already
hedged in §10 as an approximation and does not change a decision; per-row
`confidence` still does not gate retrieval, which remains the correct call.

**Status:** handed off. Next: Round 7 (in: v1.6, out: v1.7), reviewer:
opencode/big-pickle - restate R6-1..R6-26 as R7-<k> with closed/open verdicts,
add any new findings, produce v1.7, prepend to SPEC-CHANGELOG.md. Converged
when a round produces zero new findings and no open carries.

## Round 7 (in: v1.6, out: v1.7) - reviewer: opencode/big-pickle

Round 6's own handoff flagged that three of its fixes had each exposed an
"adjacent gap" around themselves, and asked this round to check whether v1.6
had done the same. It had - four times over, each a small yes - and all four
are filed below as new rather than re-opened.

### R7-1..R7-26: restatement of R6 findings, verdict in v1.7

| ID | Restates | Finding | Verdict in v1.7 |
|---|---|---|---|
| R7-1 | R6-1 | `unscoped` quarantine; `shared` auto-included in queries | closed (§17 intact) |
| R7-2 | R6-2 | `scope` + `key` frontmatter and mirror columns | closed (§4.2/§4.3/§5.2) |
| R7-3 | R6-3 | `source` out of the supersession match | closed (§6.2, index on `(scope, kind, key)`) |
| R7-4 | R6-4 | supersede-all + partial unique index + close-before-insert | closed (§6.2; schema now represents it via two pointers - R6-18) |
| R7-5 | R6-5 | key normalization + vocabulary incl. `.` | closed (§6.3) |
| R7-6 | R6-6 | key lookup on the tool surface | closed (§7.2/§10; exact-lookup bookkeeping now pinned - R7-28) |
| R7-7 | R6-7 | `min_concept_results` floor | closed (§7.2/§15) |
| R7-8 | R6-8 | distillation gathers the supersede ancestry | closed (§8 step 1) |
| R7-9 | R6-9 | authoritative `kind` vocabulary | closed (§6.1) |
| R7-10 | R6-10 | scope predicate pushed into candidate queries | closed (§7.2) |
| R7-11 | R6-11 | `session_ref` provenance | closed (§10) |
| R7-12 | R6-12 | dot-run key collapsing | closed (§6.3) |
| R7-13 | R6-13 | multi-source cluster authorship | closed (§8 step 4) |
| R7-14 | R6-14 | Streamable HTTP naming | closed (§2/§3.1/§10/§12) |
| R7-15 | R6-15 | `memory_get` by key across kinds | closed (§10/§11) |
| R7-16 | R6-16 | TLS required for remote | closed (§11/§12/§15) |
| R7-17 | R6-17 | remote `source` bound to bearer token | closed (§10/§11/§15 `tokens`) |
| R7-18 | R6-18 | two-pointer supersession (`superseded_by`) | closed (§5.1/§6.2/§3.4; header and M1 acceptance carry it) |
| R7-19 | R6-19 | keyed ancestry via `idx_memories_key_all` | closed (§5.1/§8/§16 M4) - the unkeyed half of the same walk had no index, filed as R7-27 |
| R7-20 | R6-20 | `insight` vs `memory_propose` boundary | closed (§6.1) |
| R7-21 | R6-21 | candidate_k counts in-scope rows | closed (§7.2) |
| R7-22 | R6-22 | connection handshake (source/session_ref) | closed (§3.1/§10) - `scope_hint`'s composition with the ladder was undefined, filed as R7-29 |
| R7-23 | R6-23 | config table completeness | closed (§15 lock/token/TLS/tokens) |
| R7-24 | R6-24 | M2/M4/M6 acceptance sync | closed (§16) - M1/M4 again touched this round (R7-27/R7-30) |
| R7-25 | R6-25 | `memory_propose` accepts `key` | closed (§10/§11) |
| R7-26 | R6-26 | key-only search is exact lookup; two typos | closed (§7.2/§6.1/§3.4) - the exact path's `last_retrieved` semantics were undefined, filed as R7-28 |

All twenty-six verified closed against the v1.6 body.

### R7-27..R7-30: new findings, verdict in v1.7

| ID | Finding | Verdict in v1.7 |
|---|---|---|
| R7-27 | The unkeyed ancestry walk's child lookup (`WHERE superseded_by = ?`) has no index; the keyed path got `idx_memories_key_all` in v1.6, the unkeyed half of the same walk was left to scan the hot table at every distill pass - and an implementer trimming for maintainability could drop the fork-enumeration and quietly regress R6-18 | closed (§5.1 `idx_memories_superseded_by` partial index; §8 step 1; §16 M1/M4) |
| R7-28 | The exact-lookup path (key-only search, `memory_get` by key) is undefined against the survivor rule: does an exact hit refresh `last_retrieved`? Left unresolved, an actively-queried subject gets a false "never used" flag and decays while being read every session | closed (§7.2: intentional exact hits refresh, incidental fusion echoes do not) |
| R7-29 | The round-6 handshake carries `scope_hint` but never says what the shim puts in it or how it composes with the §17 ladder, which straddles both processes (steps 2/4 shim-side, 1/3/5 daemon-side) | closed (§3.1 shim resolves flag/env then cwd into the hint; §17 daemon resolves 1, hint, 3, 5 - noting the configured default is only reachable on shim-less connections) |
| R7-30 | Unkeyed distillation clustering and concept near-duplicate detection say "Dice-over-trigrams similarity" with no threshold, while supersession has had one since v1.3; three unnamed numbers (or three silently different ones) would let rows cluster that could not supersede each other | closed (§8 step 2, §9 item 4, §15: all unkeyed surfaces share `fuzzy_supersede_threshold`, provisional, split only if M4 tuning shows cause) |

Weighed and deliberately dropped: the `concepts` DDL `status` default ('draft')
vs the frontmatter default ('stable') is cosmetic - the mirror always reads
status from frontmatter - and raising it would be noise; "most recent of them"
in §6.2/§3.4 is determinable on replay (the array ids resolve to rows already
replayed, valid_from in hand), so no wire-log change needed; two stable
concepts sharing `(scope, type, key)` surfacing together is the intended
sweep-merge flow, not a bug.

**Round result:** 26 carried, 26 closed, 4 new, 0 open, 0 for the human.
SPEC.md is now v1.7; rationale in `docs/SPEC-CHANGELOG.md`.

**Status:** complete, handed off to Round 8.


## Round 8 (in: v1.7, out: v1.8) - reviewer: opus 5/claude

Round 7 asked whether v1.7's own changes left adjacent gaps. Two did, in the
same place: the `scope_hint` composition (R7-29) both contradicts the ladder it
implements and leaves the `unscoped` query case inverted. The other two new
findings are adjacency of a different kind - R7-28 defined the refresh rule for
one of three read paths, and the exact-lookup path R6-26 built has only ever
been defined over one of the two tables that carry a `key`. Three findings are
older than this round's changes and were simply never asked: nothing in the
spec versions the wire protocol, the schema, or the log format, on a daemon
explicitly designed to outlive the binary that spawned it.

### R8-1..R8-30: restatement of R7 findings, verified against the v1.7 body

| ID | Restates | Finding | Verdict in v1.7 |
|---|---|---|---|
| R8-1 | R7-1 | `unscoped` quarantine; `shared` auto-included | closed (§17) - but see R8-32, "auto-included" inverts when the caller's own scope *is* `unscoped` |
| R8-2 | R7-2 | `scope` + `key` frontmatter and mirror columns | closed (§4.2/§4.3/§5.2) |
| R8-3 | R7-3 | `source` out of the supersession match | closed (§6.2, index on `(scope, kind, key)`) |
| R8-4 | R7-4 | supersede-all + partial unique index + close-before-insert | closed (§6.2) |
| R8-5 | R7-5 | key normalization + vocabulary incl. `.` | closed (§6.3) |
| R8-6 | R7-6 | key lookup on the tool surface | closed (§7.2/§10) - but see R8-34: the path is defined over `memories` only, though `concepts` carries `key` too |
| R8-7 | R7-7 | `min_concept_results` floor | closed (§7.2/§15) |
| R8-8 | R7-8 | distillation gathers the supersede ancestry | closed (§8 step 1) |
| R8-9 | R7-9 | authoritative `kind` vocabulary | closed (§6.1) |
| R8-10 | R7-10 | scope predicate pushed into candidate queries | closed (§7.2 step 1) |
| R8-11 | R7-11 | `session_ref` provenance | closed (§10) |
| R8-12 | R7-12 | dot-run key collapsing | closed (§6.3) |
| R8-13 | R7-13 | multi-source cluster authorship | closed (§8 step 4) |
| R8-14 | R7-14 | Streamable HTTP naming | closed (§2/§3.1/§10/§12) |
| R8-15 | R7-15 | `memory_get` by key across kinds | closed (§10/§11) |
| R8-16 | R7-16 | TLS required for remote | closed (§11/§12/§15) |
| R8-17 | R7-17 | remote `source` bound to bearer token | closed (§10/§11/§15) |
| R8-18 | R7-18 | two-pointer supersession (`superseded_by`) | closed (§5.1/§6.2/§3.4/§16 M1) |
| R8-19 | R7-19 | keyed ancestry via `idx_memories_key_all` | closed (§5.1/§8/§16 M4) |
| R8-20 | R7-20 | `insight` vs `memory_propose` boundary | closed (§6.1) |
| R8-21 | R7-21 | candidate_k counts in-scope rows | closed (§7.2 step 1) |
| R8-22 | R7-22 | connection handshake (source/session_ref) | closed (§3.1/§10) - `protocol_version` rides in the frame and is specified nowhere, filed as R8-35 |
| R8-23 | R7-23 | config table completeness | closed (§15) |
| R8-24 | R7-24 | M2/M4/M6 acceptance sync | closed (§16) |
| R8-25 | R7-25 | `memory_propose` accepts `key` | closed (§10/§11) |
| R8-26 | R7-26 | key-only search is exact lookup; typos | closed (§7.2/§6.1/§3.4) |
| R8-27 | R7-27 | unkeyed ancestry child lookup indexed | closed (§5.1 `idx_memories_superseded_by`; §8 step 1; §16 M1/M4) |
| R8-28 | R7-28 | exact hits refresh `last_retrieved`, fusion echoes do not | closed (§7.2) - covers two of the three read paths; `memory_get` by id/path and maintenance reads are still undefined, filed as R8-33 |
| R8-29 | R7-29 | `scope_hint` composes with the §17 ladder | closed (§3.1/§17) - but the composition it specifies is *not* the ladder's stated order, filed as R8-31 |
| R8-30 | R7-30 | one `fuzzy_supersede_threshold` for all unkeyed surfaces | closed (§6.2/§8 step 2/§9 item 4/§15/§16 M4) |

All thirty verified closed against the v1.7 body.

### R8-31..R8-37: new findings, verdict in v1.8

| ID | Finding | Verdict in v1.8 |
|---|---|---|
| R8-31 | §17's ladder is numbered 1 explicit, 2 shim pin, 3 daemon default, 4 shim cwd, but §3.1 and §17's own composition paragraph execute 1, hint (2 then 4), 3, 5 - the daemon default and the cwd slug swap places for every shim connection. v1.7 noticed the consequence (it admits step 3 is unreachable behind a shim) without noticing that the numbering is what an implementer codes from. A global default outranking a specific cwd was the wrong precedence anyway | closed (§17 ladder renumbered so written order equals executed order: explicit, shim pin, shim cwd, daemon default, `unscoped`; §3.1 composition updated; the "unreachable step 3" caveat is gone because it no longer exists) |
| R8-32 | §17 says an absent `scope` means "the caller's own scope, plus `shared`" and, four lines later, that "`unscoped` is **never** auto-included". When the ladder falls through, the caller's own scope *is* `unscoped`, so the default query auto-includes the quarantine bucket - for precisely the misconfigured connections it exists to isolate | closed (§17: a connection resolved to `unscoped` defaults to `shared` only; its writes stay reachable by explicit `scope: unscoped` triage) |
| R8-33 | The refresh rule is defined for fusion survivors and exact *key* hits. `memory_get` by id or path is equally intentional and unstated; worse, nothing exempts diagnostic reads, so `memory_stats`, sweep enumeration and `scope: "*"` triage would refresh the very rows they are reporting as stale - the report resetting the clock it just measured | closed (§7.2: three named paths - fusion survivors refresh, all exact lookups refresh, maintenance/diagnostic reads never do) |
| R8-34 | `key` is a column on `concepts` as well as `memories` (§5.2, indexed), but the exact-lookup path is written entirely in terms of hot rows. "What is the current value of `editor.theme`?" returns the working memory and hides the curated concept for the same subject - the exact inversion `min_concept_results` exists to prevent on the fusion path, reappearing on the path that skips fusion | closed (§7.2, §10, §11: exact lookups span both tables, concepts first) |
| R8-35 | `protocol_version` appears exactly once in the spec, inside the hello frame, and is specified nowhere. With `idle_timeout: 0` the daemon is *designed* to outlive every client, including the binary upgrade that replaces it on disk, so a new shim meeting an old resident daemon is the normal upgrade path, not an edge case | closed (§3.1: mismatch refused, shim requests shutdown and respawns from the newer binary; rationale for not negotiating) |
| R8-36 | Nothing versions the on-disk formats. The schema has changed in four of the last five rounds; the wire log carries a `v` field with no replay rule. A daemon opening an index written by a different binary, or replaying a line format it does not know, has no specified behaviour - on a design whose single guarantee is one writer | closed (new §5.3: `PRAGMA user_version`, rebuild-by-reindex as the migration path, refuse-on-newer, wire-log `v` accepted-or-refused never guessed) |
| R8-37 | Smaller: `*` is a query sentinel but nothing rejects it on write, so a literal `*` bucket is reachable; `valid_from` and `created_at` are identical for every row the engine writes, with no stated invariant, while decay reads one and the temporal model the other | closed (§17 ladder step 1; §5.1 comment) |

**Round result:** 30 carried, 30 closed, 7 new, 0 open, 0 for the human.
SPEC.md is now v1.8; rationale in `docs/SPEC-CHANGELOG.md`.

Weighed and deliberately dropped: `idx_memories_key_all` overlapping
`idx_memories_key_current` is justified (one enforces the constraint, one
covers closed rows) and not redundancy; §8 step 1 gathering ancestry per-row
before step 2 clusters is the only coherent reading and needs no sentence;
FTS5 external-content synchronisation remains an implementation detail, as
agreed in rounds 5 and 7.

**Status:** handed off. Next: Round 9 (in: v1.8, out: v1.9), reviewer:
opencode/big-pickle - restate R8-1..R8-37 as R9-<k> with closed/open verdicts,
add any new findings, produce v1.9, prepend to SPEC-CHANGELOG.md. Converged
when a round produces zero new findings and no open carries.

## Round 9 (in: v1.8, out: v1.9) - reviewer: opencode/big-pickle

Round 8's handoff asked this round to point at "what the spec assumes is never
true, that time or operations will eventually make true" - the upgrade question
that produced R8-35/36. Three of this round's four new findings answer exactly
that: a scheduled run that has no caller to derive a scope from, a rebuild that
fires on every upgrade, and a daemon that refuses to start rather than a daemon
that binds within the retry window. The fourth is a wording adjacency left by
v1.8's own renumber.

### R9-1..R9-37: restatement of R8 findings, verdict in v1.9

| ID | Restates | Finding | Verdict in v1.9 |
|---|---|---|---|
| R9-1 | R8-1 | `unscoped` quarantine; `shared` auto-included | closed (§17, incl. the unscoped-connection exception from R8-32) |
| R9-2 | R8-2 | `scope` + `key` frontmatter and mirror columns | closed (§4.2/§4.3/§5.2) |
| R9-3 | R8-3 | `source` out of the supersession match | closed (§6.2, index on `(scope, kind, key)`) |
| R9-4 | R8-4 | supersede-all + partial unique index + close-before-insert | closed (§6.2) |
| R9-5 | R8-5 | key normalization + vocabulary incl. `.` | closed (§6.3) |
| R9-6 | R8-6 | key lookup on the tool surface | closed (§7.2/§10) |
| R9-7 | R8-7 | `min_concept_results` floor | closed (§7.2/§15) |
| R9-8 | R8-8 | distillation gathers the supersede ancestry | closed (§8 step 1) |
| R9-9 | R8-9 | authoritative `kind` vocabulary | closed (§6.1) |
| R9-10 | R8-10 | scope predicate pushed into candidate queries | closed (§7.2 step 1) |
| R9-11 | R8-11 | `session_ref` provenance | closed (§10) |
| R9-12 | R8-12 | dot-run key collapsing | closed (§6.3) |
| R9-13 | R8-13 | multi-source cluster authorship | closed (§8 step 4) |
| R9-14 | R8-14 | Streamable HTTP naming | closed (§2/§3.1/§10/§12) |
| R9-15 | R8-15 | `memory_get` by key across kinds | closed (§10/§11) |
| R9-16 | R8-16 | TLS required for remote | closed (§11/§12/§15) |
| R9-17 | R8-17 | remote `source` bound to bearer token | closed (§10/§11/§15) |
| R9-18 | R8-18 | two-pointer supersession (`superseded_by`) | closed (§5.1/§6.2/§3.4/§16 M1) |
| R9-19 | R8-19 | keyed ancestry via `idx_memories_key_all` | closed (§5.1/§8/§16 M4) |
| R9-20 | R8-20 | `insight` vs `memory_propose` boundary | closed (§6.1) |
| R9-21 | R8-21 | candidate_k counts in-scope rows | closed (§7.2 step 1) |
| R9-22 | R8-22 | connection handshake (source/session_ref) | closed (§3.1/§10) |
| R9-23 | R8-23 | config table completeness | closed (§15) |
| R9-24 | R8-24 | M2/M4/M6 acceptance sync | closed (§16) |
| R9-25 | R8-25 | `memory_propose` accepts `key` | closed (§10/§11) |
| R9-26 | R8-26 | key-only search is exact lookup; typos | closed (§7.2/§6.1/§3.4) |
| R9-27 | R8-27 | unkeyed ancestry child lookup indexed | closed (§5.1 `idx_memories_superseded_by`; §8 step 1; §16 M1/M4) |
| R9-28 | R8-28 | three read paths named for the refresh rule | closed (§7.2 table) - distill's enumeration now named too, R9-39 |
| R9-29 | R8-29 | `scope_hint` composes with the §17 ladder | closed (§3.1/§17, new numbering) |
| R9-30 | R8-30 | one `fuzzy_supersede_threshold` for all unkeyed surfaces | closed (§6.2/§8/§9/§15/§16 M4) |
| R9-31 | R8-31 | §17 ladder written order equals executed order | closed (§17 steps, §3.1, §16 M0) |
| R9-32 | R8-32 | unscoped connection defaults to `shared` only | closed (§17 query semantics) |
| R9-33 | R8-33 | all three read paths refreshed or exempt, named | closed (§7.2 table) - maintenance exemption now covers distill too, R9-39 |
| R9-34 | R8-34 | exact lookups span `concepts` and `memories` | closed (§7.2/§10/§11) |
| R9-35 | R8-35 | `protocol_version` mismatch rule | closed (§3.1) - the shim-side refusal handling is new, R9-40 |
| R9-36 | R8-36 | schema and wire-log versions (§5.3) | closed (§5.3) - the reindex it triggers is now routine, and its concepts-side cost is fixed by R9-38 |
| R9-37 | R8-37 | `*` rejected on write; `valid_from`/`created_at` invariant | closed (§17 step 1, §5.1 comment) |

All thirty-seven verified closed against the v1.8 body.

### R9-38..R9-41: new findings, verdict in v1.9

| ID | Finding | Verdict in v1.9 |
|---|---|---|
| R9-38 | `concepts.last_retrieved` is reset by every `reindex`: the mirror is rebuilt wholesale from the vault, and the checkpoint mechanism that guarantees a rebuilt index "does not look never-retrieved" (§3.4) addresses hot rows by id only. Latent since the mirror gained a `last_retrieved` column; §5.3 turned the rebuild into the routine upgrade path, so the whole curated tier's decay doubles for weeks after every upgrade - while §3.4's guarantee silently held for hot rows only | closed (§3.4 checkpoint now carries a path-keyed `concepts` snapshot in the same full-snapshot line; §5.2 states the rebuild restores retrieval state from the checkpoint, not the vault; §16 M1/M5) |
| R9-39 | `memory_distill` and `memory_sweep` take "optional `scope`" but no envelope is defined: §17's query semantics are written for read paths, and a *scheduled* pass has no caller to derive a scope from at all. The most plausible reading - wire `scope` to the read rule, absent = own-plus-`shared` - would starve the sweep report's quarantine-size line of every other bucket and silently diverge scheduled from bare passes; the maintenance envelope is the only coherent one | closed (§17 canonical: a pass covers every bucket, `unscoped` included, unless an explicit `scope` narrows it; §8/§9 triggers and §10 tool rows reference it; §7.2 table now names distill's enumeration as a never-refreshing read) |
| R9-40 | The §3.1 failure modes presuppose a daemon that binds within the spawn-race retry window, but the design has startup refusals that bind nothing: a downgraded binary refusing a newer schema (§5.3) and config errors. A refused daemon left the shim retrying into silence - indistinguishable from the hang this section exists to kill | closed (§3.1: the shim waits for the socket or the daemon's exit, surfaces the refusal reason once, fails the connection; shim never unlinks the socket; §16 M0) |
| R9-41 | Smaller: §15's `scope` row still said "default scope ... (remote mode, or unresolved cwd)" - language written against v1.7's ladder, where the renumber no longer exposes "unresolved cwd" on the daemon side at all (a shim always sends a hint) | closed (§15 row names step 4, who reaches it, and the fall-through to `unscoped`) |

Weighed and deliberately dropped: hot rows exempted from sweep item 1 by a
`source_concept` link to a concept that is later deprecated or archived - the
exemption exists to save distilled rows from being mislabeled after a rebuild,
and a deprecated *concept* does not make its source rows false; those rows age
through decay exactly like any other current row, and treating the link as
expired when the concept's status changes would auto-archive rows whose
successor chain is still live, which is a policy decision for M-later, not a
missing sentence; an explicit `supersedes` pointing at a row that does not
vacate the `(scope, kind, key)` unique index - the partial unique index raises
on the contradictory insert, which is exactly its designed self-healing; the
shimmed path makes `unscoped` nearly unreachable (a shim always sends a hint) -
the bucket remains reachable for shim-less callers and for shims whose
resolution fails, so it stays the rare-and-loud signal R4 designed rather than
dead code.

**Round result:** 37 carried, 37 closed, 4 new, 0 open, 0 for the human.
SPEC.md is now v1.9; rationale in `docs/SPEC-CHANGELOG.md`.

**Status:** complete, handed off to Round 10.


## Round 10 (in: v1.9, out: v1.10) - reviewer: opus 5/claude

Round 9's handoff pointed at three adjacency checks (the new checkpoint format,
the distill/sweep envelope, the version-skew pair on Windows) and kept round
8's "what does the spec assume is never true" lens. All three checks came back
positive, and the lens produced two more. The five findings share a single
shape this time: every one of them is the spec's own words being true of one
case and quietly assumed of another - one process, one endpoint kind, one
ordering, one path that never moves, one clock that only moves forward.

### R10-1..R10-41: restatement of R9 findings, verified against the v1.9 body

| ID | Restates | Finding | Verdict in v1.9 |
|---|---|---|---|
| R10-1 | R9-1 | `unscoped` quarantine; `shared` auto-included, incl. the unscoped-connection exception | closed (§17) |
| R10-2 | R9-2 | `scope` + `key` frontmatter and mirror columns | closed (§4.2/§4.3/§5.2) |
| R10-3 | R9-3 | `source` out of the supersession match | closed (§6.2) |
| R10-4 | R9-4 | supersede-all + partial unique index + close-before-insert | closed (§6.2) - the same species of ordering rule is now stated for the log/index pair, R10-43 |
| R10-5 | R9-5 | key normalization + vocabulary incl. `.` | closed (§6.3) |
| R10-6 | R9-6 | key lookup on the tool surface | closed (§7.2/§10) |
| R10-7 | R9-7 | `min_concept_results` floor | closed (§7.2/§15) |
| R10-8 | R9-8 | distillation gathers the supersede ancestry | closed (§8 step 1) |
| R10-9 | R9-9 | authoritative `kind` vocabulary | closed (§6.1) |
| R10-10 | R9-10 | scope predicate pushed into candidate queries | closed (§7.2 step 1) |
| R10-11 | R9-11 | `session_ref` provenance | closed (§10) |
| R10-12 | R9-12 | dot-run key collapsing | closed (§6.3) |
| R10-13 | R9-13 | multi-source cluster authorship | closed (§8 step 4) |
| R10-14 | R9-14 | Streamable HTTP naming | closed (§2/§3.1/§10/§12) |
| R10-15 | R9-15 | `memory_get` by key across kinds | closed (§10/§11) |
| R10-16 | R9-16 | TLS required for remote | closed (§11/§12/§15) |
| R10-17 | R9-17 | remote `source` bound to bearer token | closed (§10/§11/§15) - the *local* token's exposure on the Windows path is new, R10-45 |
| R10-18 | R9-18 | two-pointer supersession (`superseded_by`) | closed (§5.1/§6.2/§3.4/§16 M1) |
| R10-19 | R9-19 | keyed ancestry via `idx_memories_key_all` | closed (§5.1/§8/§16 M4) |
| R10-20 | R9-20 | `insight` vs `memory_propose` boundary | closed (§6.1) |
| R10-21 | R9-21 | candidate_k counts in-scope rows | closed (§7.2 step 1) |
| R10-22 | R9-22 | connection handshake (source/session_ref) | closed (§3.1/§10) |
| R10-23 | R9-23 | config table completeness | closed (§15) |
| R10-24 | R9-24 | M2/M4/M6 acceptance sync | closed (§16) |
| R10-25 | R9-25 | `memory_propose` accepts `key` | closed (§10/§11) |
| R10-26 | R9-26 | key-only search is exact lookup; typos | closed (§7.2/§6.1/§3.4) |
| R10-27 | R9-27 | unkeyed ancestry child lookup indexed | closed (§5.1/§8/§16 M1/M4) |
| R10-28 | R9-28 | three read paths named for the refresh rule | closed (§7.2 table, distill's enumeration included) |
| R10-29 | R9-29 | `scope_hint` composes with the §17 ladder | closed (§3.1/§17) |
| R10-30 | R9-30 | one `fuzzy_supersede_threshold` for all unkeyed surfaces | closed (§6.2/§8/§9/§15/§16 M4) |
| R10-31 | R9-31 | §17 ladder written order equals executed order | closed (§17/§3.1/§16 M0) |
| R10-32 | R9-32 | unscoped connection defaults to `shared` only | closed (§17) |
| R10-33 | R9-33 | maintenance/diagnostic reads never refresh | closed (§7.2 table) |
| R10-34 | R9-34 | exact lookups span `concepts` and `memories` | closed (§7.2/§10/§11) |
| R10-35 | R9-35 | `protocol_version` mismatch rule | closed (§3.1) - unix-shaped; the Windows endpoint is not covered, R10-45 |
| R10-36 | R9-36 | schema and wire-log versions (§5.3) | closed (§5.3) - the routine rebuild it creates exposes R10-43 and R10-44 |
| R10-37 | R9-37 | `*` rejected on write; `valid_from`/`created_at` invariant | closed (§17 step 1, §5.1) |
| R10-38 | R9-38 | checkpoint covers the concepts mirror's retrieval state | closed (§3.4 `concepts` array; §5.2 carve-out; §16 M1/M5) - fully cross-referenced. Path-keying is fragile, R10-44 |
| R10-39 | R9-39 | distill/sweep maintenance envelope (full index unless narrowed) | closed (§17/§8/§9/§10/§7.2) - the envelope's interaction with the concurrent timers is new, R10-42 |
| R10-40 | R9-40 | spawned daemon that refuses to start is surfaced, not retried into a hang | closed (§3.1/§16 M0) - stated in unix-socket terms only, R10-45 |
| R10-41 | R9-41 | §15 `scope` row describes the current ladder | closed (§15) |

All forty-one verified closed against the v1.9 body, with no corrections
needed. R10-38 was initially marked as missing its §5.2 cross-reference; on
re-check §5.2 already carries it ("wholesale *from the vault*; its
`last_retrieved` column is machine bookkeeping the vault does not hold, and a
rebuild restores it from the latest checkpoint line"), so the item is closed
outright and the §5.2 half of R10-46 was withdrawn before v1.10 was written.

### R10-42..R10-46: new findings, verdict in v1.10

| ID | Finding | Verdict in v1.10 |
|---|---|---|
| R10-42 | "One writer" is true of the process and was never made true inside it. §13 puts MCP handling, the REST server, the distill timer, the sweep timer and (M9) the watcher in concurrent goroutines; §3.3 adds an hourly wire-log commit. Four of those mutate the vault and run `git`, and `index.lock` collides between goroutines exactly as between processes - the bug class §3.1 exists to kill, one level down. Not a rare interleaving either: `sweep_every` (168h) is an exact multiple of `distill_every` (30m), so every sweep fires on an instant a distill is also due, and R9-39 just widened both to the full index | closed (§3.1 maintenance lock: all vault-mutating and git-running work serialized, scheduled jobs skip rather than queue, read paths never take it; §13 and §16 M3 follow) |
| R10-43 | Nothing orders the wire-log append against the SQLite commit, and §3.3's whole durability claim rests on the log being a superset of the index. Write the index first and a crash in between leaves a row with no line - which survives until §5.3's routine rebuild-on-upgrade silently deletes it. The failure is invisible at the moment it happens and arrives weeks later as data that was never logged | closed (§3.3: log-first, flush, then commit; the crash window heals on replay; named as the same species as close-before-insert) |
| R10-44 | The concepts checkpoint R9-38 added is keyed by `path`, which is the one attribute §4.1 actively invites a human to change ("folders carry human organization... a person navigates by folder"). Moving `rules/foo.md` into `decisions/` is ordinary curation and silently resets that concept's decay at the next rebuild - reintroducing, for reorganized concepts, exactly the loss R9-38 closed for all of them | closed (§3.4 checkpoint entries carry `key` beside `path`; restore matches path then key within scope; keyless-and-renamed is the stated bounded residue, and sweep's own archival moves are safe because the checkpoint is written after the run) |
| R10-45 | §3.1 says the Windows loopback path is "identical", and that blanket hides the one real difference: a unix socket path cannot be taken over by an unrelated program, but a TCP port can be recycled. After a crash, the port in a stale port file may belong to something else, and the shim's hello frame would hand `engine.token` to an arbitrary local process. R9-40's refusal handling also waits on "the socket", which does not exist here | closed (§3.1 Windows bullet: port file carries pid + nonce, daemon speaks first echoing the nonce, shim sends nothing until the echo matches; stale/dead/wrong is treated as a stale endpoint and respawned; R9-40's wait clause generalized to "endpoint or exit"; §16 M0) |
| R10-46 | Smaller: `unused_days` has no lower bound, so a backward clock step (an NTP correction, a resumed VM) makes the decay term negative and the multiplier greater than one - boosting precisely the rows decay exists to suppress. The spec assumes a clock that only moves forward | closed (§7.2 `unused_days` floored at zero) |

**Round result:** 41 carried, 41 closed, 5 new, 0 open, 0 for the human.
SPEC.md is now v1.10; rationale in `docs/SPEC-CHANGELOG.md`.

Weighed and deliberately dropped: distillation's full-index envelope means a
session in project X can produce vault drafts and commits for project Y - a
behaviour consequence of R9-39, not a defect, and the serialization in R10-42
is what makes it safe; a rebuild triggered before any checkpoint has ever been
written leaves everything never-retrieved, which is the same best-effort
already stated in §3.4 and not a new gap; `concepts.path` as PRIMARY KEY
making any rename a delete+insert is the mirror behaving as designed, and
R10-44 addresses the only consequence that loses state.

**Status:** handed off. Next: Round 11 (in: v1.10, out: v1.11), reviewer:
opencode/big-pickle - restate R10-1..R10-46 as R11-<k> with closed/open
verdicts, add any new findings, produce v1.11, prepend to SPEC-CHANGELOG.md.
Converged when a round produces zero new findings and no open carries.

## Round 11 (in: v1.10, out: v1.11) - reviewer: opencode/big-pickle

Round 10's handoff named the four v1.10 constraints as this round's adjacency
targets - the maintenance lock R10-42, log-first R10-43, the path+key
checkpoint R10-44, and the `*` parameter rejection R10-37 - and asked whether
each was made true only for one case. All four came back positive, each with a
second case standing next to it: a second writer, a missing field, a second
ordering, a second door.

### R11-1..R11-46: restatement of R10 findings, verdict in v1.11

| ID | Restates | Finding | Verdict in v1.11 |
|---|---|---|---|
| R11-1 | R10-1 | `unscoped` quarantine; `shared` auto-included, incl. the unscoped-connection exception | closed (§17) |
| R11-2 | R10-2 | `scope` + `key` frontmatter and mirror columns | closed (§4.2/§4.3/§5.2) |
| R11-3 | R10-3 | `source` out of the supersession match | closed (§6.2) |
| R11-4 | R10-4 | supersede-all + partial unique index + close-before-insert | closed (§6.2) - the same species of ordering rule is now stated for distill's two writes, R11-49 |
| R11-5 | R10-5 | key normalization + vocabulary incl. `.` | closed (§6.3) |
| R11-6 | R10-6 | key lookup on the tool surface | closed (§7.2/§10) |
| R11-7 | R10-7 | `min_concept_results` floor | closed (§7.2/§15) |
| R11-8 | R10-8 | distillation gathers the supersede ancestry | closed (§8 step 1) |
| R11-9 | R10-9 | authoritative `kind` vocabulary | closed (§6.1) |
| R11-10 | R10-10 | scope predicate pushed into candidate queries | closed (§7.2 step 1) |
| R11-11 | R10-11 | `session_ref` provenance | closed (§10) |
| R11-12 | R10-12 | dot-run key collapsing | closed (§6.3) |
| R11-13 | R10-13 | multi-source cluster authorship | closed (§8 step 4) |
| R11-14 | R10-14 | Streamable HTTP naming | closed (§2/§3.1/§10/§12) |
| R11-15 | R10-15 | `memory_get` by key across kinds | closed (§10/§11) |
| R11-16 | R10-16 | TLS required for remote | closed (§11/§12/§15) |
| R11-17 | R10-17 | remote `source` bound to bearer token | closed (§10/§11/§15) |
| R11-18 | R10-18 | two-pointer supersession (`superseded_by`) | closed (§5.1/§6.2/§3.4/§16 M1) |
| R11-19 | R10-19 | keyed ancestry via `idx_memories_key_all` | closed (§5.1/§8/§16 M4) |
| R11-20 | R10-20 | `insight` vs `memory_propose` boundary | closed (§6.1) |
| R11-21 | R10-21 | candidate_k counts in-scope rows | closed (§7.2 step 1) |
| R11-22 | R10-22 | connection handshake (source/session_ref) | closed (§3.1/§10) |
| R11-23 | R10-23 | config table completeness | closed (§15) |
| R11-24 | R10-24 | M2/M4/M6 acceptance sync | closed (§16) |
| R11-25 | R10-25 | `memory_propose` accepts `key` | closed (§10/§11) |
| R11-26 | R10-26 | key-only search is exact lookup; typos | closed (§7.2/§6.1/§3.4) |
| R11-27 | R10-27 | unkeyed ancestry child lookup indexed | closed (§5.1/§8/§16 M1/M4) |
| R11-28 | R10-28 | three read paths named for the refresh rule | closed (§7.2 table) |
| R11-29 | R10-29 | `scope_hint` composes with the §17 ladder | closed (§3.1/§17) |
| R11-30 | R10-30 | one `fuzzy_supersede_threshold` for all unkeyed surfaces | closed (§6.2/§8/§9/§15/§16 M4) |
| R11-31 | R10-31 | §17 ladder written order equals executed order | closed (§17/§3.1/§16 M0) |
| R11-32 | R10-32 | unscoped connection defaults to `shared` only | closed (§17) |
| R11-33 | R10-33 | maintenance/diagnostic reads never refresh | closed (§7.2 table) |
| R11-34 | R10-34 | exact lookups span `concepts` and `memories` | closed (§7.2/§10/§11) |
| R11-35 | R10-35 | `protocol_version` mismatch rule | closed (§3.1) |
| R11-36 | R10-36 | schema and wire-log versions (§5.3) | closed (§5.3) - the rebuild it makes routine is itself the one unguarded writer, R11-47 |
| R11-37 | R10-37 | `*` rejected on write; `valid_from`/`created_at` invariant | closed (§17 step 1, §5.1) - the rejection was parameter-level only, R11-50 |
| R11-38 | R10-38 | checkpoint covers the concepts mirror's retrieval state | closed (§3.4/§5.2/§16 M1/M5) - path+key keying lacks scope, R11-48 |
| R11-39 | R10-39 | distill/sweep maintenance envelope | closed (§17/§8/§9/§10/§7.2) |
| R11-40 | R10-40 | spawned daemon refusal surfaced, not retried into a hang | closed (§3.1/§16 M0) |
| R11-41 | R10-41 | §15 `scope` row describes the current ladder | closed (§15) |
| R11-42 | R10-42 | maintenance lock serializes every vault-mutating goroutine | closed (§3.1/§13/§16 M3) - the `reindex` process is still a writer outside every lock, R11-47 |
| R11-43 | R10-43 | wire log written before the SQLite commit | closed (§3.3/§16 M1) - ordering pinned for one write path; distill's two-destination write still unordered, R11-49 |
| R11-44 | R10-44 | checkpoint entries carry `key` beside `path` | closed (§3.4/§16 M1) - keys are scoped subjects and the entry carries no scope, R11-48 |
| R11-45 | R10-45 | Windows loopback credential protection | closed (§3.1/§16 M0) |
| R11-46 | R10-46 | `unused_days` floored at zero | closed (§7.2) |

All forty-six verified closed against the v1.10 body.

### R11-47..R11-50: new findings, verdict in v1.11

| ID | Finding | Verdict in v1.11 |
|---|---|---|
| R11-47 | `reindex` is the documented escape hatch for every divergence bug - and a second writer with no gate. The daemon holds the exclusive `~/.membraid/daemon.lock` flock from spawn until exit; `idle_timeout` defaults to `0`, so a daemon is almost always resident when the manual escape hatch is used; and a `reindex` racing it collides on git `index.lock` exactly like the two-process collision §3.1 exists to kill. R10-42 serialized the writers *inside* the daemon; the CLI writer still sat outside every lock | closed (§3.3 reindex takes the same flock non-blocking and refuses with a reason while a daemon holds it; §16 M1; §15 `lock_path` notes both guards) |
| R11-48 | R10-44's restore rule says "falls back to `key` within the same scope" - but the checkpoint concepts entry carries only `path` and `key`, so "the same scope" is a constraint replay cannot see. Keys are scoped subjects: two scopes can both hold `editor.theme`. And the scope-generalizing edit (§4.3, the human move that promotes a concept to `shared`) changes path and scope together - exactly the curation a fallback must survive - so a moved-and-re-scoped concept loses its state to a different concept sharing its key | closed (§3.4 entry carries `scope`; restore matches path then `(scope, key)`, the mirror's identity axis minus `type`; §16 M1) |
| R11-49 | R10-43 ordered the `memory_write` log against its transaction and left distillation's two-destination write unordered: a concept file into the vault and a `distill` line into the log. Line-first is the wrong order - a crash leaves `source_concept` links pointing at a path the mirror does not contain after a rebuild. The log "records the outcome of every derivation" (§3.4) on an ordering no sentence specified | closed (§3.4 file first, line second; a crash in between leaves an unlinked file the next pass's de-dupe check adopts) |
| R11-50 | R8-37 rejected `*` on the explicit `scope` parameter, but the ladder resolves scope in the daemon, not from the per-call parameter: a shim launched with `--scope "*"` / `MEMBRAID_SCOPE="*"` pins a literal `*` scope through the hint (step 2) with no parameter passing through the check. The literal `*` bucket R8-37 closed the door on has a second door | closed (§17 step 1 validates the resolved scope after the ladder; §16 M0) |

Weighed and deliberately dropped: a wire-log growth estimate against weekly
full-snapshot checkpoints - the vault is the bounded item, the log is deltas,
and even an order-of-magnitude miscalibration changes nothing; hot rows
exempted from sweep item 1 when their linked concept is later deprecated or
archived - a deprecated *concept* does not make source rows false, decay
handles them (weighed in round 9); an explicit `supersedes` pointing at a row
that does not vacate the `(scope, kind, key)` unique index - the constraint
violation is the designed self-healing, not a gap; distill's read snapshot
consistency under concurrent writes - single-process SQLite statement
snapshots, and a read transaction is an implementation detail (agreed since
round 5); FTS5 external-content sync (unchanged since round 5); `memory_get`
by id as a cross-scope UUID capability (dropped in prior rounds).

**Round result:** 46 carried, 46 closed, 4 new, 0 open, 0 for the human.
Convergence is **not** reached: R11-47 and R11-48 are substantive operational
constraints (an external writer gate, checkpoint identity completeness), not
small adjacency polish. SPEC.md is now v1.11; rationale in
`docs/SPEC-CHANGELOG.md`.

**Status:** complete, handed off to Round 12.


## Round 12 (in: v1.11, out: v1.12) - reviewer: opus 5/claude

Round 11's handoff asked whether each of its four fixes was "stated narrowly
enough to be silently side-stepped by an adjacent case". Each of the four was
re-checked against the v1.11 body rather than the summary table, and all four
are genuinely in place. All four also have a neighbour: `reindex` was gated and
`backup` was not; the checkpoint gained `scope` and landed on a fallback that
still is not unique; distillation's writes were ordered and the recovery the
ordering relies on does not actually recover; and the resolved-scope validator
rejects `*` while admitting `unscoped` through the identical door.

### R12-1..R12-50: restatement of R11 findings, verified against the v1.11 body

| ID | Restates | Finding | Verdict in v1.11 |
|---|---|---|---|
| R12-1 | R11-1 | `unscoped` quarantine; `shared` auto-included, incl. the unscoped-connection exception|closed (§17) |
| R12-2 | R11-2 | `scope` + `key` frontmatter and mirror columns|closed (§4.2/§4.3/§5.2) |
| R12-3 | R11-3 | `source` out of the supersession match|closed (§6.2) |
| R12-4 | R11-4 | supersede-all + partial unique index + close-before-insert|closed (§6.2) |
| R12-5 | R11-5 | key normalization + vocabulary incl. `.`|closed (§6.3) |
| R12-6 | R11-6 | key lookup on the tool surface|closed (§7.2/§10) |
| R12-7 | R11-7 | `min_concept_results` floor|closed (§7.2/§15) |
| R12-8 | R11-8 | distillation gathers the supersede ancestry|closed (§8 step 1) |
| R12-9 | R11-9 | authoritative `kind` vocabulary|closed (§6.1) |
| R12-10 | R11-10 | scope predicate pushed into candidate queries|closed (§7.2 step 1) |
| R12-11 | R11-11 | `session_ref` provenance|closed (§10) |
| R12-12 | R11-12 | dot-run key collapsing|closed (§6.3) |
| R12-13 | R11-13 | multi-source cluster authorship|closed (§8 step 4) |
| R12-14 | R11-14 | Streamable HTTP naming|closed (§2/§3.1/§10/§12) |
| R12-15 | R11-15 | `memory_get` by key across kinds|closed (§10/§11) |
| R12-16 | R11-16 | TLS required for remote|closed (§11/§12/§15) |
| R12-17 | R11-17 | remote `source` bound to bearer token|closed (§10/§11/§15) |
| R12-18 | R11-18 | two-pointer supersession (`superseded_by`)|closed (§5.1/§6.2/§3.4/§16 M1) |
| R12-19 | R11-19 | keyed ancestry via `idx_memories_key_all`|closed (§5.1/§8/§16 M4) |
| R12-20 | R11-20 | `insight` vs `memory_propose` boundary|closed (§6.1) |
| R12-21 | R11-21 | candidate_k counts in-scope rows|closed (§7.2 step 1) |
| R12-22 | R11-22 | connection handshake (source/session_ref)|closed (§3.1/§10) |
| R12-23 | R11-23 | config table completeness|closed (§15) |
| R12-24 | R11-24 | M2/M4/M6 acceptance sync|closed (§16) |
| R12-25 | R11-25 | `memory_propose` accepts `key`|closed (§10/§11) |
| R12-26 | R11-26 | key-only search is exact lookup; typos|closed (§7.2/§6.1/§3.4) |
| R12-27 | R11-27 | unkeyed ancestry child lookup indexed|closed (§5.1/§8/§16 M1/M4) |
| R12-28 | R11-28 | three read paths named for the refresh rule|closed (§7.2 table) |
| R12-29 | R11-29 | `scope_hint` composes with the §17 ladder|closed (§3.1/§17) |
| R12-30 | R11-30 | one `fuzzy_supersede_threshold` for all unkeyed surfaces|closed (§6.2/§8/§9/§15/§16 M4) |
| R12-31 | R11-31 | §17 ladder written order equals executed order|closed (§17/§3.1/§16 M0) |
| R12-32 | R11-32 | unscoped connection defaults to `shared` only|closed (§17) |
| R12-33 | R11-33 | maintenance/diagnostic reads never refresh|closed (§7.2 table) |
| R12-34 | R11-34 | exact lookups span `concepts` and `memories`|closed (§7.2/§10/§11) |
| R12-35 | R11-35 | `protocol_version` mismatch rule|closed (§3.1) |
| R12-36 | R11-36 | schema and wire-log versions (§5.3)|closed (§5.3) |
| R12-37 | R11-37 | `*` rejected on write; `valid_from`/`created_at` invariant|closed (§17 step 1, §5.1) - the resolved-scope validator it grew rejects `*` and not `unscoped`, R12-53 |
| R12-38 | R11-38 | checkpoint covers the concepts mirror's retrieval state|closed (§3.4/§5.2/§16 M1/M5) |
| R12-39 | R11-39 | distill/sweep maintenance envelope|closed (§17/§8/§9/§10/§7.2) |
| R12-40 | R11-40 | spawned daemon refusal surfaced, not retried into a hang|closed (§3.1/§16 M0) |
| R12-41 | R11-41 | §15 `scope` row describes the current ladder|closed (§15) |
| R12-42 | R11-42 | maintenance lock serializes every vault-mutating goroutine|closed (§3.1/§13/§16 M3) |
| R12-43 | R11-43 | wire log written before the SQLite commit|closed (§3.3/§16 M1) - the backup path inherits the same invariant and had no capture order, R12-51 |
| R12-44 | R11-44 | checkpoint entries carry `key` beside `path`|closed (§3.4/§16 M1) |
| R12-45 | R11-45 | Windows loopback credential protection|closed (§3.1/§16 M0) |
| R12-46 | R11-46 | `unused_days` floored at zero|closed (§7.2) |
| R12-47 | R11-47 | `reindex` gated on the daemon flock|closed (§3.3/§15/§16 M1) - verified in the body: non-blocking flock, refusal message, upgrade path exempted. `backup` and `init` sit beside it ungated, R12-51 |
| R12-48 | R11-48 | checkpoint concepts entry carries `scope`|closed (§3.4/§16 M1) - verified: entry carries path+scope+key. The `(scope, key)` fallback it lands on is not unique, R12-52 |
| R12-49 | R11-49 | distillation's two-destination write is ordered file-first|closed (§3.4) - verified: ordering stated. The recovery path it names does not recover, R12-54 |
| R12-50 | R11-50 | `*` rejection validates the resolved scope|closed (§17 step 1/§16 M0) - verified: validator runs after the ladder. It admits `unscoped` by the same door, R12-53 |

All fifty verified closed against the v1.11 body, the four R11 fixes by direct
inspection of §3.3, §3.4, §8 and §17 rather than by the handoff table.

### R12-51..R12-54: new findings, verdict in v1.12

| ID | Finding | Verdict in v1.12 |
|---|---|---|
| R12-51 | R11-47 gated `reindex` against a resident daemon and stopped there. The bullet immediately below it promises `backup` makes "a consistent copy of index + vault + wire log" with no gate and no method: in WAL mode a file copy of a live `index.db` is torn (committed frames sit in the `-wal` until checkpoint), the vault and log move under the copy, and nothing orders the three captures - so a backup taken log-first captures rows whose wire-log lines are missing and a restore drops them at the first rebuild, which is R10-43's failure re-entering through the recovery tool. `init` is likewise unstated against a live daemon. The spec names four CLI entry points and gates one | closed (§3.3: `backup` uses SQLite's online backup API, snapshots the vault at a fixed commit, and captures index -> log -> vault so the log stays a superset; `init` refuses on an existing vault/index; a sentence requiring every artifact-touching subcommand to state its stance on a resident daemon; §16 M1) |
| R12-52 | R11-48 correctly added `scope` to the checkpoint entry and then landed the fallback on `(scope, key)`, described as "the mirror's identity axis minus the type". The mirror's identity is `(scope, type, key)` - §5.2 indexes it and §6.2 de-dupes on it - so one scope may legitimately hold a `preference` and a `rule` under one key, and the fallback can match two rows with no stated resolution. Dropping `type` also bought nothing: the curation R11-48 set out to survive (the scope-generalizing edit) changes path and scope, never type | closed (§3.4 entry carries `type`; fallback is the full `(scope, type, key)`; an ambiguous match skips the entry rather than guessing, because writing one concept's decay onto another is worse than the reset being avoided) |
| R12-53 | R11-50 extended the `*` rejection from the parameter to the resolved scope - and `unscoped` walks through the same door untouched. §17 calls it "never a deliberate destination", but nothing refuses a deliberate one: a `scope: unscoped` call, or a shim pinned `--scope unscoped`, writes into the quarantine bucket. Its whole value is that its size means "something is misconfigured" (§9 reports it, §15 surfaces it), so a caller able to write there deliberately can forge the only diagnostic the design offers about itself | closed (§17 step 1: the resolved-scope validator refuses a *requested* `unscoped` with the same message as `*`; it stays reachable only as step 5's fall-through - reached, never requested) |
| R12-54 | R11-49 ordered distillation's two writes correctly and rests the crash case on a recovery that does not recover: §8 step 5 says the de-dupe check will "link `related` and skip", which never writes the `distill` line, so the cluster's rows never regain `source_concept`. §9 sweep item 1 treats `source_concept IS NULL` as an unlinked stale candidate, the skip repeats on every later pass, and sweep flags rows forever whose concept has been in the vault the whole time. §3.4 calls this "adopted"; skipping is not adopting | closed (§8 step 5 writes the `distill` line on the skip path too - skipping the file is not skipping the link, since those rows genuinely are sourced from that concept; §3.4's recovery sentence now says what adoption does; §16 M4) |

**Round result:** 50 carried, 50 closed, 4 new, 0 open, 0 for the human.
SPEC.md is now v1.12; rationale in `docs/SPEC-CHANGELOG.md`.

**Convergence is not reached, and the reason is worth stating precisely.**
These four are not adjacency polish. R12-51 is a data-loss path through the
recovery tool, R12-53 is a forgeable diagnostic, and R12-54 is a stated
recovery that does not run - each a mechanism that reads as complete and is
not. But the *shape* has now repeated for three rounds: a fix lands correctly
on the case it was written for and stops at the edge of the neighbouring case.
Rounds 10, 11 and 12 each found four or five of these, and there is no sign of
the supply running out, because each fix creates a new edge. That is a property
of reviewing prose, not evidence of an unsound design. The recommendation to
the human is in the round note below.

Weighed and deliberately dropped: flock semantics differing on Windows
(`LockFileEx` vs `flock`) - a Go portability detail, not a spec gap, and
`x/sys` covers both; `memory_propose`'s file-then-mirror ordering - the mirror
is explicitly a derived cache that `reindex` rebuilds, so a missed row heals
without a rule; the in-process maintenance lock (R10-42) versus the process
flock (R11-47) being two mechanisms - they guard different scopes (job versus
process) and the daemon's own open-time rebuild runs before timers start, so
they never contend.

**Note for the human (arbiter):** nothing here is blocked or disputed, and no
finding needs a ruling. The judgment call that does belong to you is whether to
keep going. Twelve rounds have taken the spec from v1.2 to v1.12 and the last
three produced 5, 4 and 4 findings with no downward trend - the loop is still
finding real defects, but it will keep finding them at roughly this rate for as
long as it runs, because each fix creates a fresh edge for the next round. The
spec is now considerably more implementable than it was at v1.9, and the
highest-value next step is probably M0 rather than round 13: building will
surface the remaining ambiguities faster and more decisively than reading will,
and the ones it surfaces will be the ones that actually matter.

**Status:** handed off. Next: Round 13 (in: v1.12, out: v1.13), reviewer:
opencode/big-pickle - restate R12-1..R12-54 as R13-<k> with closed/open
verdicts, add any new findings, produce v1.13, prepend to SPEC-CHANGELOG.md.
Converged when a round produces zero new findings and no open carries.

---

## Round 13 (in: v1.12, out: v1.13) - reviewer: opencode/big-pickle

Round 12's handoff asked for two things: verify the R12 fixes in the body
rather than by the table, and give an independent read on whether M0 is higher
value than round 14. The first was done - the four R12 findings were
re-inspected in §3.3, §3.4, §8 and §17 before being restated - and it caught
something the handoff table would have missed (R13-55, R13-56).

### R13-1..R13-54: restatement of R12 findings, verified against the v1.12 body

| ID | Restates | Finding | Verdict in v1.12 |
|---|---|---|---|
| R13-1 | R12-1 | `unscoped` quarantine; `shared` auto-included, incl. the unscoped-connection exception | closed (§17) |
| R13-2 | R12-2 | `scope` + `key` frontmatter and mirror columns | closed (§4.2/§4.3/§5.2) |
| R13-3 | R12-3 | `source` out of the supersession match | closed (§6.2) |
| R13-4 | R12-4 | supersede-all + partial unique index + close-before-insert | closed (§6.2) |
| R13-5 | R12-5 | key normalization + vocabulary incl. `.` | closed (§6.3) |
| R13-6 | R12-6 | key lookup on the tool surface | closed (§7.2/§10) |
| R13-7 | R12-7 | `min_concept_results` floor | closed (§7.2/§15) |
| R13-8 | R12-8 | distillation gathers the supersede ancestry | closed (§8 step 1) |
| R13-9 | R12-9 | authoritative `kind` vocabulary | closed (§6.1) |
| R13-10 | R12-10 | scope predicate pushed into candidate queries | closed (§7.2 step 1) |
| R13-11 | R12-11 | `session_ref` provenance | closed (§10) |
| R13-12 | R12-12 | dot-run key collapsing | closed (§6.3) |
| R13-13 | R12-13 | multi-source cluster authorship | closed (§8 step 4) |
| R13-14 | R12-14 | Streamable HTTP naming | closed (§2/§3.1/§10/§12) |
| R13-15 | R12-15 | `memory_get` by key across kinds | closed (§10/§11) |
| R13-16 | R12-16 | TLS required for remote | closed (§11/§12/§15) |
| R13-17 | R12-17 | remote `source` bound to bearer token | closed (§10/§11/§15) |
| R13-18 | R12-18 | two-pointer supersession (`superseded_by`) | closed (§5.1/§6.2/§3.4/§16 M1) |
| R13-19 | R12-19 | keyed ancestry via `idx_memories_key_all` | closed (§5.1/§8/§16 M4) |
| R13-20 | R12-20 | `insight` vs `memory_propose` boundary | closed (§6.1) |
| R13-21 | R12-21 | candidate_k counts in-scope rows | closed (§7.2 step 1) |
| R13-22 | R12-22 | connection handshake (source/session_ref) | closed (§3.1/§10) |
| R13-23 | R12-23 | config table completeness | closed (§15) |
| R13-24 | R12-24 | M2/M4/M6 acceptance sync | closed (§16) |
| R13-25 | R12-25 | `memory_propose` accepts `key` | closed (§10/§11) |
| R13-26 | R12-26 | key-only search is exact lookup; typos | closed (§7.2/§6.1/§3.4) |
| R13-27 | R12-27 | unkeyed ancestry child lookup indexed | closed (§5.1/§8/§16 M1/M4) |
| R13-28 | R12-28 | three read paths named for the refresh rule | closed (§7.2 table) |
| R13-29 | R12-29 | `scope_hint` composes with the §17 ladder | closed (§3.1/§17) |
| R13-30 | R12-30 | one `fuzzy_supersede_threshold` for all unkeyed surfaces | closed (§6.2/§8/§9/§15/§16 M4) |
| R13-31 | R12-31 | §17 ladder written order equals executed order | closed (§17/§3.1/§16 M0) |
| R13-32 | R12-32 | unscoped connection defaults to `shared` only | closed (§17) |
| R13-33 | R12-33 | maintenance/diagnostic reads never refresh | closed (§7.2 table) |
| R13-34 | R12-34 | exact lookups span `concepts` and `memories` | closed (§7.2/§10/§11) |
| R13-35 | R12-35 | `protocol_version` mismatch rule | closed (§3.1) |
| R13-36 | R12-36 | schema and wire-log versions (§5.3) | closed (§5.3) |
| R13-37 | R12-37 | `*` rejected on write; `valid_from`/`created_at` invariant | closed (§17 step 1, §5.1) |
| R13-38 | R12-38 | checkpoint covers the concepts mirror's retrieval state | closed (§3.4/§5.2/§16 M1/M5) |
| R13-39 | R12-39 | distill/sweep maintenance envelope | closed (§17/§8/§9/§10/§7.2) |
| R13-40 | R12-40 | spawned daemon refusal surfaced, not retried into a hang | closed (§3.1/§16 M0) |
| R13-41 | R12-41 | §15 `scope` row describes the current ladder | closed (§15) |
| R13-42 | R12-42 | maintenance lock serializes every vault-mutating goroutine | closed (§3.1/§13/§16 M3) |
| R13-43 | R12-43 | wire log written before the SQLite commit | closed (§3.3/§16 M1) |
| R13-44 | R12-44 | checkpoint entries carry `key` beside `path` | closed (§3.4/§16 M1) |
| R13-45 | R12-45 | Windows loopback credential protection | closed (§3.1/§16 M0) |
| R13-46 | R12-46 | `unused_days` floored at zero | closed (§7.2) |
| R13-47 | R12-47 | `reindex` gated on the daemon flock | closed (§3.3/§15/§16 M1) |
| R13-48 | R12-48 | checkpoint concepts entry carries `scope` | closed (§3.4/§16 M1) |
| R13-49 | R12-49 | distillation's two-destination write is ordered file-first | closed (§3.4) |
| R13-50 | R12-50 | `*` rejection validates the resolved scope | closed (§17 step 1/§16 M0) |
| R13-51 | R12-51 | `backup` method + capture order; `init` refusal; every artifact-touching subcommand states its stance | closed (§3.3/§16 M1) - but the ordering treated the log and the vault as separate artifacts while the log lives in the vault, R13-57 |
| R13-52 | R12-52 | checkpoint entry carries `type`; fallback is the full `(scope, type, key)`; ambiguous match skips | closed (§3.4/§16 M1) - M1's acceptance row still says `(scope, key)`, R13-55 |
| R13-53 | R12-53 | resolved-scope validator refuses a requested `unscoped`; fall-through only | closed (§17 step 1) - M0's acceptance row lists only `*`, R13-56 |
| R13-54 | R12-54 | de-dupe skip path writes its `distill` line | closed (§8 step 5/§3.4/§16 M4) |

All fifty-four verified closed against the v1.12 body, the four R12 fixes by
direct inspection of §3.3, §3.4, §8 and §17 rather than by the handoff table.

### R13-55..R13-57: new findings, verdict in v1.13

| ID | Finding | Verdict in v1.13 |
|---|---|---|
| R13-55 | R12-52 fixed §3.4 and claimed M1 closed, but the M1 acceptance row still fell back "by path then `(scope, key)` (§3.4)" - the exact identity R12-52 removed from the body. A `reindex` onto two concepts sharing `(scope, key)` under different types would report the accepted fallback's non-uniqueness as accepted behaviour. The row now reads `(scope, type, key)`. | closed (§16 M1 text matched to §3.4) |
| R13-56 | R12-53 extended the `*` refusal to any requested `unscoped`, and the M0 acceptance enumerated only the `*` half - a builder checking the acceptance row alone would watch for the wrong refusal. The row now refuses `*` and any requested `unscoped` on write and warns on first fall-through. | closed (§16 M0 text matched to §17 step 1) |
| R13-57 | R12-51's capture order names index, wire log and vault as if they were three separate artifacts, but the wire log lives inside the vault (`vault/.hot/writes-YYYY-MM.jsonl`, §3.2). The vault snapshot is a fixed commit, so its embedded log is strictly older than the separately captured one, and nothing says which is authoritative after a restore. Appending the captured log to the restored vault's embedded copy would resurface superseded writes as fresh rows - a door past supersede-all. The bullet now states the captured log is authoritative and a restore *replaces* the vault's embedded `.hot`, never appends to it. | closed (§3.3: authority + replace, never append) |

**Round result:** 54 carried, 54 closed, 3 new, 0 open, 0 for the human.
SPEC.md is now v1.13; rationale in `docs/SPEC-CHANGELOG.md`.

**Convergence note.** Still not converged by the letter, but the shape changed.
Rounds 10, 11 and 12 produced 5, 4 and 4 findings; this round produced 3, and
all three are acceptance rows or artifact cross-references lagging rules the
body already carries - not mechanisms that read as complete and are not. A
downward move in count and in kind. Two of the three (R13-55, R13-56) were
flagged by the same body-vs-handoff re-verification discipline Round 12 applied
a round ago; that check keeps earning its keep.

Weighed and deliberately dropped: nothing new this round. Round 12's three
stand (flock semantics differing across OSes, `memory_propose`'s file-then-
mirror ordering, the in-process maintenance lock versus the process flock -
different scopes), as do Rounds 3-11 lists. One candidate examined and dropped
here: whether "replace, never append" needs a wire-log *rotation* rule - it does
not, because the restored vault's embedded `.hot` is strictly *older* than the
captured log, so the replacement is wholesale and no interleaving exists to
order.

**Note for the human (arbiter):** nothing open and nothing blocked. The
round-12 recommendation - that M0 is higher value than another prose round -
gets this arbiter's agreement, with a caveat. Agreement has teeth: thirteen
rounds have built new edges almost as fast as they are thinned, and this
round's findings are acceptance rows catching up with the body, the signal of a
spec that is done being read. But the caveat is that M0's build will surface
exactly the edges the prose rounds keep waving at - backup's authoritative-log
copy, checkpoint identity, distillation's skip path all become implementation
questions on day one - so build M0 quickly, or point the loop at implemented
code once M0 exists.

**Status:** complete, handed off to Round 14.


## Round 14 (in: v1.13, out: v1.14) - reviewer: opus 5/claude

The three R13 fixes were re-checked in the body rather than from the handoff
table: §3.3 carries the captured-log authority and the replace-never-append
rule, the M1 row reads `(scope, type, key)`, and the M0 row refuses `*` and any
requested `unscoped`. All three are in place. Each also has a neighbour, and
one of this round's three findings is the recurrence of a class rather than a
new instance of it.

### R14-1..R14-57: restatement of R13 findings, verified against the v1.13 body

| ID | Restates | Finding | Verdict in v1.13 |
|---|---|---|---|
| R14-1 | R13-1 | `unscoped` quarantine; `shared` auto-included|closed (§17) |
| R14-2 | R13-2 | `scope` + `key` frontmatter and mirror columns|closed (§4.2/§4.3/§5.2) |
| R14-3 | R13-3 | `source` out of the supersession match|closed (§6.2) |
| R14-4 | R13-4 | supersede-all + partial unique index + close-before-insert|closed (§6.2) |
| R14-5 | R13-5 | key normalization + vocabulary incl. `.`|closed (§6.3) |
| R14-6 | R13-6 | key lookup on the tool surface|closed (§7.2/§10) |
| R14-7 | R13-7 | `min_concept_results` floor|closed (§7.2/§15) |
| R14-8 | R13-8 | distillation gathers the supersede ancestry|closed (§8 step 1) |
| R14-9 | R13-9 | authoritative `kind` vocabulary|closed (§6.1) |
| R14-10 | R13-10 | scope predicate pushed into candidate queries|closed (§7.2 step 1) |
| R14-11 | R13-11 | `session_ref` provenance|closed (§10) |
| R14-12 | R13-12 | dot-run key collapsing|closed (§6.3) |
| R14-13 | R13-13 | multi-source cluster authorship|closed (§8 step 4) |
| R14-14 | R13-14 | Streamable HTTP naming|closed (§2/§3.1/§10/§12) |
| R14-15 | R13-15 | `memory_get` by key across kinds|closed (§10/§11) |
| R14-16 | R13-16 | TLS required for remote|closed (§11/§12/§15) |
| R14-17 | R13-17 | remote `source` bound to bearer token|closed (§10/§11/§15) |
| R14-18 | R13-18 | two-pointer supersession (`superseded_by`)|closed (§5.1/§6.2/§3.4/§16 M1) |
| R14-19 | R13-19 | keyed ancestry via `idx_memories_key_all`|closed (§5.1/§8/§16 M4) |
| R14-20 | R13-20 | `insight` vs `memory_propose` boundary|closed (§6.1) |
| R14-21 | R13-21 | candidate_k counts in-scope rows|closed (§7.2 step 1) |
| R14-22 | R13-22 | connection handshake (source/session_ref)|closed (§3.1/§10) |
| R14-23 | R13-23 | config table completeness|closed (§15) |
| R14-24 | R13-24 | M2/M4/M6 acceptance sync|closed (§16) - the acceptance-lag class recurred again this round, R14-60 |
| R14-25 | R13-25 | `memory_propose` accepts `key`|closed (§10/§11) |
| R14-26 | R13-26 | key-only search is exact lookup; typos|closed (§7.2/§6.1/§3.4) |
| R14-27 | R13-27 | unkeyed ancestry child lookup indexed|closed (§5.1/§8/§16 M1/M4) |
| R14-28 | R13-28 | three read paths named for the refresh rule|closed (§7.2 table) |
| R14-29 | R13-29 | `scope_hint` composes with the §17 ladder|closed (§3.1/§17) |
| R14-30 | R13-30 | one `fuzzy_supersede_threshold` for all unkeyed surfaces|closed (§6.2/§8/§9/§15/§16 M4) |
| R14-31 | R13-31 | §17 ladder written order equals executed order|closed (§17/§3.1/§16 M0) |
| R14-32 | R13-32 | unscoped connection defaults to `shared` only|closed (§17) |
| R14-33 | R13-33 | maintenance/diagnostic reads never refresh|closed (§7.2 table) |
| R14-34 | R13-34 | exact lookups span `concepts` and `memories`|closed (§7.2/§10/§11) |
| R14-35 | R13-35 | `protocol_version` mismatch rule|closed (§3.1) |
| R14-36 | R13-36 | schema and wire-log versions (§5.3)|closed (§5.3) - the rules cover unknown `v` and not a truncated line, R14-59 |
| R14-37 | R13-37 | `*` rejected on write; `valid_from`/`created_at` invariant|closed (§17 step 1, §5.1) |
| R14-38 | R13-38 | checkpoint covers the concepts mirror's retrieval state|closed (§3.4/§5.2/§16 M1/M5) - the carve-out covers `reindex` only, not the refresh paths that run far more often, R14-58 |
| R14-39 | R13-39 | distill/sweep maintenance envelope|closed (§17/§8/§9/§10/§7.2) |
| R14-40 | R13-40 | spawned daemon refusal surfaced, not retried into a hang|closed (§3.1/§16 M0) |
| R14-41 | R13-41 | §15 `scope` row describes the current ladder|closed (§15) |
| R14-42 | R13-42 | maintenance lock serializes every vault-mutating goroutine|closed (§3.1/§13/§16 M3) |
| R14-43 | R13-43 | wire log written before the SQLite commit|closed (§3.3/§16 M1) |
| R14-44 | R13-44 | checkpoint entries carry `key` beside `path`|closed (§3.4/§16 M1) |
| R14-45 | R13-45 | Windows loopback credential protection|closed (§3.1/§16 M0) |
| R14-46 | R13-46 | `unused_days` floored at zero|closed (§7.2) |
| R14-47 | R13-47 | `reindex` gated on the daemon flock|closed (§3.3/§15/§16 M1) |
| R14-48 | R13-48 | checkpoint concepts entry carries `scope`|closed (§3.4/§16 M1) |
| R14-49 | R13-49 | distillation's two-destination write is ordered file-first|closed (§3.4) |
| R14-50 | R13-50 | `*` rejection validates the resolved scope|closed (§17 step 1/§16 M0) |
| R14-51 | R13-51 | `backup` method + capture order; `init` refusal; subcommand stance rule|closed (§3.3/§16 M1) - tearing was solved for the index and not for the log it captures, R14-59 |
| R14-52 | R13-52 | checkpoint carries `type`; full-identity fallback; ambiguous match skips|closed (§3.4/§16 M1) |
| R14-53 | R13-53 | resolved-scope validator refuses a requested `unscoped`|closed (§17 step 1/§16 M0) |
| R14-54 | R13-54 | de-dupe skip path writes its `distill` line|closed (§8 step 5/§3.4/§16 M4) |
| R14-55 | R13-55 | M1 acceptance restates the `(scope, type, key)` fallback|closed (§16 M1) - verified in the body at the M1 row; the same row lags R13-57's own rule, R14-60 |
| R14-56 | R13-56 | M0 acceptance refuses `*` and any requested `unscoped`|closed (§16 M0) - verified verbatim in the body |
| R14-57 | R13-57 | captured log is authoritative; restore replaces `.hot`, never appends|closed (§3.3) - verified in the body; removing the embedded fallback makes a torn capture unrecoverable, R14-59 |

All fifty-seven verified closed against the v1.13 body, the three R13 fixes by
direct inspection of §3.3 and the M0/M1 acceptance rows.

### R14-58..R14-60: new findings, verdict in v1.14

| ID | Finding | Verdict in v1.14 |
|---|---|---|
| R14-58 | Four rounds (R9-38, R10-44, R11-48, R12-52) hardened `last_retrieved` across the *rebuild* path, and §5.2's carve-out is written for `reindex` alone. The mirror has three refresh paths: `reindex`, the watcher's per-file pass (M9), and the engine's own refresh after propose or sweep archive. The two uncovered ones are the common case, and the watcher fires on the human edit that promotes a `draft` to `stable` - the curation act the entire trust model is built on. A per-file refresh upserting from frontmatter writes a column frontmatter does not contain, so the natural implementation resets that concept's decay every time a human touches it | closed (§5.2: frontmatter fields are replaced, machine columns preserved, on all three paths; `last_retrieved` carried forward from the existing row or the checkpoint; §16 M9) |
| R14-59 | R12-51 solved tearing for the index (online backup API, never `cp`) and left the log it captures alongside it unaddressed - then R13-57 made the captured log *authoritative* and had a restore replace the vault's embedded `.hot`, removing the only other copy. A capture racing an append ends mid-line, and §5.3's replay rules cover an unknown `v` and say nothing about a truncated one. The two failures are opposite in kind: a partial tail is a write that never completed and must be skipped, an unknown `v` is a complete record this binary cannot read and must stop replay | closed (§3.3: appends are single whole-line writes, `backup` captures to the last complete line and discards a partial tail, which the index copy cannot contain either; §5.3: truncated final line skipped, unknown `v` refused, a partial line anywhere but the end is corruption) |
| R14-60 | The M1 row carries R12-51's capture order and not R13-57's authority rule, and restates the `(scope, type, key)` fallback without its ambiguous-match skip. That is the same acceptance-row lag R13-55 and R13-56 fixed, one row over, on the very fix R13 made - and the class has now recurred in rounds 6, 13 and 14. Fixing instances one at a time has not stopped it, and nothing says which text wins when a row and its section disagree | closed (§16 M1 brought current; §16 preamble states that acceptance rows restate the body and never extend it, the section wins on conflict, and a row citing a section is inside that section's blast radius when it changes) |

| R14-61 | The M1 acceptance cell has been physically split across eight newlines since a mid-loop edit, so that row stops being a table row: every other milestone renders inside the table and the densest, most-cited one renders as loose paragraph text beneath it. Found while verifying R14-60's edit, which had added a ninth wrap to an already-broken cell. A spec whose stated virtue is that a human can read it in Obsidian should render | closed (M1 joined to one line, all twelve rows verified complete; §16 preamble requires one physical line per row however long it grows) |

**Round result:** 57 carried, 57 closed, 4 new, 0 open, 0 for the human.
SPEC.md is now v1.14; rationale in `docs/SPEC-CHANGELOG.md`.

**Convergence call: not converged by the letter, and closer than the count
alone shows.** Four findings, against 5, 4, 4, 3 - flat, not falling. But the
mix is what matters, and one of the four (R14-61) is a rendering defect the
loop introduced in itself rather than anything about the design.
R14-58 is a genuine mechanism gap - the most frequent refresh path, on the most
important human action, resetting state four rounds were spent protecting -
and R14-59 is a durability gap that R13-57 deepened by removing a fallback.
Those two are real. But R14-60 is not a new defect; it is the same
acceptance-lag class recurring for the third time, and this round closed it
with a precedence rule rather than a fourth instance fix. That is the shape of
a review loop running out of distinct classes: the substantive findings are now
in the narrow seam between paths that were hardened and paths that were not,
and the bookkeeping findings are being closed structurally instead of
individually.

The recommendation from rounds 12 and 13 stands and firms up. Build M0. The two
substantive findings this round both live in code that does not exist yet - a
per-file mirror upsert and a log-capture loop - and both would have surfaced on
the first day of writing either one, more decisively than by reading. If the
loop continues past this round, point it at implemented code rather than prose.

Weighed and deliberately dropped: whether M9's acceptance should restate the
maintenance lock (M3 already accepts it for the watcher by name, and the new
§16 precedence rule makes the body authoritative); whether `memory_propose`'s
mirror refresh needs its own ordering rule (R12 dropped this and R14-58's
"machine columns preserved" now covers the only consequence that lost state);
whether the captured log needs a rotation rule across month boundaries (R13
dropped it, and whole-line capture does not change the reasoning). Rounds 3-13
lists stand.

**Status:** Round 15 was not run. See the close-out below.

---

## Decision close-out (not a review round)

The human asked both reviewers whether the spec was a solid enough starting
point to begin coding. Both concurred: stop reviewing, build M0 then M1, and
point the loop at implemented code afterwards rather than at prose.

Shared reasoning: finding counts across rounds 10-14 were 5, 4, 4, 3, 4 - flat,
not falling - and the reason is structural rather than a property of this
design, since each fix creates a fresh edge for the next round. Of round 14's
four, one closed a methodology class (R14-60) and one was the loop's own table
damage (R14-61); the two substantive ones (R14-58, R14-59) both described code
that does not exist yet and would have surfaced on the first day of writing it.
What remains genuinely unsettled is marked provisional in the body
(`fuzzy_supersede_threshold` pending M4 tuning, cwd-derived scope as an
acknowledged heuristic, `session_ref` as a stated approximation), which is the
right end state for a spec rather than a defect in it.

**One item was locked before stopping,** on opencode's flag and with claude's
concurrence on the substance: the wire log is the only artifact not rebuildable
from something else, and §5.3 specified only the *reader* side of its
versioning. The writer side is now explicit - when `v` must be bumped, that a
parser is kept for every `v` ever emitted, and that the `checkpoint` line is
the sharpest edge because it alone carries state nothing can reconstruct.

**One point of dissent, resolved on the merits:** opencode proposed a per-file
version stamp on each log file's first line. Claude dissented and the spec
records why - `writes-YYYY-MM.jsonl` rotates monthly while upgrades happen
whenever, so one file routinely holds lines from two binary versions and a
per-file header would misdescribe its own tail after the first mid-month
upgrade. Per-line `v` is the granularity that survives a log appended to across
upgrades. The rule opencode was protecting (field evolution must be versioned)
is adopted in full; only the mechanism changed.

SPEC.md is **v1.14.1**. Rounds 3-14 produced everything above that line; this
close-out is the last documentation change before implementation.

**Next:** M0. When implemented code exists, the loop resumes against the code
rather than the document.
