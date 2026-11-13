import React from "react"
import { fireEvent, render, screen, within } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it, vi } from "vitest"
import { FindingTrafficPanel } from "../task-traffic"

const requests = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock("@/lib/api-client", () => ({ api: requests }))
vi.mock("@/hooks/use-auth", () => ({ useAuth: () => ({ data: { user: { id: 7 } } }) }))

describe("Finding traffic raw packets", () => {
  it("loads both sides and labels subsequent body pages without repeating HTTP headers", async () => {
    requests.get.mockImplementation(async (url: string, options?: { params: { side: string; offset: number } }) => {
      if (!url.endsWith("/body")) return { data: { version: 1, bindings: [{ id: "17", role: "proof", note: "saved proof", snapshot: { method: "POST", url: "https://example.test/api", status: 200 } }] } }
      if (options?.params.side === "request") return { data: { head: "POST /api HTTP/1.1\r\nHost: example.test\r\n", data: btoa("input"), offset: 0, total: 5 } }
      const offset = options?.params.offset ?? 0
      return { data: { head: "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\n", data: btoa(offset ? "END" : "x".repeat(8192)), offset, total: 8195 } }
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><FindingTrafficPanel taskId="3" findingId="4" readOnly /></QueryClientProvider>)
    fireEvent.click(await screen.findByRole("button", { name: "details" }))
    await screen.findByText("POST /api HTTP/1.1")
    await screen.findByText("HTTP/1.1 200 OK")
    const request = screen.getByRole("region", { name: "request" })
    expect(request.querySelector("[data-http-raw]")?.textContent).toBe("POST /api HTTP/1.1\r\nHost: example.test\r\n\r\ninput")
    const response = screen.getByRole("region", { name: "response" }).parentElement!
    expect(within(response).getByRole("button", { name: "copyPartial" })).toBeVisible()
    fireEvent.click(within(response).getByRole("button", { name: "next" }))
    await within(response).findByText("END")
    expect(response.querySelector("[data-http-raw]")?.textContent).toBe("END")
    expect(within(response).queryByText("HTTP/1.1 200 OK")).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "remove" })).not.toBeInTheDocument()
  })
})
