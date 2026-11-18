import { describe, expect, it } from "vitest"
import { groupSteps } from "@/components/ai/conversation-timeline-steps"
import { timelineCaption } from "@/components/ai/conversation-timeline-labels"
import type { TimelineStep } from "@/types/conversation-exploration.types"

const step = (id: string, overrides: Partial<TimelineStep> = {}): TimelineStep => ({
  activityId: id, number: Number(id), tool: "Read", subject: "inventory.txt", state: "succeeded", recordedAt: "2026-01-01T00:00:00Z", overlap: false, ...overrides,
})
describe("conversation timeline presentation", () => {
  it("keeps every original occurrence reachable when folding consecutive similar steps", () => {
    const steps = [step("1"), step("2"), step("3", { overlap: true }), step("4", { state: "interrupted" }), step("5", { batch: 1 }), step("6", { batch: 2 })]
    const groups = groupSteps(steps)
    expect(groups.map(group => group.length)).toEqual([2, 1, 1, 1, 1])
    expect(groups.flat()).toEqual(steps)
  })
  it("keeps individual finding receipts visible and prefers the recorded description", () => {
    const steps = [step("1"), step("2", { finding: { receiptId: "9", vulnerabilityId: 4, title: "Stored finding", severity: "low", reviewed: false } })]
    expect(groupSteps(steps)).toHaveLength(2)
    expect(timelineCaption(key => key, step("1", { action: "Compare inventory counts" }))).toBe("Compare inventory counts")
    expect(timelineCaption(key => key, step("1", { tool: "Bash", subject: "inventory.example", actionKind: "http" }))).toBe("actions.http · inventory.example")
  })
})
