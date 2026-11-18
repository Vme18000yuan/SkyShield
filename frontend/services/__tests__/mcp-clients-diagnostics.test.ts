import { afterEach, describe, expect, it, vi } from "vitest"
import { diagnoseMcp } from "@/services/mcp-clients.service"

const success = (result: object) => Response.json({ jsonrpc: "2.0", id: 1, result })
afterEach(() => vi.unstubAllGlobals())

describe("external MCP diagnostics", () => {
  it("probes the current public URL and counts only tools exposed to this credential", async () => {
    const methods: string[] = []
    const fetchMock = vi.fn(async (url: URL, request: RequestInit) => {
      expect(String(url)).toBe(new URL("/mcp", window.location.origin).href)
      expect(request.credentials).toBe("omit")
      expect(request.redirect).toBe("error")
      expect(new Headers(request.headers).get("Authorization")).toBe("Bearer test-key")
      const message = JSON.parse(String(request.body))
      methods.push(message.method)
      if (message.method === "initialize") return success({ protocolVersion: "2025-06-18" })
      if (message.method === "notifications/initialized") return new Response(null, { status: 202 })
      return success(message.params.cursor ? { tools: [{ name: "get_vulnerability" }] } : { tools: [{ name: "list_organizations" }], nextCursor: "more" })
    })
    vi.stubGlobal("fetch", fetchMock)
    expect(await diagnoseMcp("test-key", new AbortController().signal)).toMatchObject({ state: "ready", count: 2, version: "2025-06-18" })
    expect(methods).toEqual(["initialize", "notifications/initialized", "tools/list", "tools/list"])
  })

  it.each([[401, "auth"], [403, "forbidden"], [404, "route"], [400, "protocol"], [504, "timeout"]] as const)("reports HTTP %s", async (status, state) => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status })))
    expect(await diagnoseMcp("", new AbortController().signal)).toMatchObject({ state, status, stage: "handshake" })
  })

  it("supports discovery clients and identifies an HTML error page", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(null, { status: 400 }))
      .mockResolvedValueOnce(success({ supportedVersions: ["2026-07-28"] }))
      .mockResolvedValueOnce(success({ tools: [] }))
      .mockResolvedValueOnce(new Response("<html></html>", { headers: { "Content-Type": "text/html" } }))
    vi.stubGlobal("fetch", fetchMock)
    expect(await diagnoseMcp("test-key", new AbortController().signal)).toMatchObject({ state: "ready", count: 0, version: "2026-07-28" })
    expect(new Headers(fetchMock.mock.calls[1][1].headers).get("Mcp-Method")).toBe("server/discover")
    expect(await diagnoseMcp("test-key", new AbortController().signal)).toMatchObject({ state: "route" })
  })
})
