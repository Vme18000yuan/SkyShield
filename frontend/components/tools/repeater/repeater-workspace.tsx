"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { useAuth } from "@/hooks/use-auth"
import { useHttpRepeater } from "@/hooks/use-http-repeater"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { CodeEditor } from "@/components/shared/editors/code-editor"
import { HttpRawMessage, httpRawLabels, HTTP_RAW_EXCHANGE_LAYOUT } from "@/components/shared/response-evidence"
import { CopyButton } from "@/components/shared/feedback/copy-button"
import { textRole } from "@/lib/typography"
import { cn, formatBytes } from "@/lib/utils"

export function RepeaterWorkspace() {
  const user = useAuth().data?.user?.id
  return <RepeaterWorkspaceInner key={user ?? "anonymous"} enabled={!!user} />
}

function RepeaterWorkspaceInner({ enabled }: { enabled: boolean }) {
  const state = useHttpRepeater()
  const t = useTranslations("repeater")
  const rawLabels = httpRawLabels(useTranslations("httpRaw"))
  const tab = state.active, result = state.selected?.result
  const error = tab.error || state.selected?.error || result?.errorCode
  const historyIndex = tab.history.findIndex(entry => entry.id === tab.selected)
  const draft = tab.draft
  const absoluteScheme = /^[^\s]+\s+(https?):\/\//i.exec(draft.rawRequest)?.[1].toLowerCase()
  const edit = (update: Partial<typeof draft>) => state.patch(tab.id, update)
  const [hex, setHex] = React.useState(false)
  React.useEffect(() => setHex(false), [state.activeId, tab.selected])
  const hexText = React.useMemo(() => {
    if (!hex || !result?.responseBase64) return ""
    try {
      const bytes = Uint8Array.from(atob(result.responseBase64), c => c.charCodeAt(0)).subarray(0, 65536)
      const lines: string[] = []
      for (let i = 0; i < bytes.length; i += 16) {
        const chunk = bytes.subarray(i, i + 16)
        lines.push(i.toString(16).padStart(8, "0") + "  " + Array.from(chunk, b => b.toString(16).padStart(2, "0")).join(" ").padEnd(47) + "  " + Array.from(chunk, b => b >= 32 && b < 127 ? String.fromCharCode(b) : ".").join(""))
      }
      return lines.join("\n")
    } catch { return "" }
  }, [hex, result?.responseBase64])
  function download() {
    if (!result?.responseBase64) return
    const bytes = Uint8Array.from(atob(result.responseBase64), c => c.charCodeAt(0))
    const url = URL.createObjectURL(new Blob([bytes], { type: "application/octet-stream" }))
    const link = document.createElement("a"); link.href = url; link.download = `response-${result.requestId}.http`; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return <div className="space-y-3" onKeyDown={event => { if ((event.ctrlKey || event.metaKey) && event.key === "Enter") { event.preventDefault(); if (enabled && !tab.pending) void state.send() } }}>
    <div className="flex flex-wrap items-center gap-2" role="tablist" aria-label={t("sessions")}>
      {state.tabs.map(item => <div key={item.id} className="flex items-center rounded border">
        <Button role="tab" aria-selected={item.id === tab.id} variant={item.id === tab.id ? "secondary" : "ghost"} size="sm" onClick={() => state.setActiveId(item.id)}>{t("tab", { number: item.number })}{item.pending ? " …" : ""}</Button>
        <Button variant="ghost" size="icon-sm" aria-label={t("closeTab", { number: item.number })} onClick={() => state.close(item.id)}>×</Button>
      </div>)}
      <Button variant="outline" size="sm" disabled={state.tabs.length >= 6} onClick={() => state.add()}>{t("newTab")}</Button>
      <Button variant="ghost" size="sm" disabled={state.tabs.length >= 6} onClick={() => state.add(true)}>{t("duplicateTab")}</Button>
    </div>
    <div className="space-y-3 rounded-lg border bg-card p-3">
      <div className="flex flex-wrap items-end gap-2">
        <label className="flex items-center gap-2 text-sm">{t("protocol")}<select aria-label={t("protocol")} className="h-9 rounded border bg-background px-2" value={absoluteScheme ?? draft.scheme} disabled={tab.pending || !!absoluteScheme} onChange={e => edit({ scheme: e.target.value as typeof draft.scheme })}>
          <option value="auto">{t("automatic")}</option><option value="http">HTTP</option><option value="https">HTTPS</option>
        </select></label>
        <Button disabled={!enabled || tab.pending || !draft.rawRequest.trim()} onClick={() => void state.send()}>{tab.pending ? t("sending") : t("send")}</Button>
        <Button variant="outline" disabled={!tab.pending} onClick={state.cancel}>{t("cancel")}</Button>
      </div>
      <div className="flex flex-wrap items-center gap-4 text-sm">
        <label className="flex items-center gap-2"><input type="checkbox" checked={draft.autoContentLength} disabled={tab.pending} onChange={e => edit({ autoContentLength: e.target.checked })} />{t("autoLength")}</label>
        <label className="flex items-center gap-2"><input type="checkbox" checked={draft.verifyTls} disabled={tab.pending} onChange={e => edit({ verifyTls: e.target.checked })} />{t("verifyTls")}</label>
        <label className="flex items-center gap-2">{t("timeout")}<Input aria-label={t("timeout")} className="w-20" type="number" min={1} max={60} disabled={tab.pending} value={draft.timeoutSeconds} onChange={e => edit({ timeoutSeconds: Math.max(1, Math.min(60, Number(e.target.value) || 15)) })} /></label>
      </div>
      <p className={textRole.helperText}>{t("rawTargetHint")}</p>
    </div>
    <div className="flex flex-wrap items-center gap-2">
      <label className="flex min-w-0 flex-1 items-center gap-2 text-sm">{t("history")}<select aria-label={t("history")} className="h-9 min-w-0 flex-1 rounded border bg-background px-2" disabled={tab.pending || !tab.history.length} value={tab.selected ?? ""} onChange={e => state.restore(e.target.value)}>
        <option value="" disabled>{t("noHistory")}</option>
        {tab.history.map((entry, i) => <option key={entry.id} value={entry.id}>{i + 1} · {new Date(entry.at).toLocaleTimeString()} · {entry.draft.rawRequest.split("\n")[0]} · {entry.result?.statusCode || "—"}</option>)}
      </select></label>
      <Button size="sm" variant="outline" disabled={tab.pending || historyIndex <= 0} onClick={() => state.restore(tab.history[historyIndex - 1].id)}>{t("previous")}</Button>
      <Button size="sm" variant="outline" disabled={tab.pending || historyIndex < 0 || historyIndex >= tab.history.length - 1} onClick={() => state.restore(tab.history[historyIndex + 1].id)}>{t("next")}</Button>
    </div>
    {error ? <p role="alert" className="rounded border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{t(`errors.${error}`)}</p> : null}
    {result?.representation === "http2_decoded" ? <p className={textRole.helperText}>{t("http2ViewHint")}</p> : null}
    {result?.normalizedHeaders?.length ? <p className={textRole.helperText}>{t("normalizedHeaders", { headers: result.normalizedHeaders.join(", ") })}</p> : null}
    <div className={HTTP_RAW_EXCHANGE_LAYOUT}>
      <section className="min-w-0 space-y-2" aria-label={t("request")}>
        <div className="flex items-center justify-between"><h3 className={textRole.sectionTitle}>{t("request")} · Raw</h3><CopyButton value={draft.rawRequest} copyLabel={rawLabels.copy} copiedLabel={rawLabels.copied} copyFailedLabel={rawLabels.copyFailed} /></div>
        <CodeEditor key={tab.id} value={draft.rawRequest} placeholder={t("rawPlaceholder")} onChange={rawRequest => edit({ rawRequest })} language="plaintext" readOnly={tab.pending || !enabled} showFoldGutter={false} className="h-[28rem]" />
        {result ? <details><summary className="cursor-pointer text-sm">{t("sentRequest")}</summary><HttpRawMessage value={result.requestRaw} side="request" labels={rawLabels} partial={!result.sent} /></details> : null}
      </section>
      <section className="min-w-0 space-y-2" aria-label={t("response")}>
        {result?.connectionTarget ? <p className={cn("break-all", textRole.helperText)}>{t("resolvedTarget")}: {result.connectionTarget}</p> : null}
        <div className="flex flex-wrap items-center gap-2"><h3 className={cn("flex-1", textRole.sectionTitle)}>{t("response")}</h3>{result ? <span className={textRole.helperText}>{result.responseProtocol ? `${result.responseProtocol} · ` : ""}{result.statusCode || "—"} · {result.elapsedMs} ms · {formatBytes(result.byteCount)}</span> : null}
          <Button size="sm" variant="outline" disabled={!result?.responseBase64} aria-pressed={hex} onClick={() => setHex(!hex)}>{hex ? "Raw" : "Hex"}</Button><Button size="sm" variant="outline" disabled={!result?.responseBase64} onClick={download}>{t(result?.representation === "http2_decoded" ? "downloadDecoded" : "download")}</Button>
        </div>
        {hex ? <div><p className={textRole.helperText}>{t("hexHint")}</p><pre className={cn("max-h-[60vh] overflow-auto rounded border bg-muted p-3", textRole.code)}>{hexText}</pre></div> : <HttpRawMessage value={result?.responseRaw} side="response" labels={rawLabels} loading={tab.pending} partial={!!result && (result.truncated || result.binary || !!result.errorCode)} />}
        {result?.binary ? <p className={textRole.helperText}>{t("binary")}</p> : null}
      </section>
    </div>
    <p className={textRole.helperText}>{t("historyHint")}</p>
  </div>
}
