"use client"

import * as React from "react"
import { sendHttpReplay } from "@/services/http-repeater.service"
import { replayValidationReason } from "@/services/http-repeater-target"
import type { RepeaterDraft, RepeaterResult } from "@/types/http-repeater.types"

export interface ReplayHistory { id: string; at: number; draft: RepeaterDraft; result?: RepeaterResult; error?: string }
export interface ReplayTab { id: string; number: number; draft: RepeaterDraft; history: ReplayHistory[]; selected: string | null; pending: boolean; error?: string }
const emptyDraft = (): RepeaterDraft => ({ scheme: "auto", rawRequest: "", timeoutSeconds: 15, verifyTls: true, autoContentLength: true })
const makeTab = (number: number): ReplayTab => ({ id: String(number), number, draft: emptyDraft(), history: [], selected: null, pending: false })

function requestID(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID()
  const bytes = crypto.getRandomValues(new Uint8Array(16)); bytes[6] = (bytes[6] & 15) | 64; bytes[8] = (bytes[8] & 63) | 128
  const hex = Array.from(bytes, b => b.toString(16).padStart(2, "0")).join("")
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

function trimHistory(tabs: ReplayTab[]): ReplayTab[] {
  let next = tabs.map(tab => ({ ...tab, history: tab.history.slice(-10) }))
  const size = (entry: ReplayHistory) => 2 * (entry.draft.rawRequest.length + (entry.result?.requestRaw.length ?? 0) + (entry.result?.responseRaw.length ?? 0) + (entry.result?.responseBase64?.length ?? 0))
  let total = next.reduce((sum, tab) => sum + tab.history.reduce((n, entry) => n + size(entry), 0), 0)
  const removable = next.flatMap(tab => tab.history.filter(entry => entry.id !== tab.selected).map(entry => ({ tab: tab.id, entry }))).sort((a, b) => a.entry.at - b.entry.at)
  for (const item of removable) {
    if (total <= 16 << 20) break
    total -= size(item.entry)
    next = next.map(tab => tab.id === item.tab ? { ...tab, history: tab.history.filter(entry => entry.id !== item.entry.id) } : tab)
  }
  return next
}

export function useHttpRepeater() {
  const [tabs, setTabs] = React.useState<ReplayTab[]>(() => [makeTab(1)])
  const [activeId, setActiveId] = React.useState("1")
  const sequence = React.useRef(1)
  const running = React.useRef(new Map<string, AbortController>())
  const mounted = React.useRef(true)
  React.useEffect(() => {
    mounted.current = true
    const controllers = running.current
    return () => { mounted.current = false; for (const controller of controllers.values()) controller.abort(); controllers.clear() }
  }, [])
  const active = tabs.find(tab => tab.id === activeId) ?? tabs[0]
  const selected = active.history.find(entry => entry.id === active.selected)

  function patch(id: string, update: Partial<RepeaterDraft>) {
    if (running.current.has(id)) return
    setTabs(current => current.map(tab => tab.id === id ? { ...tab, draft: { ...tab.draft, ...update }, error: undefined } : tab))
  }
  function add(duplicate = false) {
    if (tabs.length >= 6) return
    const tab = makeTab(++sequence.current)
    if (duplicate) tab.draft = { ...active.draft }
    setTabs(current => [...current, tab]); setActiveId(tab.id)
  }
  function close(id: string) {
    running.current.get(id)?.abort(); running.current.delete(id)
    const next = tabs.filter(tab => tab.id !== id)
    if (!next.length) next.push(makeTab(++sequence.current))
    setTabs(next); if (activeId === id) setActiveId(next[0].id)
  }
  function restore(id: string) {
    if (active.pending) return
    const entry = active.history.find(item => item.id === id)
    if (entry) setTabs(current => current.map(tab => tab.id === active.id ? { ...tab, draft: { ...entry.draft }, selected: id, error: entry.error } : tab))
  }
  async function send() {
    const tab = active
    if (running.current.has(tab.id)) return
    if (!tab.draft.rawRequest.trim()) {
      setTabs(current => current.map(item => item.id === tab.id ? { ...item, error: "invalid_request" } : item)); return
    }
    if (new TextEncoder().encode(tab.draft.rawRequest).length > 512 << 10) {
      setTabs(current => current.map(item => item.id === tab.id ? { ...item, error: "request_limit" } : item)); return
    }
    const controller = new AbortController(); running.current.set(tab.id, controller)
    const id = requestID(), draft = { ...tab.draft }, at = Date.now()
    setTabs(current => current.map(item => item.id === tab.id ? { ...item, pending: true, selected: null, error: undefined } : item))
    let result: RepeaterResult | undefined, error: string | undefined
    try { result = await sendHttpReplay({ ...draft, requestId: id }, controller.signal) }
    catch (cause) {
      const status = (cause as { response?: { status?: number } }).response?.status
      const invalidRaw = (cause as { code?: string }).code === "REPEATER_INVALID_RAW"
      error = controller.signal.aborted ? "canceled" : invalidRaw ? replayValidationReason(cause) : status === 403 ? "forbidden" : status === 429 ? "busy" : status === 409 ? "duplicate" : status === 413 ? "request_limit" : status === 400 ? replayValidationReason(cause) : "unavailable"
    } finally {
      if (running.current.get(tab.id) === controller) running.current.delete(tab.id)
      // The decoded HTTP/2 view may use origin-form :path and :authority even
      // when the submitted absolute URL selects a different connection host.
      // Keep the replay input so history/resend cannot silently change routing.
      const sentDraft = { ...draft, rawRequest: result?.representation === "http2_decoded" ? draft.rawRequest : result?.requestRaw ?? draft.rawRequest }
      if (mounted.current) setTabs(current => trimHistory(current.map(item => item.id === tab.id ? { ...item, draft: sentDraft, pending: false, selected: id, error, history: [...item.history, { id, at, draft: sentDraft, result, error }] } : item)))
    }
  }
  return { tabs, active, activeId, selected, patch, add, close, restore, send, setActiveId, cancel: () => running.current.get(active.id)?.abort() }
}
