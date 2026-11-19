"use client"

import * as React from "react"
import { useLocale, useTranslations } from "next-intl"
import { IconList, semanticIcons } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { useConversationTimeline } from "@/hooks/use-conversation-timeline"
import { Skeleton } from "@/components/ui/skeleton"
import { textRole } from "@/lib/typography"
import { TimelinePanels, type TimelinePanel } from "./conversation-exploration-panels"
import styles from "./conversation-timeline-map.module.css"
import type { ConversationExploreRun, TimelineText } from "@/types/conversation-exploration.types"
import { TimelineMap } from "./conversation-timeline-map"
import { TimelineDetails, type TraceSelection } from "./conversation-timeline-steps"

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
function restoredPosition(key: string): { run: string; beforeRun: string; from: number } {
  const initial = { run: "", beforeRun: "", from: 1 }
  try {
    const value = JSON.parse(sessionStorage.getItem(key) ?? "null")
    if (value && typeof value.run === "string" && uuid.test(value.run) && Number.isSafeInteger(value.from) && value.from > 0) {
      return { run: value.run, beforeRun: typeof value.beforeRun === "string" && uuid.test(value.beforeRun) ? value.beforeRun : "", from: value.from }
    }
  } catch { /* Storage is optional in private browsers. */ }
  return initial
}

export function ConversationExploration({ conversationId, running }: { conversationId: string; running: boolean }) {
  const user = useAuth().data?.user?.id
  return user ? <ConversationTimelineBody key={`${user}:${conversationId}`} user={user} conversationId={conversationId} running={running} /> : null
}

