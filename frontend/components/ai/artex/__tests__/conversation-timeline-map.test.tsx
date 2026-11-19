import { fireEvent, render, screen } from "@testing-library/react"
import { expect, it, vi } from "vitest"
import { TimelineMap } from "@/components/ai/conversation-timeline-map"
import type { ConversationTimeline, TimelineFinding } from "@/types/conversation-exploration.types"

const capture = vi.hoisted(() => vi.fn())
vi.mock("@xyflow/react", () => ({
  ReactFlow: (props: unknown) => { capture(props); return null },
  Background: () => null, Controls: () => null, Handle: () => null, MiniMap: () => null, Panel: () => null,
  useNodesState: (nodes: unknown[]) => [nodes, vi.fn(), vi.fn()],
  MarkerType: { ArrowClosed: "arrow" }, Position: { Top: "top", Bottom: "bottom", Left: "left", Right: "right" },
}))
vi.mock("next-intl", () => ({ useTranslations: () => Object.assign((key: string) => key, { has: () => true }) }))
it("shows a distinct source branch for each receipt in the same range", () => {
  const counts = { steps: 1, failed: 0, returned: 1, interrupted: 0, missing: 0, pending: 0, measured: 0, overlap: 0 }
  const finding = (id: number, source: number): TimelineFinding => ({ receiptId: String(id), vulnerabilityId: id, title: "Recorded " + id, severity: "low", reviewed: false, step: 30 + id, evidenceSources: [{ step: source, activityId: String(source) }] })
  const a = finding(3, 1), b = finding(4, 17)
  b.evidenceSources?.push({ step: 33, activityId: "33" })
  const graph: ConversationTimeline = {
    run: null, runs: [], request: { text: "Review" }, conclusion: { text: "Done" }, counts,
    sections: [1, 17, 33].map(first => ({ ...counts, first, last: first + 15, tool: "Read", findings: first === 33 ? 2 : 0 })),
    findings: [a,b], findingCount: 2, steps: [], checks: [], checksTruncated: false,
  }
  const receipts=vi.fn()
  render(<TimelineMap graph={graph} from={1} choose={vi.fn()} goal={vi.fn()} outcome={vi.fn()} receipts={receipts} />)
  const {edges,nodes}=capture.mock.calls.at(-1)![0]
  const origins=(target: string) => edges.filter((e: { target:string }) => e.target===target).map((e: {source:string})=>e.source).sort()
  expect(origins("receipt-3")).toEqual(["section-1","section-33"])
  expect(origins("receipt-4")).toEqual(["section-17","section-33","section-33"])
  const ownLinks = edges.filter((edge: { source: string; target: string }) => edge.source === "section-33" && edge.target === "receipt-4")
  expect(ownLinks.map((edge: { targetHandle: string }) => edge.targetHandle).sort()).toEqual(["citation", "in"])
  expect(new Set(ownLinks.map((edge: { id: string }) => edge.id)).size).toBe(2)
  const nodeX = (id: string) => nodes.find((node: { id: string }) => node.id === id).position.x
  expect(nodeX("receipt-3")).toBeGreaterThan(nodeX("section-33"))
  expect(nodeX("receipt-4")).toBeGreaterThan(nodeX("section-17"))
  nodes.find((n: {id:string})=>n.id==="receipt-4").data.open()
  expect(receipts).toHaveBeenCalledWith(b)
})

it("titles a task node with its recorded goal and keeps command and range out of the headline", () => {
  const counts = { steps: 4, failed: 0, returned: 4, interrupted: 0, missing: 0, pending: 0, measured: 0, overlap: 0 }
  const graph: ConversationTimeline = {
    run: null, runs: [], request: { text: "Review" }, conclusion: { text: "" }, counts,
    sections: [{ ...counts, first: 1, last: 4, tool: "Bash", title: "ls", findings: 0,
      purpose: { source: "todo", text: "核对组织资产台账", basis: [{ step: 2, activityId: "12" }] } }],
    findings: [], findingCount: 0, steps: [], checks: [], checksTruncated: false,
  }
  render(<TimelineMap graph={graph} from={1} choose={vi.fn()} goal={vi.fn()} outcome={vi.fn()} receipts={vi.fn()} />)
  const card = capture.mock.calls.at(-1)![0].nodes.find((n: { id: string }) => n.id === "section-1")
  expect(card.data).toMatchObject({ title: "核对组织资产台账", label: "taskPurpose.sources.todo", meta: "range" })
})

it("pages through a long trace without jumping to the step list or inventing off-page relationships", () => {
  const counts = { steps: 1, failed: 0, returned: 1, interrupted: 0, missing: 0, pending: 0, measured: 0, overlap: 0 }
  const graph: ConversationTimeline = {
    run: null, runs: [], request: { text: "Start" }, conclusion: { text: "Done" }, counts,
    sections: Array.from({ length: 17 }, (_, i) => ({ ...counts, first: i + 1, last: i + 1, tool: "Read", findings: 0 })),
    findings: [], findingCount: 0, steps: [], checks: [], checksTruncated: false,
  }
  const props = { graph, choose: vi.fn(), goal: vi.fn(), outcome: vi.fn(), receipts: vi.fn() }
  const view = render(<TimelineMap {...props} from={1} />)
  expect(screen.getByRole("button", { name: "taskPurpose.earlier" })).toBeDisabled()
  fireEvent.click(screen.getByRole("button", { name: "taskPurpose.later" }))
  expect(props.choose).toHaveBeenCalledWith(7, false)
  view.rerender(<TimelineMap {...props} from={13} />)
  const { nodes, edges } = capture.mock.calls.at(-1)![0]
  const ids = nodes.map((node: { id: string }) => node.id)
  expect(ids).toContain("outcome")
  expect(ids).toContain("request")
  expect(edges.every((edge: { source: string; target: string }) => ids.includes(edge.source) && ids.includes(edge.target))).toBe(true)
  expect(screen.getByRole("button", { name: "taskPurpose.later" })).toBeDisabled()
})
