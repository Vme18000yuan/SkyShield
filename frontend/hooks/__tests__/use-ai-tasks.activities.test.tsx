import { act, renderHook, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { useAITaskActivities } from "@/hooks/use-ai-tasks"
import { AITaskService } from "@/services/ai-task.service"
import type { AITaskActivity } from "@/types/ai-task.types"

vi.mock("@/hooks/use-auth", () => ({ useAuth: () => ({ data: { user: { id: 7 } } }) }))
vi.mock("@/services/ai-task.service", () => ({ AITaskService: { activities: vi.fn() } }))

function activity(id: string, detail: string): AITaskActivity[] {
  return [{ id, nodeId: null, worker: "worker", kind: "text", tool: "", toolUseId: "", isError: false,
    summary: "", detail, createdAt: "2026-01-01T00:00:00Z" }]
}

describe("AI task activity polling", () => {
  beforeEach(() => vi.resetAllMocks())
  it("discards late activity from the previous task", async () => {
    let resolveFirst!: (page: AITaskActivity[]) => void
    const first = new Promise<AITaskActivity[]>(resolve => { resolveFirst = resolve })
    vi.mocked(AITaskService.activities).mockImplementation(id => id === "1" ? first : Promise.resolve(activity("2", "second")))
    const hook = renderHook(({ id }) => useAITaskActivities(id), { initialProps: { id: "1" } })
    hook.rerender({ id: "2" })
    await waitFor(() => expect(hook.result.current.items.map(item => item.detail)).toEqual(["second"]))
    await act(async () => { resolveFirst(activity("1", "first")); await first })
    expect(hook.result.current.items.map(item => item.detail)).toEqual(["second"])
    hook.unmount()
  })
  it("clears stale activity on a failed refresh and restarts from the beginning", async () => {
    vi.mocked(AITaskService.activities).mockResolvedValueOnce(activity("2", "visible"))
      .mockRejectedValueOnce(new Error("scope revoked"))
      .mockResolvedValueOnce(activity("3", "restored"))
    const hook = renderHook(() => useAITaskActivities("1"))
    await waitFor(() => expect(hook.result.current.items.map(item => item.detail)).toEqual(["visible"]))
    await waitFor(() => expect(hook.result.current.error).toBeTruthy(), { timeout: 2500 })
    expect(hook.result.current.items).toEqual([])
    await waitFor(() => expect(hook.result.current.items.map(item => item.detail)).toEqual(["restored"]), { timeout: 2500 })
    expect(vi.mocked(AITaskService.activities).mock.calls.slice(0, 3)).toEqual([["1", "0"], ["1", "2"], ["1", "0"]])
    hook.unmount()
  })
})
