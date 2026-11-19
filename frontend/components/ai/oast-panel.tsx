"use client"

import * as React from "react"
import { useLocale, useTranslations } from "next-intl"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { CopyButton } from "@/components/shared/feedback/copy-button"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { useOASTEvents, useOASTProbes } from "@/hooks/use-oast"
import { normalizeError } from "@/lib/errors/normalize-error"
import { newRequestId } from "@/lib/request-id"
import type { OASTEvent, OASTScope } from "@/types/oast.types"

function eventText(event: OASTEvent) {
  if (event.protocol === "dns") return `${event.queryName ?? ""}\n${event.queryType ?? ""}`
  let body = ""
  try { body = new TextDecoder().decode(Uint8Array.from(atob(event.bodyBase64 ?? ""), c => c.charCodeAt(0))) }
  catch { body = "[base64 decode error]" }
  return `${event.method ?? ""} ${event.path ?? ""}\nHost: ${event.host ?? ""}\n${event.headers ?? ""}\n${body}`
}

function CallbackAddress({ label, value }: { label: string; value: string }) {
  const t = useTranslations("oast")
  // Plain text deliberately avoids link previews or accidental self-callbacks.
  return <div className="min-w-0 space-y-1"><div className="text-xs text-muted-foreground">{label}</div><div className="flex min-w-0 items-start gap-2"><code className="min-w-0 flex-1 break-all text-xs">{value}</code><CopyButton value={value} copyLabel={t("copy")} copiedLabel={t("copied")} /></div></div>
}

function ProbeEvents({ scope, id, readOnly, revoke, revoking }: { scope: OASTScope; id: string; readOnly: boolean; revoke: () => void; revoking: boolean }) {
  const t = useTranslations("oast")
  const locale = useLocale()
  const events = useOASTEvents(scope, id)
  const probe = events.data?.probe
  if (events.error) return <AppErrorState variant="section" error={normalizeError(events.error)} onRetry={events.refetch} />
  if (!probe) return <Skeleton className="h-32 w-full" />
  return <section className="min-w-0 space-y-4 rounded-lg border p-4" aria-label={t("evidence")}>
    <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="break-all font-medium">{probe.label || probe.id}</h3><div className="flex items-center gap-2"><Badge variant="outline">{t(`state.${probe.state}`)}</Badge><Button size="sm" variant="outline" onClick={() => void events.refetch()} disabled={events.isFetching}>{t("refresh")}</Button>{!readOnly && (probe.state === "active" || probe.state === "saturated") && <Button size="sm" variant="outline" disabled={revoking} onClick={revoke}>{t("revoke")}</Button>}</div></div>
    <CallbackAddress label="HTTP / HTTPS" value={probe.httpUrl} />
    {probe.dnsName && <CallbackAddress label="DNS" value={probe.dnsName} />}
    <p className="text-xs text-muted-foreground">{t("expires", { time: new Date(probe.expiresAt).toLocaleString(locale) })} · {t("count", { count: probe.eventCount })}</p>
    {probe.state === "saturated" && <p role="status" className="text-sm text-muted-foreground">{t("saturated")}</p>}
    <p className="text-xs text-muted-foreground">{t("peerNote")}</p>
    {events.data?.items.map(event => <details key={event.id} className="rounded-md border p-3">
      <summary className="cursor-pointer break-all text-sm"><span className="font-mono">{event.protocol.toUpperCase()}</span> · {event.remoteAddr} · {new Date(event.receivedAt).toLocaleString(locale)}{event.protocol === "dns" ? ` · ${event.queryType}` : ` · ${event.method}`}</summary>
      {event.truncated && <p className="mt-2 text-xs text-muted-foreground">{t("truncated")}</p>}
      <pre className="mt-3 max-h-80 overflow-auto whitespace-pre-wrap break-all rounded bg-muted p-3 text-xs">{eventText(event)}</pre>
      {event.bodyBase64 && <div className="mt-2 flex items-center gap-2 text-xs text-muted-foreground">{t("bodyBase64")}<CopyButton value={event.bodyBase64} copyLabel={t("copy")} copiedLabel={t("copied")} /></div>}
    </details>)}
    {events.data?.items.length === 0 && <p role="status" className="py-6 text-center text-sm text-muted-foreground">{t("waiting")}</p>}
    {events.data?.hasMore && <p role="status" className="text-xs text-muted-foreground">{t("moreEvents")}</p>}
  </section>
}

