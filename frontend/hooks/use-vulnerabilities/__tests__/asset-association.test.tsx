import * as React from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { useAllVulnerabilities } from "../queries"

vi.mock("@/services/vulnerability.service", () => ({
  VulnerabilityService: {
    getAllVulnerabilities: vi.fn(async () => ({ results: [{ id: 1, assetId: "123", url: "https://app.example.test/api", vulnType: "风险", severity: "info", source: "info-gath", isReviewed: false, createdAt: "2026-01-01T00:00:00Z" }], totalSize: 1 })),
  },
}))

describe("vulnerability asset association", () => {
  it("retains the asset id when transforming API results", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
    const { result } = renderHook(() => useAllVulnerabilities(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.vulnerabilities[0]?.assetId).toBe("123")
  })
})
