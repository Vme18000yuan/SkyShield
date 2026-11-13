import { fireEvent, render, screen } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import type { ReactNode } from "react"

import { OverviewTokenUsage } from "@/components/overview/overview-token-usage"

const mocks = vi.hoisted(() => ({ auth: vi.fn(), usage: vi.fn(), models: vi.fn(), retry: vi.fn() }))
vi.mock("@/hooks/use-auth", () => ({ useAuth: mocks.auth }))
vi.mock("@/hooks/use-ai-usage", () => ({ useAIUsageSummary: mocks.usage, useAIUsageModelOptions: mocks.models }))
vi.mock("recharts", () => ({
  ResponsiveContainer: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  BarChart: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Bar: () => null, CartesianGrid: () => null, Tooltip: () => null, XAxis: () => null, YAxis: () => null,
}))

describe("OverviewTokenUsage", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    const date = new Date().toISOString().slice(0, 10)
    const first = { profile_id: "a", profile_name: "Model A", calls: 2, tasks: 1, input_tokens: 1000, output_tokens: 200, cache_read_tokens: 400, cache_write_tokens: 50 }
    const second = { profile_id: "b", profile_name: "Model B", calls: 1, tasks: 0, input_tokens: 200, output_tokens: 50, cache_read_tokens: 0, cache_write_tokens: 0 }
    mocks.auth.mockReturnValue({ data: { user: { id: 7 } }, isLoading: false })
    mocks.usage.mockReturnValue({ isError: false, data: { totals: { ...first, input_tokens: 1200, output_tokens: 250, calls: 3 }, profiles: [first, second], daily: [{ ...first, date }], days: 30 }, refetch: mocks.retry })
    mocks.models.mockReturnValue({ isError: false, data: [{ id: "a", displayName: "Model A", model: "a", isDefault: true }, { id: "b", displayName: "Model B", model: "b", isDefault: false }] })
  })

  it("shows total usage, filters by profile and requests the selected time range", () => {
    render(<OverviewTokenUsage />)
    expect(screen.getByText("title")).toBeVisible()
    expect(screen.getByTitle("1,450")).toBeVisible()
    fireEvent.click(screen.getByRole("button", { name: "Model A · default" }))
    expect(screen.getByTitle("1,200")).toBeVisible()
    expect(screen.getByText("40%")).toBeVisible()
    fireEvent.click(screen.getByRole("button", { name: 'days:{"count":7}' }))
    expect(mocks.usage).toHaveBeenLastCalledWith(7)
  })

  it("shows a retry action when usage cannot be loaded", () => {
    mocks.usage.mockReturnValue({ isError: true, data: undefined, refetch: mocks.retry })
    render(<OverviewTokenUsage />)
    expect(screen.getByRole("alert")).toHaveTextContent("loadError")
    fireEvent.click(screen.getByRole("button", { name: "retry" }))
    expect(mocks.retry).toHaveBeenCalledOnce()
  })
})
