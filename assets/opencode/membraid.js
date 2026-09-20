// membraid for OpenCode: put the shared-memory digest in front of the model
// when a session starts, the way Claude Code's SessionStart hook does.
//
// experimental.chat.system.transform runs on EVERY model request, not once per
// session, so a good digest is computed once per session and reused. Re-running
// membraid on every turn would add a process spawn to each request and, worse,
// let the digest shift mid-conversation as the agent writes to memory.
//
// Two rules the failures taught: a binary that moves must be found, not
// assumed — and an empty digest is never cached, so one failed load cannot
// poison the session forever. Any failure is logged loudly rather than
// swallowing the session's memory quietly.
//
// The hook is marked experimental in @opencode-ai/plugin 1.18.30; if OpenCode
// renames it, this plugin goes quiet and the MCP server instructions still
// reach the model.
export const Membraid = async ({ $, directory }) => {
  // The install rewrites the first candidate to the true installed path;
  // the rest cover where Go puts the binary next. MEMBRAID_BIN wins outright.
  const candidates = [
    `${process.env.HOME}/go/bin/membraid`,
    `${process.env.HOME}/.local/share/go/bin/membraid`,
    "/usr/local/bin/membraid",
  ]
  const digests = new Map()

  const resolveBin = async () => {
    if (process.env.MEMBRAID_BIN) return process.env.MEMBRAID_BIN
    for (const c of candidates) {
      try {
        const r = await $`test -x ${c}`.cwd(directory).quiet().nothrow()
        if (r.exitCode === 0) return c
      } catch {
        // keep looking; the loud error below names every miss at once
      }
    }
    try {
      const found = await $`command -v membraid`.quiet().nothrow().text()
      if (found.trim()) return found.trim().split("\n")[0]
    } catch {
      // fall through to the loud failure
    }
    return ""
  }

  const load = async (sessionID) => {
    const bin = await resolveBin()
    if (!bin) {
      console.error(
        `membraid: no working binary (tried MEMBRAID_BIN, ${candidates.join(", ")}, and PATH); re-run membraid install to repair`
      )
      return ""
    }
    try {
      // The session id travels as an environment variable (no shell quoting
      // to get wrong): the binary records the receipt, and a session that
      // never got its digest hears about it loudly in the digest itself.
      process.env.MEMBRAID_SESSION_ID = sessionID
      const out = await $`${bin} context`.cwd(directory).quiet().nothrow().text()
      return out.trim()
    } catch (err) {
      console.error(`membraid: digest failed (${bin} context): ${err?.message ?? err}`)
      return ""
    } finally {
      delete process.env.MEMBRAID_SESSION_ID
    }
  }

  return {
    "experimental.chat.system.transform": async (input, output) => {
      const key = input?.sessionID ?? "default"
      if (!digests.has(key)) {
        const digest = await load(key)
        // Never cache an empty digest: a failed load must retry next turn,
        // not poison every remaining turn of the session.
        if (digest) digests.set(key, digest)
      }
      const digest = digests.get(key)
      if (digest) output.system.push(digest)
    },
  }
}
