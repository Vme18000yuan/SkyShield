import { fireEvent, render, screen, within, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { AIDataDeleteButton } from "../ai-data-delete-button"

const fixtures = vi.hoisted(() => ({ mutate: vi.fn(), pending: false }))
vi.mock("@/hooks/use-ai-data-deletion", () => ({ useAIDataDeletion: () => ({ mutate: fixtures.mutate, isPending: fixtures.pending }) }))
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }))

describe("AI data delete confirmation", () => {
  beforeEach(() => { fixtures.mutate.mockReset(); fixtures.pending = false })
  it("requires confirmation and explains retained assets", async () => {
    const done = vi.fn()
    fixtures.mutate.mockImplementation((_request, options) => options.onSuccess())
    render(<AIDataDeleteButton request={{ kind: "task", id: "7" }} onDeleted={done} />)
    fireEvent.click(screen.getByRole("button", { name: "delete" }))
    const dialog = await screen.findByRole("alertdialog")
    expect(within(dialog).getByText("taskDescription")).toBeVisible()
    expect(within(dialog).getByText("preserved")).toBeVisible()
    expect(fixtures.mutate).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole("button", { name: "confirm" }))
    expect(fixtures.mutate).toHaveBeenCalledWith({ kind: "task", id: "7" }, expect.any(Object))
    await waitFor(() => expect(done).toHaveBeenCalledOnce())
  })
  it("keeps the dialog available after a rejected mutation", async () => {
    render(<AIDataDeleteButton request={{ kind: "executions", source: "task", ids: ["8"] }} />)
    fireEvent.click(screen.getByRole("button", { name: "delete" }))
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "confirm" }))
    expect(screen.getByRole("alertdialog")).toBeVisible()
  })
  it("does not change the confirmed selection when the list refreshes", async () => {
    const view = render(<AIDataDeleteButton request={{ kind: "executions", source: "task", ids: ["8"] }} />)
    fireEvent.click(screen.getByRole("button", { name: "delete" }))
    await screen.findByRole("alertdialog")
    view.rerender(<AIDataDeleteButton request={{ kind: "executions", source: "task", ids: ["9"] }} />)
    fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "confirm" }))
    expect(fixtures.mutate).toHaveBeenCalledWith({ kind: "executions", source: "task", ids: ["8"] }, expect.any(Object))
  })
})
