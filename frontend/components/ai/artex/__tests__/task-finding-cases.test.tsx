import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { TaskFindingCases } from "../task-finding-cases"
import { TaskFindings } from "../task-findings"

const requests = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
vi.mock("@/lib/api-client", () => ({ api: requests }))
vi.mock("@/hooks/use-auth", () => ({ useAuth: () => ({ data: { user: { id: 7 } } }) }))
vi.mock("@/components/settings/ai/artex-markdown", () => ({ Markdown: ({ text }: { text: string }) => <div>{text}</div> }))
vi.mock("@/components/ai/finding-retest-panel", () => ({ FindingRetestPanel: () => null }))
vi.mock("@/components/ai/artex/task-traffic", () => ({ FindingTrafficPanel: () => null }))
vi.mock("next/link", () => ({ default: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a> }))
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const findings = [
  { id: "1", task_id: "30", name: "Alpha", vulnclass: "fixture", severity: "high", status: "pending", summary: "first evidence", evidence: "Alpha proof", assets: [], inherited: false },
  { id: "2", task_id: "30", name: "Beta", vulnclass: "fixture", severity: "medium", status: "pending", summary: "second evidence", evidence: "Beta proof", assets: [], inherited: false },
]
const own = { id: "8", task_id: "30", task_name: "Current", title: "Own group", reason: "same issue", severity: "", version: 2, report_version: 0, has_report: false, member_count: 2, inherited: false, created_at: "2026-01-01T00:00:00Z", members: findings, events: [{ id: "10", action: "merge", actor: "human", reason: "historical reason", finding_ids: ["1", "2"], created_at: "2026-01-01T00:00:00Z" }] }
const inherited = { ...own, id: "9", task_id: "10", task_name: "Source task", title: "Inherited group", inherited: true, events: [] }
const suggestion = { id: "20", task_id: "30", task_name: "Current", title: "Possible group", reason: "same endpoint", inherited: false, created_at: "2026-01-01T00:00:00Z", members: [...findings, { ...findings[1], id: "3", name: "Gamma" }] }
const completedReview = { id: "50", task_id: "30", conversation_id: "22222222-2222-4222-8222-222222222222", state: "completed", error: "", finding_count: 1, created_at: "2026-01-01T00:00:00Z", findings: [findings[0]], conclusion: { summary: "Reviewed", decisions: [{ finding_id: "1", verdict: "independent", reason: "different path", root_cause_evidence: "proof" }] } }

