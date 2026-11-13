import { act, renderHook, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { useAIActivities } from "@/hooks/use-ai-chat"
import { AIChatService } from "@/services/ai-chat.service"
import type { AIActivityPage } from "@/types/ai-chat.types"

vi.mock("@/services/ai-chat.service", () => ({
  AIChatService: { activities: vi.fn() },
}))

function activity(id: string, detail: string): AIActivityPage {
  return { activities: [{ id, runId: "run", kind: "text", tool: "", toolUseId: "", isError: false,
    summary: "", detail, createdAt: "2026-01-01T00:00:00Z" }], running: false, runStatus: "completed", errorMessage: "" }
}

describe("AI conversation activity polling", () => {
  beforeEach(() => vi.resetAllMocks())

  it("ignores a late response from a previously selected conversation", async () => {
    let resolveFirst!: (page: AIActivityPage) => void
    const first = new Promise<AIActivityPage>(resolve => { resolveFirst = resolve })
    vi.mocked(AIChatService.activities).mockImplementation(id => id === "first" ? first : Promise.resolve(activity("2", "second")))
    const hook = renderHook(({ id }) => useAIActivities(id), { initialProps: { id: "first" as string | null } })
    hook.rerender({ id: "second" })
    await waitFor(() => expect(hook.result.current.items.map(item => item.detail)).toEqual(["second"]))
    await act(async () => { resolveFirst(activity("1", "first")); await first })
    expect(hook.result.current.items.map(item => item.detail)).toEqual(["second"])
    hook.unmount()
  })
})
