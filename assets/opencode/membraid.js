// membraid for OpenCode: put the shared-memory digest in front of the model
// when a session starts, the way Claude Code's SessionStart hook does.
//
// TWO APIS, ONE FILE
// OpenCode 2.x replaced the v1 hook map with a plugin definition:
//
//     export default { id, setup(context) }       // v2
//     export const MyPlugin = async (ctx) => ({}) // v1
//
// v2 has no `experimental.chat.system.transform`. The equivalent is a
// `context` hook registered on the session domain, whose input carries a
// mutable `system` array of { type: "text", text } parts. v1 keeps using the
// transform hook. Both share the logic below, so a machine on either version
// gets one digest per session.
//
// The default export carries `server` for v1 and `setup` for v2, so whichever
// version loads the file finds the entry point it knows.
//
// Two rules the failures taught: a binary that moves must be found, not
// assumed - and an empty digest is never cached, so one failed load cannot
// poison the session forever. Any failure is logged loudly rather than
// swallowing the session's memory quietly.
//
// Shelling out uses node:child_process rather than the v1 context's `$`:
// v2's context has no shell, and one code path serves both versions.
import { execFile } from "node:child_process"
import { access, appendFile } from "node:fs/promises"
import { constants as FS } from "node:fs"
import { promisify } from "node:util"

const execFileAsync = promisify(execFile)

// A one-line write scan rides every digest push. Skill paragraphs rot under
// task context (front-load decay); this line is read now, at the moment it
// applies. It joins the push, never the stored digest, so the digest itself
// cannot shift mid-conversation.
const scanLine =
  "Memory scan before you finish: a decision, discovery, or correction this turn gets a memory_write; a memory that guided you gets a memory_used."

// The install rewrites the first candidate to the true installed path; the
// rest cover where Go puts the binary next. MEMBRAID_BIN wins outright.
const candidates = () => [
  `${process.env.HOME}/go/bin/membraid`,
  `${process.env.HOME}/.local/bin/membraid`,
  `${process.env.HOME}/.local/share/go/bin/membraid`,
  "/usr/local/bin/membraid",
]

const digests = new Map()

// Telemetry for the write-compliance question: one line per push, so pushes
// can be correlated with later writes by session and time. Telemetry never
// breaks the turn; a failed log is silent.
const nudgeLog = async (sessionID) => {
  try {
    const entry = JSON.stringify({
      ts: new Date().toISOString(),
      channel: "opencode-push",
      session: String(sessionID ?? "default"),
    })
    await appendFile(`${process.env.HOME}/.membraid/nudge.jsonl`, `${entry}\n`)
  } catch {
    // telemetry is expendable
  }
}

const resolveBin = async () => {
  if (process.env.MEMBRAID_BIN) return process.env.MEMBRAID_BIN
  for (const c of candidates()) {
    try {
      await access(c, FS.X_OK)
      return c
    } catch {
      // keep looking; the loud error below names every miss at once
    }
  }
  try {
    const { stdout } = await execFileAsync("sh", ["-c", "command -v membraid"])
    const found = stdout.trim().split("\n")[0]
    if (found) return found
  } catch {
    // fall through to the loud failure
  }
  return ""
}

const load = async (sessionID, cwd) => {
  const bin = await resolveBin()
  if (!bin) {
    console.error(
      `membraid: no working binary (tried MEMBRAID_BIN, ${candidates().join(", ")}, and PATH); re-run membraid install to repair`
    )
    return ""
  }
  try {
    // The session id travels as an environment variable (no shell quoting to
    // get wrong): the binary records the receipt, and a session that never got
    // its digest hears about it loudly in the digest itself.
    const { stdout } = await execFileAsync(bin, ["context"], {
      cwd: cwd || process.env.HOME,
      env: { ...process.env, MEMBRAID_SESSION_ID: sessionID },
      maxBuffer: 8 * 1024 * 1024,
      timeout: 20000,
    })
    return String(stdout).trim()
  } catch (err) {
    console.error(`membraid: digest failed (${bin} context): ${err?.message ?? err}`)
    return ""
  }
}

// One push per session per digest. Returns the text to inject, or "".
const digestFor = async (sessionID, cwd) => {
  const key = sessionID ?? "default"
  if (!digests.has(key)) {
    const digest = await load(key, cwd)
    // Never cache an empty digest: a failed load must retry next turn, not
    // poison every remaining turn of the session.
    if (digest) digests.set(key, digest)
  }
  const digest = digests.get(key)
  if (!digest) return ""
  await nudgeLog(key)
  return `${digest}\n\n${scanLine}`
}

// ---------------------------------------------------------------------------
// OpenCode 1.x: hook map. The transform runs on every model request, so the
// digest is computed once per session and reused; re-running membraid each
// turn would add a spawn per request and let the digest shift mid-conversation.
// ---------------------------------------------------------------------------
export const Membraid = async ({ directory }) => ({
  "experimental.chat.system.transform": async (input, output) => {
    const text = await digestFor(input?.sessionID, directory)
    if (text) output.system.push(text)
  },
})

// ---------------------------------------------------------------------------
// OpenCode 2.x: id + setup, registering a session context hook
// ---------------------------------------------------------------------------
async function setup(context) {
  await context.session.hook("context", async (input) => {
    const text = await digestFor(input?.sessionID, context.location?.directory)
    // v2 carries the system prompt as parts; a text part is the analogue of
    // the v1 string push.
    if (text && Array.isArray(input.system)) {
      input.system.push({ type: "text", text })
    }
  })
}

export default {
  id: "membraid",
  server: Membraid,
  setup,
}
