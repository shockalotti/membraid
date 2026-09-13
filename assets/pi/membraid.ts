// membraid for Pi: the user's shared memory across agents and machines.
//
// Pi has no MCP support by design, so this extension runs membraid's MCP
// server itself, one per session, and registers its tools under their own
// names. The server's instructions, which carry the session digest for this
// project (--digest), go at the end of the system prompt.
//
// Any failure leaves Pi working without memory: a notice, never a broken session.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { StringEnum } from "@earendil-works/pi-ai";
import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { Type, type TSchema } from "typebox";

const bin = process.env.MEMBRAID_BIN || `${process.env.HOME}/go/bin/membraid`;

type Pending = { resolve: (result: any) => void; reject: (err: Error) => void };

// Server speaks newline-delimited JSON-RPC to `membraid mcp` over stdio.
class Server {
  private proc: ChildProcess;
  private nextID = 1;
  private pending = new Map<number, Pending>();

  constructor(cwd: string) {
    this.proc = spawn(bin, ["mcp", "--source", "pi", "--digest"], { cwd, stdio: ["pipe", "pipe", "ignore"] });
    createInterface({ input: this.proc.stdout! }).on("line", (line) => {
      let msg: any;
      try {
        msg = JSON.parse(line);
      } catch {
        return;
      }
      const p = this.pending.get(msg.id);
      if (!p) return;
      this.pending.delete(msg.id);
      if (msg.error) p.reject(new Error(msg.error.message));
      else p.resolve(msg.result);
    });
    const failAll = (err: Error) => {
      for (const p of this.pending.values()) p.reject(err);
      this.pending.clear();
    };
    this.proc.on("error", failAll);
    this.proc.on("exit", () => failAll(new Error("membraid mcp exited")));
  }

  request(method: string, params: object = {}, timeoutMs = 30000): Promise<any> {
    const id = this.nextID++;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`membraid did not answer ${method} within ${timeoutMs / 1000}s`));
      }, timeoutMs);
      const done = (fn: (v: any) => void) => (v: any) => {
        clearTimeout(timer);
        fn(v);
      };
      this.pending.set(id, { resolve: done(resolve), reject: done(reject) });
      this.proc.stdin!.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n");
    });
  }

  notify(method: string) {
    this.proc.stdin!.write(JSON.stringify({ jsonrpc: "2.0", method }) + "\n");
  }

  // Closing stdin, rather than killing, lets the server push writes still
  // waiting out its sync delay before it exits.
  close() {
    this.proc.stdin?.end();
  }
}

// toSchema turns a tool's JSON Schema into TypeBox, with enums as StringEnum,
// which Google models accept where a union of literals is rejected.
function toSchema(s: any): TSchema {
  const opts = s.description ? { description: s.description } : {};
  switch (s.type) {
    case "string":
      return Array.isArray(s.enum) ? StringEnum(s.enum, opts) : Type.String(opts);
    case "integer":
      return Type.Integer(opts);
    case "number":
      return Type.Number(opts);
    case "boolean":
      return Type.Boolean(opts);
    case "array":
      return Type.Array(toSchema(s.items ?? {}), opts);
    case "object": {
      const required = new Set<string>(s.required ?? []);
      const props: Record<string, TSchema> = {};
      for (const [name, prop] of Object.entries(s.properties ?? {})) {
        props[name] = required.has(name) ? toSchema(prop) : Type.Optional(toSchema(prop));
      }
      return Type.Object(props, opts);
    }
    default:
      return Type.Unknown(opts);
  }
}

export default function (pi: ExtensionAPI) {
  let server: Server | undefined;
  let instructions = "";

  pi.on("session_start", async (_event, ctx) => {
    server?.close();
    instructions = "";
    try {
      server = new Server(ctx.cwd);
      const init = await server.request("initialize", {
        protocolVersion: "2025-06-18",
        capabilities: {},
        clientInfo: { name: "pi", version: "membraid-extension" },
      });
      server.notify("notifications/initialized");
      instructions = (init?.instructions ?? "").trim();
      const { tools } = await server.request("tools/list");
      for (const tool of tools ?? []) {
        pi.registerTool({
          name: tool.name,
          label: tool.name.replace(/_/g, " "),
          description: tool.description ?? "",
          parameters: toSchema(tool.inputSchema ?? { type: "object" }),
          async execute(_toolCallId, params) {
            if (!server) throw new Error("membraid is not running");
            const res = await server.request("tools/call", { name: tool.name, arguments: params });
            const text = (res?.content ?? [])
              .filter((c: any) => c.type === "text")
              .map((c: any) => c.text)
              .join("\n");
            if (res?.isError) throw new Error(text || `${tool.name} failed`);
            return { content: [{ type: "text", text }], details: {} };
          },
        });
      }
    } catch (err) {
      server?.close();
      server = undefined;
      instructions = "";
      if (ctx.hasUI) ctx.ui.notify(`membraid unavailable: ${(err as Error).message}`, "warning");
    }
  });

  pi.on("before_agent_start", async (event) => {
    if (!instructions) return;
    return { systemPrompt: `${event.systemPrompt}\n\n<membraid>\n${instructions}\n</membraid>` };
  });

  pi.on("session_shutdown", async () => {
    server?.close();
    server = undefined;
  });
}
