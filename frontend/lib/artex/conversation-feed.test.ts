import { describe, expect, it } from "vitest"
import { hasUnreadConversationEvents, conversationStatusMessage } from "./conversation-feed"
import { mergeActivities } from "./activity-merge"
import type { Activity } from "@/types/artex-ai.types"

describe("conversation recovery", () => {
  it("drains all unread pages after a completed run", () => {
    for (const cursor of [200, 400]) expect(hasUnreadConversationEvents({ items: [], cursor, finalCursor: 450, running: false })).toBe(true)
    expect(hasUnreadConversationEvents({ items: [], cursor: 450, finalCursor: 450, running: false })).toBe(false)
  })
  it("does not present an interrupted or exhausted run as completion", () => {
    expect(conversationStatusMessage({ items: [], cursor: 0, running: false, runStatus: "interrupted" })).toContain("中断")
    expect(conversationStatusMessage({ items: [], cursor: 0, running: false, runStatus: "exhausted" })).toContain("预算")
  })
  it("merges overlap, reordering and newer versions without duplicate rows", () => {
    const row = (seq: number, summary = "old") => ({ seq, summary }) as Activity
    const merged = mergeActivities([row(3), row(1)], [row(2), row(3, "new"), row(4)])
    expect(merged.map(item => item.seq)).toEqual([1, 2, 3, 4])
    expect(merged[2].summary).toBe("new")
  })
})
