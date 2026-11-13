"use client"

import * as React from "react"
import { Button } from "@/components/ui/button"
import { CopyButton } from "@/components/shared/feedback/copy-button"
import { textRole } from "@/lib/typography"
import { cn } from "@/lib/utils"
import { inspectHttpRaw, type HttpMessageSide } from "./http-raw"

const labelKeys = ["request", "response", "raw", "bodyOnly", "bodyOnlyHint", "incomplete", "incompleteHint", "notCaptured", "wrap", "copy", "copyPartial", "copied", "copyFailed", "partial", "loading"] as const
export type HttpRawLabels = Record<(typeof labelKeys)[number], string>
export function httpRawLabels(t: (key: (typeof labelKeys)[number]) => string): HttpRawLabels {
  return Object.fromEntries(labelKeys.map(key => [key, t(key)])) as HttpRawLabels
}

export const HTTP_RAW_EXCHANGE_LAYOUT = "grid min-w-0 gap-3 grid-cols-[repeat(auto-fit,minmax(min(100%,24rem),1fr))]"

type HttpRawMessageProps = {
  value?: string | null
  side: HttpMessageSide
  labels: HttpRawLabels
  bodyOnly?: boolean
  partial?: boolean
  loading?: boolean
  error?: string
  className?: string
}

function RawText({ value, headerLength }: { value: string; headerLength: number }) {
  if (!headerLength) return <>{value}</>
  // Keep every original line ending and the empty line separating headers/body.
  const lines = value.slice(0, headerLength).match(/[^\r\n]*(?:\r\n|\n|\r|$)/g)?.filter(Boolean) ?? []
  return <>{lines.map((line, index) => {
    if (index === 0) return <span key={index} className="font-semibold text-primary">{line}</span>
    const colon = line.indexOf(":")
    return colon > 0 ? <React.Fragment key={index}><span className="font-medium text-foreground">{line.slice(0, colon + 1)}</span>{line.slice(colon + 1)}</React.Fragment> : <React.Fragment key={index}>{line}</React.Fragment>
  })}{value.slice(headerLength)}</>
}

/** Read-only HTTP text, with copy bound to the untouched source string. */
export function HttpRawMessage({ value = "", side, labels, bodyOnly = false, partial = false, loading = false, error, className }: HttpRawMessageProps) {
  const raw = value ?? ""
  const [wrap, setWrap] = React.useState(false)
  const info = React.useMemo(() => inspectHttpRaw(raw, side, bodyOnly), [raw, side, bodyOnly])
  const numbers = React.useMemo(() => Array.from({ length: info.lineCount }, (_, i) => i + 1).join("\n"), [info.lineCount])
  const mode = info.bodyOnly ? labels.bodyOnly : info.incomplete ? labels.incomplete : labels.raw
  return <section className={cn("flex min-w-0 flex-col overflow-hidden rounded-lg border border-border bg-card", className)} aria-label={labels[side]}>
    <div className="flex flex-wrap items-center justify-between gap-2 border-b bg-muted/30 px-3 py-2">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <h4 className={textRole.sectionTitle}>{labels[side]}</h4>
        {raw ? <span className={cn("rounded border px-1.5 py-0.5 text-muted-foreground", textRole.badge)}>{mode}</span> : null}
      </div>
      <div className="flex items-center gap-1">
        <Button size="sm" variant="ghost" aria-pressed={wrap} onClick={() => setWrap(current => !current)} disabled={!raw}>{labels.wrap}</Button>
        <CopyButton value={raw} copyLabel={partial || info.bodyOnly || info.incomplete ? labels.copyPartial : labels.copy} copiedLabel={labels.copied} copyFailedLabel={labels.copyFailed} disabled={!raw || loading || !!error} />
      </div>
    </div>
    {error ? <p role="alert" className="p-3 text-sm text-destructive">{error}</p> : loading ? <p role="status" className={cn("p-3", textRole.helperText)}>{labels.loading}</p> : !raw ? <p className={cn("p-3", textRole.helperText)}>{labels.notCaptured}</p> : <>
      <div role="region" aria-label={`${labels[side]} ${labels.raw}`} tabIndex={0} className={cn("flex max-h-[60vh] min-h-40 overflow-auto overscroll-contain focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring", textRole.code)}>
        {!wrap ? <pre aria-hidden="true" className={cn(textRole.code, "sticky left-0 z-10 select-none border-r bg-muted px-2 py-3 text-right leading-5 text-muted-foreground")}>{numbers}</pre> : null}
        <pre data-http-raw className={cn(textRole.code, "m-0 flex-1 p-3 leading-5 text-foreground", wrap ? "min-w-0 whitespace-pre-wrap break-all" : "w-max min-w-max whitespace-pre")}><RawText value={raw} headerLength={info.headerLength} /></pre>
      </div>
      {info.bodyOnly || info.incomplete || partial ? <p className={cn("border-t px-3 py-2", textRole.helperText)}>{partial ? labels.partial : info.bodyOnly ? labels.bodyOnlyHint : labels.incompleteHint}</p> : null}
    </>}
  </section>
}

export function HttpRawExchange({ request, response, responseBodyOnly = false, partial = false, labels }: {
  request?: string | null
  response?: string | null
  responseBodyOnly?: boolean
  partial?: boolean
  labels: HttpRawLabels
}) {
  return <div className={HTTP_RAW_EXCHANGE_LAYOUT}>
    <HttpRawMessage value={request} side="request" labels={labels} partial={partial} />
    <HttpRawMessage value={response} side="response" bodyOnly={responseBodyOnly} labels={labels} partial={partial} />
  </div>
}
