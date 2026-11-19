import React from "react"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { OASTPanel } from "../oast-panel"

const transport = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock("@/lib/api-client", () => ({ api: transport }))
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const probe = { id: "probe-1", label: "Avatar import", httpUrl: "https://callback.test/oast/secret", dnsName: "secret.cb.test", createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 3600_000).toISOString(), state: "active", eventCount: 1 }
function mount(readOnly = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><OASTPanel scope={{ kind: "conversation", id: "chat-1" }} readOnly={readOnly} /></QueryClientProvider>)
}
beforeEach(() => {
  vi.clearAllMocks()
  transport.get.mockImplementation(async (url: string) => ({ data: url.endsWith("/events")
    ? { probe, items: [{ id: "1", protocol: "http", remoteAddr: "192.0.2.1", method: "POST", path: "/test", headers: "X-Test: evidence", bodyBase64: btoa('<img src="https://attacker.test" onerror="alert(1)">'), receivedAt: new Date().toISOString(), truncated: true }], nextAfter: "1", hasMore: false }
    : { items: [probe], nextBefore: "", enabled: true, dnsEnabled: true } }))
})

describe("Callback monitor", () => {
  it("shows untrusted evidence as text and never links or visits callback addresses", async () => {
    const view = mount(true)
    fireEvent.click(await screen.findByRole("button", { name: /Avatar import/ }))
    expect(await screen.findByText(probe.httpUrl)).toBeInTheDocument()
    fireEvent.click(screen.getByText(/192.0.2.1/, { selector: "summary" }))
    expect(screen.getByText(/<img src=/)).toBeInTheDocument()
    expect(screen.getByText("truncated")).toBeInTheDocument()
    expect(view.container.querySelector("img")).toBeNull()
    expect(view.container.querySelector(`a[href="${probe.httpUrl}"]`)).toBeNull()
    expect(screen.queryByRole("button", { name: "revoke" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "create" })).not.toBeInTheDocument()
    expect(transport.get.mock.calls.every(([url]) => String(url).startsWith("/aiConversations/chat-1/oastProbes"))).toBe(true)
  })

  it("reuses the request ID after a failed create and revokes through the scoped API", async () => {
    transport.post.mockRejectedValueOnce(new Error("lost response")).mockResolvedValueOnce({ data: probe })
    transport.delete.mockResolvedValue({})
    mount()
    const create = await screen.findByRole("button", { name: "create" })
    fireEvent.change(screen.getByLabelText("label"), { target: { value: "Avatar import" } })
    fireEvent.click(create)
    await waitFor(() => expect(transport.post).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(screen.getByRole("button", { name: "create" })).toBeEnabled())
    fireEvent.click(screen.getByRole("button", { name: "create" }))
    await screen.findByText(probe.httpUrl)
    expect(transport.post.mock.calls[1][1].requestId).toBe(transport.post.mock.calls[0][1].requestId)
    expect(transport.post.mock.calls[1][0]).toBe("/aiConversations/chat-1/oastProbes")
    fireEvent.click(screen.getByRole("button", { name: "revoke" }))
    await waitFor(() => expect(transport.delete).toHaveBeenCalledWith("/aiConversations/chat-1/oastProbes/probe-1"))
  })

  it("polls from the last cursor and retains previously loaded evidence", async () => {
    transport.get.mockImplementation(async (url: string, options?: { params?: { after?: string } }) => {
      if (!url.endsWith("/events")) return { data: { items: [probe], nextBefore: "", enabled: true, dnsEnabled: true } }
      const after = options?.params?.after
      return { data: { probe, items: [{ id: after ? "2" : "1", protocol: "dns", remoteAddr: "192.0.2.1", queryName: after ? "second.cb.test" : "first.cb.test", queryType: "A", receivedAt: new Date().toISOString(), truncated: false }], nextAfter: after ? "2" : "1", hasMore: false } }
    })
    mount(true)
    fireEvent.click(await screen.findByRole("button", { name: /Avatar import/ }))
    await screen.findByText(/first.cb.test/)
    fireEvent.click(within(screen.getByRole("region", { name: "evidence" })).getByRole("button", { name: "refresh" }))
    await screen.findByText(/second.cb.test/)
    expect(screen.getAllByText(/first.cb.test/)).toHaveLength(1)
    expect(transport.get.mock.calls.at(-1)?.[1].params.after).toBe("1")
  })
})
