import { api } from "@/lib/api-client"

export interface McpPolicy {
  name: string
  enabled: boolean
  allOrganizations: boolean
  organizationIds: number[]
  tools: string[]
}
export interface McpClient extends McpPolicy { id: number; legacy: boolean; isDefault: boolean }
export interface ClientCatalog { clients: McpClient[]; availableTools: string[] }

export const mcpClients = {
  list: async () => (await api.get<ClientCatalog>("/mcpClients")).data,
  create: async (policy: McpPolicy) => (await api.post<{ id: number; key: string }>("/mcpClients", policy)).data,
  update: async (id: number, policy: McpPolicy) => { await api.put(`/mcpClients/${id}`, policy) },
  rotate: async (id: number) => (await api.post<{ id: number; key: string }>(`/mcpClients/${id}/rotate`)).data,
  revoke: async (id: number) => { await api.delete(`/mcpClients/${id}`) },
}

export type DiagnosticState = "ready" | "route" | "auth" | "forbidden" | "protocol" | "timeout" | "network" | "busy" | "upstream"
export interface Diagnostic {
  state: DiagnosticState
  stage: "handshake" | "tools"
  status?: number
  version?: string
  count?: number
}

class ProbeFailure extends Error {
  constructor(readonly state: DiagnosticState, readonly status?: number) { super(state) }
}

export async function diagnoseMcp(key: string, signal: AbortSignal): Promise<Diagnostic> {
  const modern = "2026-07-28"
  let version = "2025-06-18"
  let session = ""
  let stage: Diagnostic["stage"] = "handshake"
  let id = 0
  const endpoint = new URL("/mcp", window.location.origin)
  async function rpc(method: string, params: Record<string, unknown> = {}, notification = false) {
    const headers: Record<string, string> = { "Content-Type": "application/json", Accept: "application/json, text/event-stream", "MCP-Protocol-Version": version }
    if (key) headers.Authorization = `Bearer ${key}`
    if (session) headers["Mcp-Session-Id"] = session
    if (version === modern) {
      headers["Mcp-Method"] = method
      params = { ...params, _meta: {
        "io.modelcontextprotocol/protocolVersion": version,
        "io.modelcontextprotocol/clientInfo": { name: "skyshield-diagnostics", version: "1" },
        "io.modelcontextprotocol/clientCapabilities": {},
      } }
    }
    const response = await fetch(endpoint, {
      method: "POST", headers, signal, redirect: "error", credentials: "omit", cache: "no-store",
      body: JSON.stringify({ jsonrpc: "2.0", ...(notification ? {} : { id: ++id }), method, params }),
    })
    if (!response.ok) {
      const failures: Record<number, DiagnosticState> = { 400: "protocol", 401: "auth", 403: "forbidden", 404: "route", 405: "route", 406: "protocol", 408: "timeout", 415: "protocol", 429: "busy", 502: "upstream", 504: "timeout" }
      throw new ProbeFailure(failures[response.status] ?? "upstream", response.status)
    }
    session = response.headers.get("Mcp-Session-Id") ?? session
    if (notification) return {}
    if (!response.headers.get("Content-Type")?.includes("application/json")) throw new ProbeFailure("route", response.status)
    const message = await response.json().catch(() => { throw new ProbeFailure("protocol", response.status) })
    if (message.error || typeof message.result !== "object" || message.result === null) throw new ProbeFailure("protocol", response.status)
    return message.result
  }
  try {
    try {
      const hello = await rpc("initialize", { protocolVersion: version, capabilities: {}, clientInfo: { name: "skyshield-diagnostics", version: "1" } })
      if (typeof hello.protocolVersion !== "string") throw new ProbeFailure("protocol", 200)
      version = hello.protocolVersion
      await rpc("notifications/initialized", {}, true)
    } catch (error) {
      if (!(error instanceof ProbeFailure) || error.state !== "protocol") throw error
      version = modern
      const hello = await rpc("server/discover")
      if (!Array.isArray(hello.supportedVersions) || !hello.supportedVersions.includes(modern)) throw new ProbeFailure("protocol", 200)
    }
    stage = "tools"
    let cursor = ""
    const cursors = new Set<string>()
    const names = new Set<string>()
    do {
      const list = await rpc("tools/list", cursor ? { cursor } : {})
      if (!Array.isArray(list.tools)) throw new ProbeFailure("protocol", 200)
      for (const tool of list.tools) {
        if (typeof tool.name !== "string") throw new ProbeFailure("protocol", 200)
        names.add(tool.name)
      }
      cursor = list.nextCursor ?? ""
      if (cursor && (typeof cursor !== "string" || cursors.has(cursor))) throw new ProbeFailure("protocol", 200)
      cursors.add(cursor)
    } while (cursor)
    return { state: "ready", stage, version, status: 200, count: names.size }
  } catch (error) {
    if (error instanceof ProbeFailure) return { state: error.state, stage, status: error.status, version }
    return { state: signal.aborted ? "timeout" : "network", stage, version }
  }
}
