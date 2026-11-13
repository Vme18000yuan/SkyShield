import { describe, expect, it, vi } from "vitest"
import { chatAPI } from "../artex-chat.service"

const mocks = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock("@/lib/api-client", () => ({ api: mocks, apiClient: mocks, ensurePrimaryTokenFresh: vi.fn() }))

describe("historical review conversations", () => {
  it("preserves the server read-only flag in the chat model", async () => {
    mocks.get.mockResolvedValueOnce({ data: { conversations: [{ id: "review", title: "Review", agentKey: "reporter", pinned: false, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z", running: false, reviewReadOnly: true }] } })
    const conversations = await chatAPI.conversations()
    expect(conversations[0].review_read_only).toBe(true)
  })
})
