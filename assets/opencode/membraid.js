// membraid for OpenCode: put the shared-memory digest in front of the model
// when a session starts, the way Claude Code's SessionStart hook does.
//
// experimental.chat.system.transform runs on EVERY model request, not once per
// session, so the digest is computed once per session and reused. Re-running
// membraid on every turn would add a process spawn to each request and, worse,
// let the digest shift mid-conversation as the agent writes to memory.
//
// Any failure yields no digest rather than a broken session. The hook is marked
// experimental in @opencode-ai/plugin 1.18.30; if OpenCode renames it, this
// plugin goes quiet and the MCP server instructions still reach the model.
export const Membraid = async ({ $, directory }) => {
  const bin = process.env.MEMBRAID_BIN || `${process.env.HOME}/go/bin/membraid`
  const digests = new Map()

  const load = async () => {
    try {
      const out = await $`${bin} context`.cwd(directory).quiet().nothrow().text()
      return out.trim()
    } catch {
      return ""
    }
  }

  return {
    "experimental.chat.system.transform": async (input, output) => {
      const key = input?.sessionID ?? "default"
      if (!digests.has(key)) digests.set(key, await load())
      const digest = digests.get(key)
      if (digest) output.system.push(digest)
    },
  }
}
