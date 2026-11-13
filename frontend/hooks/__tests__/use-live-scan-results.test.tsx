import { act, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import { useLiveScanResults } from "@/hooks/use-live-scan-results"

const mocks = vi.hoisted(() => ({ refetch: vi.fn().mockResolvedValue(undefined) }))
vi.mock("@tanstack/react-query", () => ({ useQueryClient: () => ({ refetchQueries: mocks.refetch }) }))
vi.mock("@/hooks/use-scans", () => ({ scanKeys: { detail: (id: number) => ["scans", "detail", id] } }))

beforeEach(() => { vi.useFakeTimers(); mocks.refetch.mockClear(); Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" }) })
afterEach(() => { vi.useRealTimers() })

it("refreshes visible running results and stops after the final refresh", () => {
  const { rerender, unmount } = renderHook(({ status }) => useLiveScanResults(7, status), { initialProps: { status: "running" } })
  expect(mocks.refetch).toHaveBeenCalledTimes(2)
  act(() => vi.advanceTimersByTime(3000))
  expect(mocks.refetch).toHaveBeenCalledTimes(4)
  rerender({ status: "completed" })
  expect(mocks.refetch).toHaveBeenCalledTimes(6)
  act(() => vi.advanceTimersByTime(9000))
  expect(mocks.refetch).toHaveBeenCalledTimes(6)
  unmount()
})

it("pauses while hidden and refreshes when the page becomes visible", () => {
  Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" })
  const { unmount } = renderHook(() => useLiveScanResults(7, "running"))
  act(() => vi.advanceTimersByTime(9000))
  expect(mocks.refetch).not.toHaveBeenCalled()
  Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" })
  act(() => document.dispatchEvent(new Event("visibilitychange")))
  expect(mocks.refetch).toHaveBeenCalledTimes(2)
  unmount()
})
