import { act, renderHook } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { useHttpRepeater } from "../use-http-repeater"
import type { RepeaterInput, RepeaterResult } from "@/types/http-repeater.types"

const transport = vi.hoisted(() => ({ send: vi.fn() }))
vi.mock("@/services/http-repeater.service", () => ({ sendHttpReplay: transport.send }))
const raw = (path: string) => `GET /${path} HTTP/1.1\nHost: example.test\n\n`
function response(input: RepeaterInput, body = "body"): RepeaterResult {
  const wire = `HTTP/1.1 200 OK\r\nContent-Length: ${body.length}\r\n\r\n${body}`
  return { requestId: input.requestId, connectionTarget: "http://example.test/", requestRaw: input.rawRequest, responseRaw: wire, responseBase64: btoa(wire), statusCode: 200, elapsedMs: 10, byteCount: wire.length, sent: true, truncated: false, binary: false }
}

describe("HTTP Repeater orchestration", () => {
  beforeEach(() => { transport.send.mockReset() })
  it("sends only on command, suppresses double clicks and isolates concurrent tab responses", async () => {
    const pending: Array<{ input: RepeaterInput; resolve: (result: RepeaterResult) => void }> = []
    transport.send.mockImplementation((input: RepeaterInput) => new Promise(resolve => pending.push({ input, resolve })))
    const { result } = renderHook(() => useHttpRepeater())
    expect(transport.send).not.toHaveBeenCalled()
    act(() => result.current.patch("1", { rawRequest: raw("first") }))
    let first!: Promise<void>, second!: Promise<void>
    act(() => { first = result.current.send(); void result.current.send() })
    expect(transport.send).toHaveBeenCalledTimes(1)
    act(() => result.current.add())
    act(() => result.current.patch("2", { scheme: "https", rawRequest: raw("second") }))
    act(() => { second = result.current.send() })
    await act(async () => { pending[1].resolve(response(pending[1].input, "second")); await second })
    await act(async () => { pending[0].resolve(response(pending[0].input, "first")); await first })
    expect(result.current.active.id).toBe("2")
    expect(result.current.selected?.result?.responseRaw).toContain("second")
    expect(result.current.tabs[0].history[0].result?.responseRaw).toContain("first")
    expect(pending[0].input.requestId).not.toBe(pending[1].input.requestId)
    act(() => result.current.setActiveId("1"))
    act(() => result.current.restore(result.current.active.history[0].id))
    expect(result.current.active.draft.rawRequest).toBe(raw("first"))
    expect(transport.send).toHaveBeenCalledTimes(2)
  })

  it("cancels the actual transport and does not replay canceled requests", async () => {
    let signal!: AbortSignal
    transport.send.mockImplementation((_input: RepeaterInput, current: AbortSignal) => { signal = current; return new Promise((_resolve, reject) => current.addEventListener("abort", () => reject(new Error("aborted")), { once: true })) })
    const { result } = renderHook(() => useHttpRepeater())
    act(() => result.current.patch("1", { rawRequest: raw("cancel") }))
    let sending!: Promise<void>
    act(() => { sending = result.current.send() })
    await act(async () => { result.current.cancel(); await sending })
    expect(signal.aborted).toBe(true)
    expect(result.current.active.pending).toBe(false)
    expect(result.current.active.error).toBe("canceled")
    expect(transport.send).toHaveBeenCalledTimes(1)
  })

  it("aborts on unmount and rejects empty requests without network activity", async () => {
    let signal!: AbortSignal
    transport.send.mockImplementation((_input: RepeaterInput, current: AbortSignal) => { signal = current; return new Promise((_resolve, reject) => current.addEventListener("abort", () => reject(new Error("aborted")), { once: true })) })
    const { result, unmount } = renderHook(() => useHttpRepeater())
    act(() => result.current.patch("1", { rawRequest: "" }))
    await act(() => result.current.send())
    expect(result.current.active.error).toBe("invalid_request")
    expect(transport.send).not.toHaveBeenCalled()
    act(() => result.current.patch("1", { rawRequest: raw("test") }))
    let sending!: Promise<void>
    act(() => { sending = result.current.send() })
    unmount(); await sending
    expect(signal.aborted).toBe(true)
  })

  it("shows the specific parser failure instead of replacing it with a generic error", async () => {
    transport.send.mockRejectedValue({ code: "REPEATER_INVALID_RAW", reason: "raw_missing_body" })
    const { result } = renderHook(() => useHttpRepeater())
    act(() => result.current.patch("1", { rawRequest: "POST / HTTP/1.1\nHost: example.test\nContent-Length: 10" }))
    await act(() => result.current.send())
    expect(result.current.active.error).toBe("raw_missing_body")
    expect(result.current.selected?.error).toBe("raw_missing_body")
    expect(transport.send).toHaveBeenCalledTimes(1)
  })

  it("keeps HTTP/2 replay routing when the decoded view uses a different authority", async () => {
    const source = "GET https://connection.test/api HTTP/2.0\nHost: virtual.test\n\n"
    transport.send.mockImplementation(async (input: RepeaterInput) => ({ ...response(input), requestRaw: "GET /api HTTP/2.0\r\nHost: virtual.test\r\n\r\n", representation: "http2_decoded" }))
    const { result } = renderHook(() => useHttpRepeater())
    act(() => result.current.patch("1", { scheme: "http", rawRequest: source }))
    await act(() => result.current.send())
    expect(result.current.active.draft.rawRequest).toBe(source)
    await act(() => result.current.send())
    expect(transport.send.mock.calls[1][0].rawRequest).toBe(source)
    expect(result.current.active.history[0].draft.rawRequest).toBe(source)
  })
})
