import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import type { FofaRecord, FofaSearchPage } from "@/types/fofa.types"
import { FofaSearchWorkspace } from "../fofa-search-workspace"

const state = vi.hoisted(() => ({ userId: 9, search: vi.fn(), reset: vi.fn() }))
vi.mock("@/hooks/use-fofa", () => ({ useFofa: () => ({
  userId: state.userId,
  settings: { data: { name: "fofaSettings/current", configured: true, canManage: false, canSearch: true, email: "", updatedAt: null }, isPending: false, error: null },
  search: { mutateAsync: state.search, reset: state.reset, isPending: false },
}) }))
vi.mock("@/components/shared/loading/content-handoff", () => ({ ContentHandoff: ({ children }: { children: React.ReactNode }) => children }))
vi.mock("../fofa-settings-dialog", () => ({ FofaSettingsDialog: () => null }))
vi.mock("../fofa-import-dialog", () => ({ FofaImportDialog: ({ open, rows }: { open: boolean; rows: number[] }) => open ? <div role="dialog">selected:{rows.join(",")}</div> : null }))

const row = (host: string, rowId = 0, valid = true): FofaRecord => ({ rowId, host, ip: "192.0.2.1", port: 443, protocol: "https", domain: "example.com", title: "fixture", server: "nginx", country: "", countryName: "", region: "", city: "", icp: "", baseProtocol: "tcp", url: host, hostname: "api.example.com", target: "api.example.com", importable: valid, importError: valid ? "" : "invalid fixture" })
const page = (number: number): FofaSearchPage => ({ id: `snapshot-${number}`, name: `fofaSearches/snapshot-${number}`, query: "fixture", page: number, pageSize: 50, full: false, total: 51, results: number === 1 ? [row("https://first.example.com"), row("https://invalid.example.com", 1, false)] : [row("https://second.example.com")], hasNext: number === 1, createdAt: "2026-10-30T00:00:00Z", expiresAt: "2026-10-30T01:00:00Z" })

describe("FOFA explicit search and page selection", () => {
  beforeEach(() => {
    state.userId = 9
    state.search.mockReset()
    state.reset.mockReset()
    state.search.mockImplementation(async (input: { page: number }) => page(input.page))
  })

  it("queries only on explicit actions and reuses cached previous pages without carrying selection", async () => {
    render(<FofaSearchWorkspace />)
    expect(state.search).not.toHaveBeenCalled()
    fireEvent.change(screen.getByRole("textbox", { name: "query" }), { target: { value: 'domain="example.com"' } })
    fireEvent.focus(window)
    expect(state.search).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole("button", { name: "search" }))
    await screen.findByText("https://first.example.com")
    expect(state.search).toHaveBeenCalledTimes(1)
    const invalid = screen.getByRole("checkbox", { name: 'selectRow:{"number":2}' })
    expect(invalid).toHaveAttribute("aria-disabled", "true")
    fireEvent.click(invalid)
    expect(screen.getByRole("button", { name: 'syncSelected:{"count":0}' })).toBeDisabled()
    fireEvent.click(screen.getByRole("checkbox", { name: 'selectRow:{"number":1}' }))
    expect(screen.getByRole("button", { name: 'syncSelected:{"count":1}' })).toBeEnabled()
    fireEvent.click(screen.getByRole("button", { name: "next" }))
    await screen.findByText("https://second.example.com")
    expect(screen.getByRole("button", { name: 'syncSelected:{"count":0}' })).toBeDisabled()
    fireEvent.click(screen.getByRole("button", { name: "previous" }))
    await screen.findByText("https://first.example.com")
    expect(state.search).toHaveBeenCalledTimes(2)
    expect(screen.getByRole("checkbox", { name: 'selectRow:{"number":1}' })).not.toBeChecked()
  })

  it("does not submit stale page rows after a new query fails", async () => {
    render(<FofaSearchWorkspace />)
    const input = screen.getByRole("textbox", { name: "query" })
    fireEvent.change(input, { target: { value: "first-query" } })
    fireEvent.click(screen.getByRole("button", { name: "search" }))
    await screen.findByText("https://first.example.com")
    fireEvent.click(screen.getByRole("checkbox", { name: 'selectRow:{"number":1}' }))
    state.search.mockRejectedValueOnce(new Error("fixture error"))
    fireEvent.change(input, { target: { value: "second-query" } })
    fireEvent.click(screen.getByRole("button", { name: "search" }))
    await screen.findByRole("alert")
    expect(screen.queryByText("https://first.example.com")).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /^syncSelected:/ })).not.toBeInTheDocument()
  })

  it("discards an in-flight result when the authenticated account changes", async () => {
    let finish: (value: FofaSearchPage) => void = () => undefined
    state.search.mockImplementationOnce(() => new Promise<FofaSearchPage>(resolve => { finish = resolve }))
    const view = render(<FofaSearchWorkspace />)
    fireEvent.change(screen.getByRole("textbox", { name: "query" }), { target: { value: "fixture" } })
    fireEvent.click(screen.getByRole("button", { name: "search" }))
    await waitFor(() => expect(state.search).toHaveBeenCalledTimes(1))
    state.userId = 10
    view.rerender(<FofaSearchWorkspace />)
    await act(async () => { finish(page(1)) })
    expect(screen.queryByText("https://first.example.com")).not.toBeInTheDocument()
    expect(screen.getByRole("textbox", { name: "query" })).toHaveValue("")
  })
})
