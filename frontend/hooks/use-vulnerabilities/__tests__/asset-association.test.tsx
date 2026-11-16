import * as React from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { useAllVulnerabilities, useTargetVulnerabilities, useScanVulnerabilities } from "../queries"

const fixture = vi.hoisted(() => ({ results: [{ id: 1, assetId: "123", displayName: "接口未授权读取内部数据", url: "https://app.example.test/api", vulnType: "unauthorized_access", severity: "info", source: "ai", isReviewed: false, createdAt: "2026-01-01T00:00:00Z" }], totalSize: 1 }))

vi.mock("@/services/vulnerability.service", () => ({
  VulnerabilityService: {
    getAllVulnerabilities: vi.fn(async () => fixture),
    getVulnerabilitiesByTargetId: vi.fn(async () => fixture),
    getVulnerabilitiesByScanId: vi.fn(async () => fixture),
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

  it.each([useAllVulnerabilities, () => useTargetVulnerabilities(7), () => useScanVulnerabilities(9)])("preserves names independently from classification in every list scope", async (useList) => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
    const { result } = renderHook(() => useList(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.vulnerabilities[0]?.displayName).toBe(fixture.results[0].displayName)
    expect(result.current.data?.vulnerabilities[0]?.vulnType).toBe(fixture.results[0].vulnType)
  })
})
