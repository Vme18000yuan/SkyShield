import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { TaskApprovalExecution } from "../task-approval-execution"

const requests = vi.hoisted(() => ({ interceptExecution: vi.fn(), interceptExecutionContext: vi.fn() }))
vi.mock("@/hooks/use-auth", () => ({ useAuth: () => ({ data: { user: { id: 7 } } }) }))
vi.mock("@/services/artex-ai.service", () => ({ api: requests }))
vi.mock("../transcript", () => ({ Transcript: ({ activity, focusedSeq }: { activity: Array<{ seq: number; summary: string }>; focusedSeq: number }) => <div data-testid="transcript" data-focus={focusedSeq}>{activity.map(item => <p key={item.seq}>{item.summary}</p>)}</div> }))

const item = (seq: number, kind = "text") => ({ seq, worker: "work#1", kind, ts: "2026-01-01T00:00:00Z", summary: `step-${seq}` })

describe("task approval execution focus", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    window.history.replaceState(null, "", "/ai/tasks/30/?tab=activity&approval=50")
    requests.interceptExecution.mockResolvedValue({ conversation_id: null, task_id: "30", session: "intent:9", seq: 100, items: [item(100, "tool_use")] })
    requests.interceptExecutionContext.mockResolvedValue({ items: [item(98), item(99), item(100, "tool_use"), item(101, "tool_result")], has_before: true, has_after: true })
  })
  afterEach(() => window.history.replaceState(null, "", "/"))

  it("opens the exact call and loads surrounding pages", async () => {
    render(<TaskApprovalExecution taskId="30" />)
    expect(await screen.findByText("step-100")).toBeVisible()
    expect(screen.getByTestId("transcript")).toHaveAttribute("data-focus", "100")
    requests.interceptExecutionContext.mockResolvedValueOnce({ items: [item(96), item(97)], has_before: false, has_after: true })
    fireEvent.click(screen.getByRole("button", { name: "加载更早记录" }))
    await screen.findByText("step-96")
    expect(requests.interceptExecutionContext).toHaveBeenCalledWith(50, 98, undefined)
    expect(screen.getByRole("button", { name: "加载更早记录" })).toBeDisabled()
    expect(screen.getByText("step-100")).toBeVisible()
  })

  it("rejects a source from another task", async () => {
    requests.interceptExecution.mockResolvedValueOnce({ conversation_id: null, task_id: "99", session: "plan", seq: 100, items: [item(100, "tool_use")] })
    render(<TaskApprovalExecution taskId="30" />)
    expect(await screen.findByRole("alert")).toHaveTextContent("审批来源与当前任务不一致")
    expect(requests.interceptExecutionContext).not.toHaveBeenCalled()
  })

  it("clears previously loaded context when access is lost", async () => {
    render(<TaskApprovalExecution taskId="30" />)
    await screen.findByText("step-100")
    requests.interceptExecutionContext.mockRejectedValueOnce(new Error("访问已失效"))
    fireEvent.click(screen.getByRole("button", { name: "加载更早记录" }))
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("访问已失效"))
    expect(screen.queryByText("step-100")).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "关闭定位" }))
    expect(window.location.search).not.toContain("approval")
  })
})
