import React from "react"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { RepeaterWorkspace } from "../repeater-workspace"
import type { RepeaterInput } from "@/types/http-repeater.types"

const transport = vi.hoisted(() => ({ send: vi.fn(), user: 7 }))
vi.mock("@/services/http-repeater.service", () => ({ sendHttpReplay: transport.send }))
vi.mock("@/hooks/use-auth", () => ({ useAuth: () => ({ data: { user: { id: transport.user } } }) }))
vi.mock("@/components/shared/editors/code-editor", () => ({ CodeEditor: ({ value, onChange, readOnly }: { value: string; onChange: (text: string) => void; readOnly: boolean }) => <textarea aria-label="rawEditor" value={value} onChange={e => onChange(e.target.value)} readOnly={readOnly} /> }))

describe("Repeater workspace", () => {
  beforeEach(() => {
    transport.user = 7
    transport.send.mockReset()
    transport.send.mockImplementation(async (input: RepeaterInput) => {
      const wire = "HTTP/1.1 401 Unauthorized\r\nX-Proof: one\r\nX-Proof: two\r\n\r\nactual response"
      return { requestId: input.requestId, connectionTarget: "https://example.test:8443/edited", requestRaw: input.rawRequest.replaceAll("\n", "\r\n"), responseRaw: wire, responseBase64: btoa(wire), statusCode: 401, elapsedMs: 12, byteCount: wire.length, sent: true, truncated: false, binary: false }
    })
  })
  it("sends pasted Raw directly without a connection URL field", async () => {
    render(<RepeaterWorkspace />)
    expect(transport.send).not.toHaveBeenCalled()
    expect(screen.queryByLabelText("target")).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "generate" })).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText("rawEditor"), { target: { value: "POST /edited HTTP/1.1\nHost: example.test:8443\n\nbody" } })
    fireEvent.change(screen.getByLabelText("protocol"), { target: { value: "https" } })
    fireEvent.click(screen.getByRole("button", { name: "send" }))
    await screen.findByText("HTTP/1.1 401 Unauthorized")
    expect(screen.getByText(/actual response/)).toBeVisible()
    expect(transport.send).toHaveBeenCalledWith(expect.objectContaining({ scheme: "https", rawRequest: expect.stringContaining("Host: example.test:8443"), verifyTls: true, autoContentLength: true }), expect.any(AbortSignal))
    expect(transport.send.mock.calls[0][0]).not.toHaveProperty("targetUrl")
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "download" })).toBeEnabled()
  })
  it("uses the scheme in an absolute request URL without asking for a target", async () => {
    render(<RepeaterWorkspace />)
    fireEvent.change(screen.getByLabelText("rawEditor"), { target: { value: "GET https://example.test/ HTTP/1.1\nHost: example.test\n\n" } })
    expect(screen.getByLabelText("protocol")).toHaveValue("https")
    expect(screen.getByLabelText("protocol")).toBeDisabled()
    fireEvent.click(screen.getByRole("button", { name: "send" }))
    await screen.findByText("HTTP/1.1 401 Unauthorized")
    expect(transport.send.mock.calls[0][0]).not.toHaveProperty("targetUrl")
  })
  it("clears drafts and history when the account changes", async () => {
    const { rerender } = render(<RepeaterWorkspace />)
    fireEvent.change(screen.getByLabelText("rawEditor"), { target: { value: "GET / HTTP/1.1\nHost: private.example.test\n\n" } })
    fireEvent.click(screen.getByRole("button", { name: "send" }))
    await screen.findByText("HTTP/1.1 401 Unauthorized")
    transport.user = 8; rerender(<RepeaterWorkspace />)
    await waitFor(() => expect(screen.getByLabelText("rawEditor")).toHaveValue(""))
    expect(screen.queryByText("HTTP/1.1 401 Unauthorized")).not.toBeInTheDocument()
    expect(screen.getByLabelText("history")).toBeDisabled()
  })
})
