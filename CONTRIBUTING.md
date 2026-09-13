# Contributing to membraid

Thanks for considering a contribution. This covers building, testing and the few
rules that matter. For what membraid is and why it is shaped this way, read
[docs/V1-SCOPE.md](docs/V1-SCOPE.md) first, then [docs/SPEC.md](docs/SPEC.md) as
the design reference.

## Prerequisites

- **Go 1.27+.** SQLite is pure Go (modernc.org/sqlite): no CGO, no C toolchain.
- **git**, for the sync tests.

## Layout

- `cmd/membraid/` - the CLI, the MCP server, the installer's prompts, and the
  end-to-end tests that drive the built binary
- `internal/wirelog/` - the append-only log, the one artifact nothing can rebuild
- `internal/index/` - the SQLite index: supersession, search, tasks, scopes
- `internal/vaultsync/` - git sync between machines
- `internal/embed/` - optional local embedding models (Ollama, or built in via
  hugot; `-tags nobuiltin` leaves the built-in one out)
- `internal/install/` - per-harness setup (Claude Code, OpenCode, Grok, Hermes,
  Omarchy)
- `internal/vault/`, `internal/scope/`, `internal/config/` - the vault layout,
  project identity, and per-machine settings
- `assets/` - the agent skill, harness plugins and bar widget, embedded into the
  binary with `go:embed`

## Build and test

```sh
go build ./cmd/membraid
go vet ./...
gofmt -l cmd internal assets        # must print nothing
go test ./...
GOOS=windows go build -o /dev/null ./cmd/membraid   # Windows must keep compiling
go build -tags nobuiltin ./cmd/membraid             # without the built-in embedding model
```

The built-in embedding model's test downloads about 90 MB, so it is skipped
unless `MEMBRAID_TEST_BUILTIN=1` is set. Everything else runs offline.

Tests must never touch a real vault, real harness configs, or real sync state.
The end-to-end tests build the binary into a temp dir and point it at temp
directories through `MEMBRAID_VAULT` and `MEMBRAID_CONFIG_DIR`; follow the same
pattern. Harness installers are tested against a fake home directory with the
harness CLIs stubbed.

## Rules that are easy to break

- **The wire-log line format is locked** (SPEC §5.3). Everything else can be
  rebuilt from it, so a change to an existing line type needs a new version
  number and a reader that still accepts the old one. New line types are fine.
- **stdout belongs to the protocol in `membraid mcp`.** Diagnostics go to stderr;
  anything else on stdout corrupts the JSON-RPC stream.
- **A session must never break because of membraid.** `membraid context` exits
  0 with an empty digest on any error, because harness hooks run it on every
  session start.
- **Harness configs belong to the user.** Installers add or migrate membraid's
  own entries, back up what they edit, and leave everything else untouched.
- **No secrets in memory.** Memory syncs to a git remote; nothing should
  encourage storing credentials.
- **Writing style:** no em dashes, in code comments, docs, or commit messages.
  Use a hyphen or restructure the sentence.

## Pull requests

1. Branch off `master`.
2. `go vet`, `gofmt` and `go test ./...` are clean, and the Windows build
   compiles.
3. Keep the change focused, and explain the *why* in the description. If it
   changes behaviour described in `docs/`, update the doc in the same PR.
4. **Contributor License Agreement.** First-time contributors are asked to sign
   our [CLA](CLA.md): a bot comments on your PR with a one-line phrase to post.
   You keep the copyright to your work; the CLA lets the project distribute it
   and license future versions under different terms if needed (for example, a
   hosted edition). It is a one-time signature. *(This is a CLA, not a DCO: a DCO
   only certifies origin, whereas retaining relicensing rights requires a CLA.)*

The project's distributed code is [Apache-2.0](LICENSE).