function ConversationTimelineBody({ conversationId, user, running }: { conversationId: string; user: number; running: boolean }) {
  const t = useTranslations("aiChat.exploration")
  const locale = useLocale()
  const positionKey = `skyshield:exploration:${user}:${conversationId}`
  const [initial] = React.useState(() => restoredPosition(positionKey))
  const [run, setRun] = React.useState(initial.run)
  const [beforeRun, setBeforeRun] = React.useState(initial.beforeRun)
  const [from, setFrom] = React.useState(initial.from)
  const [findingCursors, setFindingCursors] = React.useState([""])
  const [selection, setSelection] = React.useState<{ run: string; item: TraceSelection } | null>(null)
  const [panel, setPanel] = React.useState<TimelinePanel | null>(null)
  const [returnPanel, setReturnPanel] = React.useState<TimelinePanel | null>(null)
  const afterFinding = findingCursors[findingCursors.length - 1]
  React.useEffect(() => {
    try {
      if (run) sessionStorage.setItem(positionKey, JSON.stringify({ run, beforeRun, from }))
      else sessionStorage.removeItem(positionKey)
    } catch { /* The current in-memory selection remains usable. */ }
  }, [positionKey, run, beforeRun, from])
  const query = useConversationTimeline(user, conversationId, run, beforeRun, from, afterFinding, running)
  const graph = query.data
  const active = graph?.run
  const activeId = active?.id
  const select = (item: TraceSelection) => { if (active) { setReturnPanel(panel); setPanel(null); setSelection({ run: active.id, item }) } }
  const selectRun = (id: string, cursor = beforeRun) => {
    setRun(id); setBeforeRun(cursor); setFrom(1); setFindingCursors([""]); setSelection(null); setPanel(null); setReturnPanel(null)
  }
  const go = React.useCallback((step: number, revealSteps = true) => {
    if (!activeId) return
    setRun(activeId); setFrom(step); setSelection(null)
    if (revealSteps) setPanel("steps")
  }, [activeId])
  const record = (title: string, item?: TimelineText) => select({ kind: "record", title, text: item?.text || t("noSummary"), id: item?.activityId })
  const stateLabel = (state: string) => t.has(`states.${state}`) ? t(`states.${state}`) : state
  const runLabel = (entry: ConversationExploreRun) => `${new Date(entry.createdAt).toLocaleString(locale)} · ${stateLabel(entry.status)}`
  const reason = active?.terminalReason && active.terminalReason !== active.status
    ? (t.has(`terminalReasons.${active.terminalReason}`) ? t(`terminalReasons.${active.terminalReason}`) : active.terminalReason)
    : undefined
  const openRequest = () => record(t("request"), graph?.request)
  const openOutcome = () => {
    if (!active || !graph) return
    record([t("runOutcome"), stateLabel(active.status), reason].filter(Boolean).join(" · "), {
      ...graph.conclusion, text: graph.conclusion.text || t(active.running ? "inProgress" : "noConclusion"),
    })
  }
  const selectFindingPage = (forward: boolean) => {
    if (!active) return
    setRun(active.id); setSelection(null)
    setFindingCursors(value => forward && graph?.nextFinding ? [...value, graph.nextFinding] : value.length > 1 ? value.slice(0, -1) : value)
  }
  const choices = active && !graph?.runs.some(item => item.id === active.id) ? [active, ...(graph?.runs ?? [])] : graph?.runs ?? []
  const counts = graph?.counts
  // Keep an open drawer on the stable occurrence while its result arrives.
  const selectedItem = selection && selection.run === active?.id ? selection.item : null
  let detail = selectedItem
  if (selectedItem?.kind === "step") {
    const refreshed = graph?.steps.find(s => s.activityId === selectedItem.step.activityId)
    if (refreshed) detail = { kind: "step", step: refreshed }
  } else if (selectedItem?.kind === "finding") {
    const refreshed = graph?.findings.find(f => f.receiptId === selectedItem.finding.receiptId)
    if (refreshed) detail = { kind: "finding", finding: refreshed }
  }
  return <div className={styles.workspace}>
    <div className="flex flex-wrap items-center gap-2">
      <Select value={active?.id ?? ""} onValueChange={id => selectRun(id)} disabled={!choices.length}>
        <SelectTrigger size="sm" aria-label={t("selectRun")} className="w-full sm:w-auto"><SelectValue placeholder={t("selectRun")}>{active ? runLabel(active) : undefined}</SelectValue></SelectTrigger>
        <SelectContent>{choices.map(entry => <SelectItem key={entry.id} value={entry.id}>{runLabel(entry)}</SelectItem>)}</SelectContent>
      </Select>
      <Button variant="ghost" size="sm" onClick={() => { if (!run && !beforeRun && from === 1) void query.refetch(); selectRun("", "") }}>{t("latestRun")}</Button>
      {graph?.runsBefore && <Button variant="ghost" size="sm" onClick={() => selectRun("", graph.runsBefore)}>{t("olderRuns")}</Button>}
      <Button size="icon-sm" variant="ghost" aria-label={t("refresh")} onClick={() => void query.refetch()} disabled={query.isFetching}><semanticIcons.action.refresh className="size-4" /></Button>
      {active && <div className="ms-auto flex flex-wrap items-center gap-2">
        <Button size="sm" variant="outline" onClick={() => setPanel("steps")}><IconList className="size-3.5" />{t("steps")}</Button>
        <Button size="sm" variant="outline" onClick={() => setPanel("checks")}>{t("panels.checks")}</Button>
        <Button size="sm" variant="outline" onClick={() => setPanel("findings")}><semanticIcons.concept.vulnerability className="size-3.5" />{t("evidenceBranch")}{graph?.findingCount ? ` · ${graph.findingCount}` : ""}</Button>
      </div>}
    </div>
    {query.isError ? <div role="alert" className="p-4">{t("loadError")} <Button size="sm" variant="link" onClick={() => void query.refetch()}>{t("refresh")}</Button></div>
      : !graph ? <Skeleton className="min-h-64 flex-1" /> : !active || !counts ? <p className={textRole.bodySubtle}>{t("empty")}</p> : <>
        <TimelineMap key={active.id} graph={graph} from={from} choose={go} goal={openRequest} outcome={openOutcome} receipts={finding => select({ kind: "finding", finding })} />
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1" aria-label={t("wholeRun")}>
          <span className={textRole.compactPrimary}>{t("stepCount", { count: counts.steps })} · {t("registrations", { count: graph.findingCount })}</span>
          {[["failed", counts.failed], ["pending", counts.pending], ["interrupted", counts.interrupted]].map(([label, value]) => Number(value) > 0 && <span key={label} className={textRole.compactCaption}>{t(`totals.${label}`)} {value}</span>)}
          <span className={textRole.compactCaption}>{t("map.sequenceHint")}</span>
          {reason && <span className={textRole.compactCaption}>{reason}</span>}
        </div>
        {panel && <TimelinePanels panel={panel} change={setPanel} close={() => setPanel(null)} graph={graph} from={from} busy={query.isPlaceholderData} go={go} select={select} record={record} selectFindingPage={selectFindingPage} findingCursors={findingCursors} />}
      </>}
    {detail && <TimelineDetails key={active?.id + ":" + (detail.kind === "record" ? detail.id : detail.kind === "step" ? detail.step.activityId : detail.finding.receiptId)} selected={detail} user={user} conversation={conversationId} close={() => { setSelection(null); setPanel(returnPanel); setReturnPanel(null) }} go={go} />}
  </div>
}
