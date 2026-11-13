import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { AssetPagination } from "../asset-pagination"

describe("asset pagination", () => {
  it("supports the last page and a direct page jump", () => {
    const onPageChange = vi.fn()
    render(<AssetPagination page={1} total={123} pageSize={50} onPageChange={onPageChange} />)
    expect(screen.getByRole("button", { name: "previous" })).toBeDisabled()
    fireEvent.click(screen.getByRole("button", { name: "next" }))
    expect(onPageChange).toHaveBeenLastCalledWith(2)
    fireEvent.change(screen.getByRole("spinbutton", { name: "jumpPage" }), { target: { value: "3" } })
    fireEvent.submit(screen.getByRole("spinbutton", { name: "jumpPage" }).closest("form")!)
    expect(onPageChange).toHaveBeenLastCalledWith(3)
  })
})
