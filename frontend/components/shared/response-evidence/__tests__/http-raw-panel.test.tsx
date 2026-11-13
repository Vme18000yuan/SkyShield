import React from "react"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { HttpRawExchange, HttpRawMessage, httpRawLabels } from "../http-raw-panel"
import { inspectHttpRaw, joinHttpHeadBody, trafficRawPreview } from "../http-raw"

const labels = httpRawLabels(key => key)
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }))

describe("Raw HTTP evidence", () => {
  beforeEach(() => {
    Object.defineProperty(window, "isSecureContext", { configurable: true, value: true })
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: vi.fn().mockResolvedValue(undefined) } })
  })

  it("shows request and response together and copies CRLF, duplicate headers and body unchanged", async () => {
    const request = 'POST /api?q=%2F HTTP/1.1\r\nHost: example.test\r\nX-Proof: first\r\nX-Proof: second\r\n\r\n\n{"value":"<script>"}'
    const response = 'HTTP/2.0 200 OK\r\nSet-Cookie: a=1\r\nSet-Cookie: b=2\r\n\r\n{"ok":true}'
    render(<HttpRawExchange request={request} response={response} labels={labels} />)
    const req = screen.getByRole("region", { name: "request" })
    const res = screen.getByRole("region", { name: "response" })
    expect(req.querySelector("[data-http-raw]")?.textContent).toBe(request)
    expect(res.querySelector("[data-http-raw]")?.textContent).toBe(response)
    expect(req.querySelector("[data-http-raw]")).toHaveClass("whitespace-pre")
    fireEvent.click(within(req).getByRole("button", { name: "copy" }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith(request))
    fireEvent.click(within(req).getByRole("button", { name: "wrap" }))
    expect(within(req).getByRole("button", { name: "wrap" })).toHaveAttribute("aria-pressed", "true")
    expect(req.querySelector("[data-http-raw]")).toHaveClass("whitespace-pre-wrap")
    expect(req.querySelector("[data-http-raw]")?.textContent).toBe(request)
    expect(req.querySelector("script")).toBeNull()
  })

  it("labels absent and body-only evidence without inventing an HTTP status", () => {
    render(<HttpRawExchange response={'{"code":3}'} responseBodyOnly labels={labels} />)
    expect(screen.getByText("notCaptured")).toBeVisible()
    expect(screen.getByText("bodyOnlyHint")).toBeVisible()
    expect(screen.queryByText(/HTTP\/1\.1/)).not.toBeInTheDocument()
    expect(screen.getByText('{"code":3}')).toBeVisible()
  })

  it("makes partial-copy scope explicit", async () => {
    render(<HttpRawMessage value="body fragment" side="response" bodyOnly partial labels={labels} />)
    expect(screen.getByText("partial")).toBeVisible()
    fireEvent.click(screen.getByRole("button", { name: "copyPartial" }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledWith("body fragment"))
  })

  it("assembles separate fields with exactly one boundary and leaves body bytes alone", () => {
    const body = '\r\n{"first":"line"}'
    for (const head of ["HTTP/1.1 200 OK\r\nX-A: 1", "HTTP/1.1 200 OK\r\nX-A: 1\r\n", "HTTP/1.1 200 OK\r\nX-A: 1\r\n\r\n"]) {
      expect(joinHttpHeadBody(head, body)).toBe("HTTP/1.1 200 OK\r\nX-A: 1\r\n\r\n" + body)
    }
    expect(joinHttpHeadBody("", body)).toBe(body)
    expect(joinHttpHeadBody("HTTP 200\nX-A: 1", body)).toBe("HTTP 200\nX-A: 1\n\n" + body)
    expect(inspectHttpRaw("HTTP 200\n\nbody", "response").incomplete).toBe(true)
    expect(inspectHttpRaw('GET / HTTP/1.1\r\n\r\n{"a":1}', "request").bodyOnly).toBe(false)
  })

  it("keeps recorder annotations outside copied traffic fragments", () => {
    const text = "HTTP/1.1 200 OK\r\n\r\npartial"
    const preview = trafficRawPreview(text + "\n…[truncated] @blob sha256:" + "a".repeat(64) + " (len=9000)")
    expect(preview).toEqual({ text, partial: true, blob: "a".repeat(64) })
  })
})