describe("task finding groups", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    let pending = [suggestion]
    let reviews = [completedReview]
    requests.get.mockImplementation(async (url: string) => {
      if (url.endsWith("/findingCaseSuggestions")) return { data: { items: pending, total: pending.length, page: 0, size: 25 } }
      if (url.endsWith("/findingCaseReviews")) return { data: { items: reviews, total: reviews.length, page: 0, size: 25 } }
      if (url.endsWith("/findingCaseReviews/50")) return { data: completedReview }
      if (url.endsWith("/findingCases")) return { data: { items: [own, inherited], total: 2, page: 0, size: 25 } }
      if (url.endsWith("/findingCases/8")) return { data: own }
      if (url.endsWith("/findingCases/9")) return { data: inherited }
      if (url.endsWith("/findings")) return { data: { items: findings, total: 2, page: 0, size: 25 } }
      if (url.endsWith("/findings/1/duplicates")) return { data: { items: [{ ...findings[1], score: 7, tier: "strong", hit_reasons: ["same_type", "asset_overlap"] }], total: 1, strong_total: 1, page: 0, size: 20 } }
      if (url.endsWith("/findings/1")) return { data: findings[0] }
      throw new Error(`unexpected GET ${url}`)
    })
    requests.post.mockImplementation(async (url: string) => { if (url.endsWith("/findingCaseSuggestions/20/resolve")) pending = []; if (url.endsWith("/findingCaseReviews")) reviews = [{ ...completedReview, id: "51", state: "queued" }, ...reviews]; if (url.endsWith("/findingCaseReviews/51/cancel")) reviews = reviews.map(item => item.id === "51" ? { ...item, state: "stopped" } : item); return { data: { updated: true } } })
    requests.put.mockResolvedValue({ data: { updated: true } })
  })

  it("groups own findings, views evidence, saves a report and detaches a member", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(<QueryClientProvider client={client}><TaskFindingCases taskId="30" /></QueryClientProvider>)
    await screen.findByText("Own group")
    fireEvent.click(screen.getByRole("button", { name: "merge" }))
    let dialog = await screen.findByRole("dialog")
    fireEvent.change(within(dialog).getByLabelText("groupName"), { target: { value: "Merged" } })
    fireEvent.change(within(dialog).getByLabelText("mergeReason"), { target: { value: "same root cause" } })
    fireEvent.click(await within(dialog).findByRole("checkbox", { name: /Alpha/ }))
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /Beta/ }))
    fireEvent.click(within(dialog).getByRole("button", { name: "confirmMerge" }))
    await waitFor(() => expect(requests.post).toHaveBeenCalledWith("/aiTasks/30/findingCases", { finding_ids: ["1", "2"], title: "Merged", reason: "same root cause" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())

    const ownRow = screen.getByText("Own group").closest("article")!
    fireEvent.click(within(ownRow).getByRole("button", { name: "details" }))
    dialog = await screen.findByRole("dialog")
    await within(dialog).findByText("Alpha")
    const alphaRow = within(dialog).getByText("Alpha").parentElement!.parentElement!
    fireEvent.click(within(alphaRow).getByRole("button", { name: "evidence" }))
    await within(dialog).findByText("Alpha proof")
    fireEvent.click(within(dialog).getByText("history"))
    expect(within(dialog).getByText("historical reason")).toBeVisible()
    fireEvent.click(within(dialog).getByText("editReport"))
    fireEvent.change(within(dialog).getByLabelText("groupName"), { target: { value: "Unified" } })
    fireEvent.change(within(dialog).getByLabelText("severityReason"), { target: { value: "confirmed impact" } })
    fireEvent.change(within(dialog).getByLabelText("report"), { target: { value: "# report" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "saveReport" }))
    await waitFor(() => expect(requests.put).toHaveBeenCalledWith("/aiTasks/30/findingCases/8/report", { version: 2, title: "Unified", severity: "high", severity_reason: "confirmed impact", report: "# report" }))

    fireEvent.click(within(alphaRow).getByRole("button", { name: "detach" }))
    fireEvent.change(within(dialog).getByRole("textbox", { name: "detachReason" }), { target: { value: "different cause" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "confirmDetach" }))
    await waitFor(() => expect(requests.post).toHaveBeenCalledWith("/aiTasks/30/findingCases/8/members/1/detach", { reason: "different cause" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())

    const inheritedRow = screen.getByText("Inherited group").closest("article")!
    fireEvent.click(within(inheritedRow).getByRole("button", { name: "details" }))
    dialog = await screen.findByRole("dialog")
    await within(dialog).findByText("Source task")
    expect(within(dialog).queryByRole("button", { name: "detach" })).not.toBeInTheDocument()
    expect(within(dialog).queryByText("editReport")).not.toBeInTheDocument()
    expect(within(dialog).queryByText("history")).not.toBeInTheDocument()
    expect(within(dialog).getByRole("link", { name: "Source task" })).toHaveAttribute("href", "/ai/tasks/10/")
  })

  it("applies the task finding filters to both groups and raw findings", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(<QueryClientProvider client={client}><TaskFindings taskId="30" /></QueryClientProvider>)
    await screen.findByText("Own group")
    fireEvent.change(screen.getByLabelText("severity"), { target: { value: "high" } })
    fireEvent.change(screen.getByLabelText("search"), { target: { value: "Alpha" } })
    await waitFor(() => {
      expect(requests.get).toHaveBeenCalledWith("/aiTasks/30/findingCases", { params: { page: 0, size: 25, severity: "high", q: "Alpha" } })
      expect(requests.get).toHaveBeenCalledWith("/aiTasks/30/findings", { params: { page: 0, size: 25, severity: "high", q: "Alpha" } })
    })
  })

  it("lets the operator review similar findings before preparing a group", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(<QueryClientProvider client={client}><TaskFindings taskId="30" /></QueryClientProvider>)
    fireEvent.click(await screen.findByRole("button", { name: 'similarFor:{"name":"Alpha"}' }))
    let dialog = await screen.findByRole("dialog")
    fireEvent.click(await within(dialog).findByRole("checkbox", { name: /Beta/ }))
    fireEvent.click(within(dialog).getByRole("button", { name: 'prepareMerge:{"count":2}' }))
    await screen.findByText("mergeDescription")
    dialog = screen.getByRole("dialog")
    expect(within(dialog).getByLabelText("groupName")).toHaveValue("Alpha")
    expect(within(dialog).getByText('selectedCount:{"count":2}')).toBeVisible()
    fireEvent.change(within(dialog).getByLabelText("mergeReason"), { target: { value: "same root cause" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "confirmMerge" }))
    await waitFor(() => expect(requests.post).toHaveBeenCalledWith("/aiTasks/30/findingCases", { finding_ids: ["1", "2"], title: "Alpha", reason: "same root cause" }))
  })

  it("accepts a suggestion while excluding a member after inspecting evidence", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(<QueryClientProvider client={client}><TaskFindingCases taskId="30" /></QueryClientProvider>)
    await screen.findByText("Possible group")
    fireEvent.click(screen.getByRole("button", { name: "review" }))
    const dialog = await screen.findByRole("dialog")
    fireEvent.click(within(dialog).getByRole("checkbox", { name: 'includeMember:{"name":"Gamma"}' }))
    const alpha = within(dialog).getByText("Alpha").parentElement!
    fireEvent.click(within(alpha).getByRole("button", { name: "evidence" }))
    await within(dialog).findByText("Alpha proof")
    fireEvent.click(within(dialog).getByRole("button", { name: "confirmAccept" }))
    await waitFor(() => expect(requests.post).toHaveBeenCalledWith("/aiTasks/30/findingCaseSuggestions/20/resolve", { accept: true, exclude: ["3"] }))
    await waitFor(() => expect(screen.queryByText("Possible group")).not.toBeInTheDocument())
  })

  it("requires a separate confirmation before rejecting a suggestion", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(<QueryClientProvider client={client}><TaskFindingCases taskId="30" /></QueryClientProvider>)
    await screen.findByText("Possible group")
    fireEvent.click(screen.getByRole("button", { name: "reject" }))
    const dialog = await screen.findByRole("dialog")
    fireEvent.click(within(dialog).getByRole("button", { name: "confirmReject" }))
    await waitFor(() => expect(requests.post).toHaveBeenCalledWith("/aiTasks/30/findingCaseSuggestions/20/resolve", { accept: false, exclude: [] }))
  })

  it("starts and cancels a queued historical review and shows completed decisions", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(<QueryClientProvider client={client}><TaskFindingCases taskId="30" /></QueryClientProvider>)
    await screen.findByText("state.completed")
    const completedRow = screen.getByText("state.completed").closest("article")!
    fireEvent.click(within(completedRow).getByRole("button", { name: "details" }))
    let dialog = await screen.findByRole("dialog")
    await within(dialog).findByText("Reviewed")
    expect(within(dialog).getByText(/Alpha.*verdict.independent/)).toBeVisible()
    fireEvent.click(within(dialog).getByRole("button", { name: "close" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    fireEvent.click(screen.getByRole("button", { name: "start" }))
    dialog = await screen.findByRole("dialog")
    fireEvent.click(await within(dialog).findByRole("checkbox", { name: /Alpha/ }))
    fireEvent.click(within(dialog).getByRole("button", { name: "confirmStart" }))
    await waitFor(() => expect(requests.post).toHaveBeenCalledWith("/aiTasks/30/findingCaseReviews", { finding_ids: ["1"] }))
    const queued = await screen.findByText("state.queued")
    fireEvent.click(within(queued.closest("article")!).getByRole("button", { name: "stop" }))
    await waitFor(() => expect(requests.post).toHaveBeenCalledWith("/aiTasks/30/findingCaseReviews/51/cancel"))
    await screen.findByText("state.stopped")
  })

  it("prefills an AI review from an owned group", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(<QueryClientProvider client={client}><TaskFindingCases taskId="30" /></QueryClientProvider>)
    const row = (await screen.findByText("Own group")).closest("article")!
    fireEvent.click(within(row).getByRole("button", { name: "details" }))
    let dialog = await screen.findByRole("dialog")
    fireEvent.click(await within(dialog).findByRole("button", { name: "reviewGroup" }))
    await screen.findByText("selectDescription")
    dialog = screen.getByRole("dialog")
    expect(within(dialog).getByText('selected:{"count":2}')).toBeVisible()
    fireEvent.click(within(dialog).getByRole("button", { name: "confirmStart" }))
    await waitFor(() => expect(requests.post).toHaveBeenCalledWith("/aiTasks/30/findingCaseReviews", { finding_ids: ["1", "2"] }))
  })

  it("selects or removes the whole group when choosing a grouped finding", async () => {
    const originalGet = requests.get.getMockImplementation()!
    requests.get.mockImplementation(async (url: string, ...args: unknown[]) => url.endsWith("/findings")
      ? { data: { items: [{ ...findings[0], case_id: "8" }], total: 1, page: 0, size: 25 } }
      : originalGet(url, ...args))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(<QueryClientProvider client={client}><TaskFindingCases taskId="30" /></QueryClientProvider>)
    fireEvent.click(await screen.findByRole("button", { name: "start" }))
    const dialog = await screen.findByRole("dialog")
    const alpha = await within(dialog).findByRole("checkbox", { name: /Alpha/ })
    fireEvent.click(alpha)
    await within(dialog).findByText('selected:{"count":2}')
    fireEvent.click(within(dialog).getByRole("button", { name: 'removeSelected:{"name":"Beta"}' }))
    expect(within(dialog).getByText('selected:{"count":0}')).toBeVisible()
    fireEvent.click(alpha)
    await within(dialog).findByText('selected:{"count":2}')
    fireEvent.click(within(dialog).getByRole("button", { name: "confirmStart" }))
    await waitFor(() => expect(requests.post).toHaveBeenCalledWith("/aiTasks/30/findingCaseReviews", { finding_ids: ["1", "2"] }))
  })

  it("removes the whole group from a prefilled review", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(<QueryClientProvider client={client}><TaskFindingCases taskId="30" /></QueryClientProvider>)
    const row = (await screen.findByText("Own group")).closest("article")!
    fireEvent.click(within(row).getByRole("button", { name: "details" }))
    const detail = await screen.findByRole("dialog")
    fireEvent.click(await within(detail).findByRole("button", { name: "reviewGroup" }))
    await screen.findByText("selectDescription")
    const dialog = screen.getByRole("dialog")
    fireEvent.click(within(dialog).getByRole("button", { name: 'removeSelected:{"name":"Alpha"}' }))
    expect(within(dialog).getByText('selected:{"count":0}')).toBeVisible()
    expect(within(dialog).getByRole("button", { name: "confirmStart" })).toBeDisabled()
  })

  it("opens an authorized finding from a direct task link", async () => {
    window.history.replaceState(null, "", "/ai/tasks/30/?tab=findings&finding=1")
    try {
      const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
      render(<QueryClientProvider client={client}><TaskFindings taskId="30" /></QueryClientProvider>)
      const dialog = await screen.findByRole("dialog")
      await within(dialog).findByText("Alpha proof")
    } finally {
      window.history.replaceState(null, "", "/")
    }
  })
})
