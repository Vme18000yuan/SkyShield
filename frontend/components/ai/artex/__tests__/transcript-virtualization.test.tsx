import { render, waitFor } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"
import { Transcript } from "../transcript"
import type { Activity } from "@/types/artex-ai.types"

afterEach(() => vi.restoreAllMocks())

it("keeps the DOM bounded when a long transcript is loaded", async () => {
  vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(600)
  vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(800)
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ top: 0, left: 0, bottom: 600, right: 800, width: 800, height: 600, x: 0, y: 0, toJSON: () => ({}) })
  const activity = Array.from({ length: 2000 }, (_, i) => ({ seq: i + 1, kind: i % 2 ? "result" : "user", worker: "mainagent", summary: `row ${i}`, ts: "2026-11-13T00:00:00Z" })) as Activity[]
  const fetchDetail = vi.fn(async () => "fixture body")
  const { container } = render(<div data-slot="scroll-area-viewport"><Transcript activity={activity} chat fetchDetail={fetchDetail} /></div>)
  await waitFor(() => {
    const mountedRows = container.querySelectorAll("[data-index]").length
    expect(mountedRows).toBeGreaterThan(0)
    expect(mountedRows).toBeLessThan(60)
  })
  expect(fetchDetail.mock.calls.length).toBeLessThan(60)
})
