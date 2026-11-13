import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import ChatPage from "../chat-page"

const fixtures = vi.hoisted(() => ({
  api: {
    agents: vi.fn(),
    chatLLMProfiles: vi.fn(),
    conversations: vi.fn(),
    deleteConversation: vi.fn(),
    renameConversation: vi.fn(),
    pinConversation: vi.fn(),
  },
  toast: { error: vi.fn(), warning: vi.fn(), success: vi.fn() },
}))
vi.mock("@/services/artex-ai.service", () => ({ api: fixtures.api }))
vi.mock("sonner", () => ({ toast: fixtures.toast }))

const conversation = {
  id: "11111111-1111-4111-8111-111111111111",
  title: "删除测试对话",
  agent_key: "auto",
  pinned: false,
  running: false,
  created_at: "2026-11-04T00:00:00Z",
  updated_at: "2026-11-04T00:00:00Z",
}

async function openMenu() {
  fireEvent.click(await screen.findByRole("button", { name: "管理对话「删除测试对话」" }))
  return await screen.findByRole("menu")
}

describe("AI conversation actions", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    window.history.replaceState(null, "", "/ai/chat/")
    fixtures.api.agents.mockResolvedValue([])
    fixtures.api.chatLLMProfiles.mockResolvedValue([])
    fixtures.api.conversations.mockResolvedValue([conversation])
    fixtures.api.renameConversation.mockResolvedValue(undefined)
    fixtures.api.pinConversation.mockResolvedValue(undefined)
  })

  it("opens confirmation and removes a confirmed deletion even if refresh fails", async () => {
    fixtures.api.deleteConversation.mockImplementation(async () => {
      fixtures.api.conversations.mockRejectedValue(new Error("offline"))
      return { items: [{ id: conversation.id, ok: true }] }
    })
    render(<ChatPage />)
    fireEvent.click(within(await openMenu()).getByRole("menuitem", { name: "删除" }))
    const dialog = await screen.findByRole("alertdialog")
    expect(within(dialog).getByText("删除对话「删除测试对话」？")).toBeVisible()
    fireEvent.click(within(dialog).getByRole("button", { name: "删除" }))
    await waitFor(() => expect(fixtures.api.deleteConversation).toHaveBeenCalledWith(conversation.id))
    await waitFor(() => expect(screen.queryByRole("button", { name: "管理对话「删除测试对话」" })).not.toBeInTheDocument())
  })

  it("keeps the conversation and reports a rejected deletion", async () => {
    fixtures.api.deleteConversation.mockResolvedValue({ items: [{ id: conversation.id, ok: false, error: "对话不可删除" }] })
    render(<ChatPage />)
    fireEvent.click(within(await openMenu()).getByRole("menuitem", { name: "删除" }))
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "删除" }))
    await waitFor(() => expect(fixtures.toast.error).toHaveBeenCalledWith("删除失败：对话不可删除"))
    expect(screen.getByRole("button", { name: "管理对话「删除测试对话」" })).toBeVisible()
  })

  it("opens rename and dispatches pin from the same menu", async () => {
    render(<ChatPage />)
    fireEvent.click(within(await openMenu()).getByRole("menuitem", { name: "重命名" }))
    const input = await screen.findByDisplayValue("删除测试对话")
    fireEvent.change(input, { target: { value: "新名称" } })
    fireEvent.keyDown(input, { key: "Enter" })
    await waitFor(() => expect(fixtures.api.renameConversation).toHaveBeenCalledWith(conversation.id, "新名称"))
    fireEvent.click(within(await openMenu()).getByRole("menuitem", { name: "置顶" }))
    await waitFor(() => expect(fixtures.api.pinConversation).toHaveBeenCalledWith(conversation.id, true))
  })
})