export function OASTPanel({ scope, readOnly = false }: { scope: OASTScope; readOnly?: boolean }) {
  const t = useTranslations("oast")
  const [label, setLabel] = React.useState("")
  const [ttl, setTTL] = React.useState("3600")
  const [selected, setSelected] = React.useState("")
  const [cursors, setCursors] = React.useState<string[]>([""])
  const request = React.useRef<{ fingerprint: string; id: string } | null>(null)
  const { list, create, revoke } = useOASTProbes(scope, cursors.at(-1) ?? "")
  const labelId = React.useId()
  const createProbe = async (event: React.FormEvent) => {
    event.preventDefault()
    const fingerprint = JSON.stringify([scope.kind, scope.id, label.trim(), ttl])
    try {
      if (request.current?.fingerprint !== fingerprint) request.current = { fingerprint, id: newRequestId() }
      const probe = await create.mutateAsync({ label: label.trim(), ttlSeconds: Number(ttl), requestId: request.current.id })
      setSelected(probe.id); setCursors([""]); setLabel(""); request.current = null
    } catch { toast.error(t("createError")) }
  }
  const stopProbe = async () => {
    try { await revoke.mutateAsync(selected); toast.success(t("revoked")) }
    catch { toast.error(t("revokeError")) }
  }
  if (list.error) return <AppErrorState variant="section" error={normalizeError(list.error)} onRetry={list.refetch} />
  if (!list.data) return <Skeleton className="h-40 w-full" />
  return <div className="space-y-4">
    <p className="text-sm text-muted-foreground">{t("description")}</p>
    {!list.data.enabled && <p role="status" className="rounded-md border p-3 text-sm">{t("disabled")}</p>}
    {!list.data.dnsEnabled && list.data.enabled && <p className="text-xs text-muted-foreground">{t("httpOnly")}</p>}
    {!readOnly && <form onSubmit={createProbe} className="flex flex-wrap items-end gap-3">
      <div className="min-w-48 flex-1 space-y-1.5"><Label htmlFor={labelId}>{t("label")}</Label><Input id={labelId} value={label} onChange={event => setLabel(event.target.value)} placeholder={t("labelPlaceholder")} maxLength={200} disabled={create.isPending} /></div>
      <div className="space-y-1.5"><Label>{t("ttl")}</Label><Select value={ttl} onValueChange={setTTL} disabled={create.isPending}><SelectTrigger aria-label={t("ttl")} className="w-32"><SelectValue /></SelectTrigger><SelectContent>{[1, 6, 24].map(hours => <SelectItem key={hours} value={String(hours * 3600)}>{t("hours", { hours })}</SelectItem>)}</SelectContent></Select></div>
      <Button type="submit" disabled={!list.data.enabled || create.isPending}>{create.isPending ? t("creating") : t("create")}</Button>
    </form>}
    <div className="flex items-center justify-between gap-2"><h3 className="text-sm font-medium">{t("probes")}</h3><Button variant="outline" size="sm" onClick={() => void list.refetch()} disabled={list.isFetching}>{t("refresh")}</Button></div>
    {list.data.items.length === 0 && <p className="rounded-md border py-8 text-center text-sm text-muted-foreground">{t("empty")}</p>}
    <div className="grid max-h-64 gap-2 overflow-auto sm:grid-cols-2">{list.data.items.map(probe => <button key={probe.id} type="button" aria-pressed={selected === probe.id} onClick={() => setSelected(probe.id)} className="flex min-w-0 items-center justify-between gap-2 rounded-md border p-3 text-left text-sm hover:bg-muted aria-pressed:bg-muted">
      <span className="min-w-0"><span className="block truncate font-medium">{probe.label || probe.id}</span><span className="block text-xs text-muted-foreground">{t(`state.${probe.state}`)} · {t("count", { count: probe.eventCount })}</span></span><span className="shrink-0 text-xs text-muted-foreground">{t("inspect")}</span>
    </button>)}</div>
    <div className="flex gap-2">{cursors.length > 1 && <Button variant="outline" size="sm" onClick={() => setCursors(value => value.slice(0, -1))}>{t("previous")}</Button>}{list.data.nextBefore && <Button variant="outline" size="sm" onClick={() => setCursors(value => [...value, list.data!.nextBefore])}>{t("next")}</Button>}</div>
    {selected && <ProbeEvents key={selected} scope={scope} id={selected} readOnly={readOnly} revoke={() => void stopProbe()} revoking={revoke.isPending} />}
  </div>
}
