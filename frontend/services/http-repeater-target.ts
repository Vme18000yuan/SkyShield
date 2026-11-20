import type { RepeaterDraft } from "@/types/http-repeater.types"

export class RepeaterTargetError extends Error {
  readonly code = "REPEATER_INVALID_RAW"
  constructor(readonly reason = "raw_invalid_host") { super(reason) }
}

function requestLine(line: string): RegExpExecArray {
  const first = /^([^\s]+) ([^\s]+) HTTP\/(1\.[01]|2(?:\.0)?)$/.exec(line)
  if (!first) throw new RepeaterTargetError(/ HTTP\/3(?:\.\d)?$/.test(line) ? "raw_http_version" : "raw_request_line")
  if (first[1] === "CONNECT") throw new RepeaterTargetError("raw_connect")
  return first
}

/** Complete an unambiguous header-only paste. Never guess or trim a Body. */
export function normalizeReplayRequest(raw: string): string {
  const boundary = /\r\n\r\n|\n\n/.exec(raw)
  const head = boundary ? raw.slice(0, boundary.index) : raw
  if (new TextEncoder().encode(head).length > 65536) throw new RepeaterTargetError("raw_header_limit")
  if (boundary) return raw
  const normalizedHead = head.replaceAll("\r\n", "\n")
  if (normalizedHead.includes("\r")) throw new RepeaterTargetError("raw_line_endings")
  const lines = normalizedHead.split("\n")
  if (lines.at(-1) === "") lines.pop()
  const first = requestLine(lines[0] ?? "")
  let length: string | undefined
  let transferEncoding = false
  for (const line of lines.slice(1)) {
    const header = /^([!#$%&'*+.^_`|~\w-]+):[ \t]*(.*)$/.exec(line)
    if (!header || line.includes("\x00")) throw new RepeaterTargetError("raw_missing_separator")
    const key = header[1].toLowerCase(), value = header[2].trim()
    if (key === "content-length") {
      if (length !== undefined) throw new RepeaterTargetError("raw_duplicate_length")
      if (!/^\d+$/.test(value)) throw new RepeaterTargetError("raw_content_length")
      length = value
    }
    if (key === "transfer-encoding") transferEncoding = true
  }
  if (transferEncoding || length !== undefined && Number(length) > 0) throw new RepeaterTargetError("raw_missing_body")
  if (!["GET", "HEAD", "OPTIONS", "TRACE", "DELETE"].includes(first[1]) && length === undefined) throw new RepeaterTargetError("raw_missing_separator")
  if (raw.endsWith("\r\n")) return raw + "\r\n"
  if (raw.endsWith("\n")) return raw + "\n"
  return raw + (raw.includes("\r\n") ? "\r\n\r\n" : "\n\n")
}

export function replayValidationReason(cause: unknown): string {
  const error = cause as { code?: string; reason?: string; response?: { data?: { error?: string | { message?: string }; message?: string } } }
  if (error.code === "REPEATER_INVALID_RAW") return error.reason ?? "invalid_request"
  const remote = error.response?.data?.error
  const message = typeof remote === "string" ? remote : remote?.message ?? error.response?.data?.message
  const reasons: Record<string, string> = {
    "Invalid requestId": "raw_request_id",
    "Raw request must be UTF-8 text, at most 512 KiB": "request_limit",
    "Use an HTTP(S) target URL without credentials or fragment": "raw_invalid_host",
    "Invalid destination host": "raw_invalid_host",
    "Invalid destination hostname": "raw_invalid_host",
    "Destination port must be between 1 and 65535": "raw_invalid_port",
    "Timeout must be between 1 and 60 seconds": "raw_timeout",
    "Separate headers and body with a blank line; headers must fit in 64 KiB": "raw_missing_separator",
    "Invalid header line ending or NUL byte": "raw_line_endings",
    "Use a single HTTP/1.0 or HTTP/1.1 request; CONNECT is not supported": "raw_request_line",
    "Use a single HTTP/1.0, HTTP/1.1 or HTTP/2 request; CONNECT is not supported": "raw_request_line",
    "HTTP/2 messages must not contain Transfer-Encoding": "raw_http2_transfer",
    "Connection cannot nominate Host or Content-Length for HTTP/2": "raw_http2_connection",
    "HTTP/2 TE header may only contain trailers": "raw_http2_te",
    "Invalid HTTP header": "raw_invalid_header",
    "Duplicate Content-Length is not supported": "raw_duplicate_length",
    "Protocol upgrade is not supported by single HTTP replay": "raw_upgrade",
    "Content-Length and Transfer-Encoding cannot be combined": "raw_framing_conflict",
    "Raw request is not a valid HTTP message": "raw_invalid_http",
    "HTTP/1.1 requires a Host header": "raw_missing_host",
    "Invalid absolute request URL": "raw_invalid_host",
    "Body length or chunk framing is invalid": "raw_body_length",
    "Body length does not match its headers, or multiple messages were supplied": "raw_body_length",
  }
  return message ? reasons[message] ?? "invalid_request" : "invalid_request"
}

function authorityURL(authority: string): URL {
  if (!authority || authority.length > 8192 || /[/\\?#@%\s]/u.test(authority) || authority.includes("\x00") || authority.endsWith(":")) throw new RepeaterTargetError()
  if ((authority.match(/:/g)?.length ?? 0) > 1 && !authority.startsWith("[")) throw new RepeaterTargetError()
  try {
    const value = new URL(`http://${authority}`)
    if (!value.hostname || value.username || value.password || value.pathname !== "/" || value.search || value.hash || value.port === "0") throw new RepeaterTargetError()
    return value
  } catch { throw new RepeaterTargetError() }
}

// Transport-side request shaping: the page owns only Raw text and a protocol
// preference. The existing sender API continues validating the derived URL,
// the complete HTTP message, permissions and destination policy.
export function deriveReplayTarget(raw: string, scheme: RepeaterDraft["scheme"]): string {
  if (!["auto", "http", "https"].includes(scheme)) throw new RepeaterTargetError("raw_scheme")
  const boundary = /\r\n\r\n|\n\n/.exec(raw)
  if (!boundary) throw new RepeaterTargetError("raw_missing_separator")
  if (new TextEncoder().encode(raw.slice(0, boundary.index)).length > 65536) throw new RepeaterTargetError("raw_header_limit")
  const lines = raw.slice(0, boundary.index).split(/\r\n|\n/)
  const first = requestLine(lines[0])
  let host: string | undefined
  for (const line of lines.slice(1)) {
    const header = /^Host:[ \t]*(.*)$/i.exec(line)
    if (!header) continue
    if (host !== undefined) throw new RepeaterTargetError("raw_duplicate_host")
    host = header[1].replace(/[ \t]+$/, "")
    authorityURL(host)
  }
  const target = first[2]
  if (/^[a-z][a-z0-9+.-]*:/i.test(target)) {
    const absolute = /^(https?):\/\/([^/?#]+)(?:[/?]|$)/i.exec(target)
    if (!absolute || target.includes("#")) throw new RepeaterTargetError()
    authorityURL(absolute[2])
    // URL.host drops explicit default ports. Validate without serializing the
    // authority so the connection target preserves what the user supplied.
    return `${absolute[1].toLowerCase()}://${absolute[2]}/`
  }
  if (target[0] !== "/" && target !== "*") throw new RepeaterTargetError("raw_request_line")
  if (host === undefined) throw new RepeaterTargetError("raw_missing_host")
  const parsed = authorityURL(host)
  const protocol = scheme === "auto" ? parsed.port === "443" || first[3] === "2" || first[3] === "2.0" ? "https" : "http" : scheme
  return `${protocol}://${host}/`
}
