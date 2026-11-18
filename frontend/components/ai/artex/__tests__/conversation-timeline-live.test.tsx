import { act, fireEvent, screen, within } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { ConversationExploration } from "@/components/ai/conversation-exploration"
import { renderWithProviders } from "@/test/utils/render-with-providers"
import type { ConversationTimeline } from "@/types/conversation-exploration.types"

const { read, readDetail } = vi.hoisted(() => ({ read: vi.fn(), readDetail: vi.fn() }))
vi.mock("@/hooks/use-auth", () => ({ useAuth: () => ({ data: { user: { id: 7 } } }) }))
vi.mock("@/services/conversation-exploration.service", () => ({ getConversationTimeline: read, getConversationExplorationDetail: readDetail }))
vi.mock("@/components/ai/conversation-timeline-map", () => ({ TimelineMap: () => null }))
vi.mock("next-intl", () => ({ useLocale: () => "en", useTranslations: () => Object.assign((key: string) => key, { has: () => true }) }))

describe("live step detail", () => {
  it("shows the purpose above the steps and opens its own source on demand", async () => {
    const run = { id: "r", status: "completed", createdAt: "2026-01-01T00:00:00Z", running: false }
    const counts = { steps: 1, returned: 1, failed: 0, pending: 0, interrupted: 0, missing: 0, measured: 0, overlap: 0 }
    const graph: ConversationTimeline = { run, runs: [run], request: { text: "Summary" }, conclusion: { text: "" }, counts,
      sections: [{ ...counts, first: 1, last: 1, tool: "Bash", findings: 0, purpose: { source: "statement", text: "Compare invoice totals", basis: [{ step: 0, activityId: "19" }] } }],
      steps: [], checks: [], checksTruncated: false, findings: [], findingCount: 0 }
    read.mockResolvedValue(graph)
    readDetail.mockResolvedValue("Current task: Compare invoice totals")
    renderWithProviders(<ConversationExploration conversationId="purpose" running={false} />)
    expect(await screen.findByRole("heading", { name: "Compare invoice totals" })).toBeVisible()
    expect(readDetail).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole("button", { name: "taskPurpose.basisRecord" }))
    const drawer = await screen.findByRole("dialog")
    const summary = within(drawer).getByText("#19").closest("summary")!
    fireEvent.click(summary)
    expect(await within(drawer).findByText("Current task: Compare invoice totals")).toBeVisible()
    expect(readDetail).toHaveBeenCalledWith("purpose", "19", expect.any(AbortSignal))
  })
  it("restores the pinned run and step for the current user and conversation", async () => {
    const id="10000000-0000-4000-9000-000000000033"
    const key="skyshield:exploration:7:pinned"
    sessionStorage.setItem(key,JSON.stringify({run:id,from:33}))
    try {
      read.mockResolvedValue({run:null,runs:[],sections:[]})
      const view=renderWithProviders(<ConversationExploration conversationId="pinned" running={false} />)
      await screen.findByText("empty")
      expect(read).toHaveBeenCalledWith("pinned",id,"",33,"",expect.any(AbortSignal))
      view.unmount()
    } finally { sessionStorage.removeItem(key) }
  })
  it("attaches a delayed result to the same open drawer", async () => {
    const run = { id: "run", status: "running", createdAt: "2026-01-01T00:00:00Z", running: true }
    const counts = { steps: 1, returned: 0, failed: 0, pending: 1, interrupted: 0, missing: 0, measured: 0, overlap: 0 }
    const fixture: ConversationTimeline = {
      run, runs: [run], request: { text: "User requirement" }, conclusion: { text: "" }, counts, sections: [],
      checks: [], checksTruncated: false, findings: [], findingCount: 0,
      steps: [{ activityId: "4", number: 1, tool: "Read", action: "Inspect fixture", state: "running", recordedAt: run.createdAt, overlap: false }],
    }
    read.mockResolvedValue(fixture)
    const { queryClient } = renderWithProviders(<ConversationExploration conversationId="fixture" running />)
    fireEvent.click(await screen.findByText("Inspect fixture"))
    const drawer = await screen.findByRole("dialog")
    expect(within(drawer).queryByText("returnRecord")).not.toBeInTheDocument()
    act(() => { queryClient.setQueryData(["conversation-timeline", 7, "fixture", "", "", 1, ""], { ...fixture, steps: [{ ...fixture.steps[0], resultId: "8", state: "succeeded" }] }) })
    expect(await within(drawer).findByText("returnRecord")).toBeInTheDocument()
    expect(within(drawer).getByText("#8")).toBeInTheDocument()
  })
})
