import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { GlobalFindingReview } from "../global-finding-review"

const requests = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock("@/lib/api-client", () => ({ api: requests }))
vi.mock("@/hooks/use-auth", () => ({ useAuth: () => ({ data: { user: { id: 7 } } }) }))
vi.mock("next/link", () => ({ default: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a> }))
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

describe("cross-task finding review", () => {
  it("submits selected findings as one atomic batch grouped by source task", async () => {
    requests.get.mockResolvedValue({ data: { items: [
      { id: "1", task_id: "10", task_name: "Task A", target_name: "a.example", name: "Alpha", vulnclass: "x", severity: "high", status: "pending", summary: "A", in_group: false },
      { id: "2", task_id: "20", task_name: "Task B", target_name: "b.example", name: "Beta", vulnclass: "x", severity: "medium", status: "pending", summary: "B", in_group: false },
    ], total: 2, page: 0, size: 25 } })
    requests.post.mockResolvedValue({ data: { reviews: [
      { id: "100", task_id: "10", conversation_id: "11111111-1111-4111-8111-111111111111", state: "queued", finding_count: 1 },
      { id: "200", task_id: "20", conversation_id: "22222222-2222-4222-8222-222222222222", state: "queued", finding_count: 1 },
    ] } })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(<QueryClientProvider client={client}><GlobalFindingReview /></QueryClientProvider>)
    fireEvent.click(await screen.findByRole("checkbox", { name: 'select:{"name":"Alpha","task":"Task A"}' }))
    fireEvent.click(screen.getByRole("checkbox", { name: 'select:{"name":"Beta","task":"Task B"}' }))
    fireEvent.click(screen.getByRole("button", { name: "start" }))
    await waitFor(() => expect(requests.post).toHaveBeenCalledWith("/aiFindingCaseReviews", { batches: [
      { task_id: "10", finding_ids: ["1"] }, { task_id: "20", finding_ids: ["2"] },
    ] }))
    await screen.findByText("queuedTitle")
    expect(screen.getAllByRole("link", { name: "conversation" })).toHaveLength(2)
    expect(screen.getAllByRole("link", { name: "viewFinding" })).toHaveLength(2)
  })

  it("expands and removes grouped findings as a unit across tasks", async () => {
    const alpha = { id: "1", task_id: "10", task_name: "Task A", target_name: "a.example", name: "Alpha", vulnclass: "x", severity: "high", status: "pending", summary: "A", in_group: true, case_id: "8" }
    const beta = { ...alpha, id: "2", name: "Beta" }
    const gamma = { ...alpha, id: "3", name: "Gamma", task_id: "20", task_name: "Task B", in_group: false, case_id: undefined }
    requests.get.mockImplementation(async (url: string) => url.endsWith("/findingCases/8")
      ? { data: { id: "8", task_id: "10", inherited: false, member_count: 2, members: [alpha, beta] } }
      : { data: { items: [alpha, gamma], total: 2, page: 0, size: 25 } })
    requests.post.mockResolvedValue({ data: { reviews: [] } })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(<QueryClientProvider client={client}><GlobalFindingReview /></QueryClientProvider>)
    const alphaCheckbox = await screen.findByRole("checkbox", { name: 'select:{"name":"Alpha","task":"Task A"}' })
    fireEvent.click(alphaCheckbox)
    await screen.findByRole("button", { name: 'removeSelected:{"name":"Beta","task":"Task A"}' })
    fireEvent.click(screen.getByRole("button", { name: 'removeSelected:{"name":"Beta","task":"Task A"}' }))
    expect(alphaCheckbox).not.toBeChecked()
    fireEvent.click(alphaCheckbox)
    await screen.findByRole("button", { name: 'removeSelected:{"name":"Beta","task":"Task A"}' })
    fireEvent.click(screen.getByRole("checkbox", { name: 'select:{"name":"Gamma","task":"Task B"}' }))
    fireEvent.click(screen.getByRole("button", { name: "start" }))
    await waitFor(() => expect(requests.post).toHaveBeenCalledWith("/aiFindingCaseReviews", { batches: [
      { task_id: "10", finding_ids: ["1", "2"] }, { task_id: "20", finding_ids: ["3"] },
    ] }))
  })

  it("does not select a group whose membership changed", async () => {
    const alpha = { id: "1", task_id: "10", task_name: "Task A", name: "Alpha", in_group: true, case_id: "8" }
    requests.get.mockImplementation(async (url: string) => url.endsWith("/findingCases/8")
      ? { data: { id: "8", task_id: "10", inherited: false, member_count: 2, members: [{ id: "2" }, { id: "3" }] } }
      : { data: { items: [alpha], total: 1, page: 0, size: 25 } })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(<QueryClientProvider client={client}><GlobalFindingReview /></QueryClientProvider>)
    const checkbox = await screen.findByRole("checkbox", { name: 'select:{"name":"Alpha","task":"Task A"}' })
    fireEvent.click(checkbox)
    await waitFor(() => expect(checkbox).not.toBeDisabled())
    expect(checkbox).not.toBeChecked()
    expect(screen.getByRole("button", { name: "start" })).toBeDisabled()
  })
})
