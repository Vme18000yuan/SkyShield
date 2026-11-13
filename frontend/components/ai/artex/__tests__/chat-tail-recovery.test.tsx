import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import ChatPage from "../chat-page"

const fixtures = vi.hoisted(() => ({ api: { agents: vi.fn(), chatLLMProfiles: vi.fn(), conversations: vi.fn(), conversationHistory: vi.fn(), conversationMessages: vi.fn() } }))
vi.mock("@/services/artex-ai.service", () => ({ api: fixtures.api }))
vi.mock("@/hooks/artex/use-side-questions", () => ({ useSideQuestions: () => ({ handleCommand: () => false }) }))
vi.mock("../side-question-workspace", () => ({ SideQuestionButton: () => null, SideQuestionWorkspace: ({ children }: { children: React.ReactNode }) => children }))
vi.mock("../approval-execution-focus", () => ({ ApprovalExecutionFocus: () => null, useApprovalFocus: () => ({ state: null }), useApprovalHistory: () => ({ ready: true }) }))
vi.mock("../transcript", () => ({ Transcript: ({ activity }: { activity: { seq: number }[] }) => <div data-testid="transcript-tail">{activity.at(-1)?.seq ?? 0}</div> }))

describe("chat terminal backlog", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    window.history.replaceState(null, "", "/ai/chat/")
    fixtures.api.agents.mockResolvedValue([])
    fixtures.api.chatLLMProfiles.mockResolvedValue([])
    fixtures.api.conversations.mockResolvedValue([{ id: "11111111-1111-4111-8111-111111111111", title: "尾页恢复测试", agent_key: "mainagent", running: false, pinned: false, created_at: "2026-11-13T00:00:00Z", updated_at: "2026-11-13T00:00:00Z" }])
    fixtures.api.conversationHistory.mockResolvedValue({ items: [], cursor: 0, running: true, hasMore: false })
  })

  it("keeps polling after running becomes false until all three pages arrive", async () => {
    fixtures.api.conversationMessages.mockImplementation(async (_id: string, cursor: number) => {
      const end = Math.min(cursor + 200, 450)
      return { items: Array.from({ length: end - cursor }, (_, i) => ({ seq: cursor + i + 1, kind: "progress", worker: "mainagent", summary: "fixture", ts: "2026-11-13T00:00:00Z" })), cursor: end, finalCursor: 450, hasMore: end < 450, running: false, runStatus: "completed" }
    })
    render(<ChatPage />)
    fireEvent.click(await screen.findByText("尾页恢复测试"))
    await waitFor(() => expect(screen.getByTestId("transcript-tail")).toHaveTextContent("450"))
    expect(fixtures.api.conversationMessages.mock.calls.map(call => call[1])).toEqual([0, 200, 400])
  })
})
