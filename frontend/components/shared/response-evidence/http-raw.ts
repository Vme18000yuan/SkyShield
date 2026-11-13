export type HttpMessageSide = "request" | "response"

/** The traffic recorder appends this marker outside a truncated body preview. */
export function trafficRawPreview(value: string) {
  const marker = /\n…\[truncated\] @blob sha256:([a-f0-9]{64}) \(len=\d+\)$/.exec(value)
  return { text: marker ? value.slice(0, marker.index) : value, partial: !!marker, blob: marker?.[1] }
}

/** Join separately recorded fields without adding a byte to the HTTP body. */
export function joinHttpHeadBody(head: string, body: string): string {
  if (!head) return body
  if (/(?:\r\n\r\n|\n\n|\r\r)$/.test(head)) return head + body
  const eol = head.includes("\r\n") ? "\r\n" : head.includes("\n") ? "\n" : "\r\n"
  return head + (head.endsWith(eol) ? eol : eol + eol) + body
}

/** Inspect presentation only. Never parse/rewrite a JSON body or guess headers. */
export function inspectHttpRaw(value: string, side: HttpMessageSide, bodyOnly = false) {
  const firstLine = value.split(/\r\n|\n|\r/, 1)[0]
  const startLine = !bodyOnly && (side === "response"
    ? /^HTTP\/\d(?:\.\d)? [0-9]{3}(?:[ \t]|$)/.test(firstLine)
    : /^[!#$%&'*+.^_`|~\w-]+ \S+ HTTP\/\d(?:\.\d)?[ \t]*$/.test(firstLine))
  const legacyStart = !bodyOnly && side === "response" && /^HTTP [0-9]{3}(?:[ \t]|$)/.test(firstLine)
  const boundary = /\r\n\r\n|\n\n|\r\r/.exec(value)
  const headerLength = startLine || legacyStart ? boundary ? boundary.index + boundary[0].length : value.length : 0
  return {
    headerLength,
    bodyOnly: !!value && !startLine && !legacyStart,
    incomplete: !!value && (legacyStart || startLine && !boundary),
    lineCount: value ? value.split(/\r\n|\n|\r/).length : 0,
  }
}
