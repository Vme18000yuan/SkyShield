import { beforeEach, describe, expect, it, vi } from "vitest"
import { sendHttpReplay } from "../http-repeater.service"
import { deriveReplayTarget, normalizeReplayRequest, replayValidationReason } from "../http-repeater-target"
import type { RepeaterInput } from "@/types/http-repeater.types"

const transport = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock("@/lib/api-client", () => ({ api: transport }))

describe("Raw-only replay transport", () => {
  beforeEach(() => { transport.post.mockReset(); transport.post.mockResolvedValue({ data: { statusCode: 200, responseRaw: "HTTP/1.1 200 OK\r\n\r\n" } }) })
  it.each([
    ["example.test", "auto", "http://example.test/"],
    ["example.test:80", "auto", "http://example.test:80/"],
    ["example.test:443", "auto", "https://example.test:443/"],
    ["example.test:00443", "auto", "https://example.test:00443/"],
    ["example.test", "https", "https://example.test/"],
    ["example.test:8443", "https", "https://example.test:8443/"],
    ["example.test:80", "https", "https://example.test:80/"],
    ["192.0.2.1", "http", "http://192.0.2.1/"],
    ["192.0.2.1", "https", "https://192.0.2.1/"],
    ["192.0.2.1:8080", "https", "https://192.0.2.1:8080/"],
    ["[2001:db8::1]", "http", "http://[2001:db8::1]/"],
    ["[2001:db8::1]", "https", "https://[2001:db8::1]/"],
    ["[2001:db8::1]:443", "auto", "https://[2001:db8::1]:443/"],
    ["[::1]:8080", "auto", "http://[::1]:8080/"],
  ] as const)("derives %s with %s without a URL input", async (host, scheme, target) => {
    const rawRequest = `POST /inspect?q=1 HTTP/1.1\r\nhOsT: ${host}\r\nX-Proof: one\r\nX-Proof: two\r\n\r\nbody`
    const request = { requestId: "fixture", rawRequest, timeoutSeconds: 15, verifyTls: true, autoContentLength: true }
    const input: RepeaterInput = { ...request, scheme }
    const signal = new AbortController().signal
    const result = await sendHttpReplay(input, signal)
    expect(transport.post).toHaveBeenCalledExactlyOnceWith("/httpRepeater:send", { ...request, targetUrl: target }, { signal, timeout: 20000 })
    expect(transport.post.mock.calls[0][1].rawRequest).toBe(rawRequest)
    expect(transport.post.mock.calls[0][1]).not.toHaveProperty("scheme")
    expect(result.connectionTarget).toBe(target)
  })
  it("uses the absolute request URL and preserves the original request text", async () => {
    const rawRequest = "GET https://absolute.test:9443/api?a=%2f HTTP/1.1\r\nHost: virtual.test\r\n\r\n"
    const result = await sendHttpReplay({ requestId: "fixture", scheme: "http", rawRequest, verifyTls: true, timeoutSeconds: 15, autoContentLength: true }, new AbortController().signal)
    expect(transport.post.mock.calls[0][1]).toEqual(expect.objectContaining({ rawRequest, targetUrl: "https://absolute.test:9443/" }))
    expect(result.connectionTarget).toBe("https://absolute.test:9443/")
  })
  it.each([
    "http://absolute.test/",
    "http://absolute.test:80/",
    "https://absolute.test/",
    "https://absolute.test:443/",
    "https://absolute.test:00443/",
    "https://192.0.2.1:8443/",
    "https://[2001:db8::1]/",
    "https://[2001:db8::1]:443/",
  ])("preserves the authority of absolute target %s", async target => {
    const rawRequest = `GET ${target}path?q=1 HTTP/1.1\r\nHost: virtual.test:8080\r\n\r\n`
    const result = await sendHttpReplay({ requestId: "fixture", scheme: "auto", rawRequest, verifyTls: true, timeoutSeconds: 15, autoContentLength: true }, new AbortController().signal)
    expect(transport.post.mock.calls[0][1]).toEqual(expect.objectContaining({ rawRequest, targetUrl: target }))
    expect(result.connectionTarget).toBe(target)
  })
  it.each([
    "GET / HTTP/1.1\n\n",
    "GET / HTTP/1.1\nHost: first.test\nhost: second.test\n\n",
    "GET / HTTP/1.1\nHost: user:pass@example.test\n\n",
    "GET / HTTP/1.1\nHost: example.test/path\n\n",
    "GET / HTTP/1.1\nHost: example.test:0\n\n",
    "GET / HTTP/1.1\nHost: example.test:\n\n",
    "GET / HTTP/1.1\nHost: [domain.test]\n\n",
    "GET / HTTP/1.1\nHost: ::1\n\n",
    "GET ftp://example.test/a HTTP/1.1\nHost: example.test\n\n",
    "GET https://user:pass@example.test/ HTTP/1.1\nHost: example.test\n\n",
  ])("rejects ambiguous/missing destinations before sending", async rawRequest => {
    await expect(sendHttpReplay({ requestId: "fixture", scheme: "auto", rawRequest, verifyTls: true, timeoutSeconds: 15, autoContentLength: true }, new AbortController().signal)).rejects.toMatchObject({ code: "REPEATER_INVALID_RAW" })
    expect(transport.post).not.toHaveBeenCalled()
  })
  it("never takes a Host-looking body line as the destination", () => {
    expect(deriveReplayTarget("POST / HTTP/1.1\r\nHost: actual.test\r\n\r\nHost: body.test", "auto")).toBe("http://actual.test/")
  })
  it("accepts the reported header-only GET without requiring a final empty line", async () => {
    const rawRequest = "GET /container?restype=container&comp=list HTTP/1.1\nHost: storage.skyshield.example\nAccept: application/xml"
    await sendHttpReplay({ requestId: "fixture", scheme: "auto", rawRequest, timeoutSeconds: 15, verifyTls: true, autoContentLength: true }, new AbortController().signal)
    expect(transport.post).toHaveBeenCalledTimes(1)
    expect(transport.post.mock.calls[0][1]).toEqual(expect.objectContaining({ targetUrl: "http://storage.skyshield.example/", rawRequest: rawRequest + "\n\n" }))
  })
  it.each(["\n", "\r\n"])("completes a header-only %j message without creating a body", eol => {
    const headers = `GET / HTTP/1.1${eol}Host: example.test${eol}Accept: application/xml`
    for (const value of [headers, headers + eol, headers + eol + eol]) expect(normalizeReplayRequest(value)).toBe(headers + eol + eol)
  })
  it("preserves every body byte in complete messages", () => {
    const request = "POST / HTTP/1.1\r\nHost: example.test\r\nContent-Length: 12\r\n\r\n\n  a=b\r\n\r\n"
    expect(normalizeReplayRequest(request)).toBe(request)
  })
  it.each([
    ["POST / HTTP/1.1\nHost: example.test\nContent-Length: 10", "raw_missing_body"],
    ["POST / HTTP/1.1\nHost: example.test\nTransfer-Encoding: chunked", "raw_missing_body"],
    ["POST / HTTP/1.1\nHost: example.test\n{\"value\":1}", "raw_missing_separator"],
    ["GET / HTTP/3\nHost: example.test", "raw_http_version"],
    ["GET / HTTP/1.1\nHost: first.test\nHost: second.test", "raw_duplicate_host"],
  ])("explains incomplete requests without sending them", async (rawRequest, reason) => {
    await expect(sendHttpReplay({ requestId: "fixture", scheme: "auto", rawRequest, timeoutSeconds: 15, verifyTls: true, autoContentLength: true }, new AbortController().signal)).rejects.toMatchObject({ reason })
    expect(transport.post).not.toHaveBeenCalled()
  })
  it("keeps backend framing diagnostics specific", () => {
    expect(replayValidationReason({ response: { data: { error: { message: "Body length does not match its headers, or multiple messages were supplied" } } } })).toBe("raw_body_length")
  })
  it.each(["HTTP/2", "HTTP/2.0"])("accepts the reported %s text and defaults to HTTPS without downgrading", async version => {
    const rawRequest = `GET /container?restype=container&comp=list ${version}\nHost: storage.skyshield.example\nAccept: application/xml`
    await sendHttpReplay({ requestId: "fixture", scheme: "auto", rawRequest, verifyTls: true, timeoutSeconds: 15, autoContentLength: true }, new AbortController().signal)
    expect(transport.post).toHaveBeenCalledTimes(1)
    expect(transport.post.mock.calls[0][1]).toEqual(expect.objectContaining({ targetUrl: "https://storage.skyshield.example/", rawRequest: rawRequest + "\n\n" }))
  })
  it("allows explicit cleartext HTTP/2", () => {
    expect(deriveReplayTarget("GET / HTTP/2.0\nHost: example.test:8080\n\n", "http")).toBe("http://example.test:8080/")
  })
})
