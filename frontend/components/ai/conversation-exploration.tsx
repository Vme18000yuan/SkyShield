"use client"

import * as React from "react"
import { useQuery } from "@tanstack/react-query"
import { useLocale, useTranslations } from "next-intl"
import { ArrowLeft, ArrowRight, CheckCircle2, Flag, Link2, RefreshCw } from "lucide-react"
import { useAuth } from "@/hooks/use-auth"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { getConversationTimeline } from "@/services/conversation-exploration.service"
import type { TimelineText } from "@/types/conversation-exploration.types"
import { TimelineMap } from "./conversation-timeline-map"
import { TimelineDetails, TimelineSteps, type TraceSelection } from "./conversation-timeline-steps"
import { timelineTaskTitle } from "./conversation-timeline-labels"

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
  const listRef = React.useRef<HTMLElement>(null)
  const afterFinding = findingCursors[findingCursors.length - 1]
  React.useEffect(() => {
    try {
      if (run) sessionStorage.setItem(positionKey, JSON.stringify({ run, beforeRun, from }))
      else sessionStorage.removeItem(positionKey)
    } catch { /* The current in-memory selection remains usable. */ }
  }, [positionKey, run, beforeRun, from])
  const query = useQuery({
    queryKey: ["conversation-timeline", user, conversationId, run, beforeRun, from, afterFinding],
    queryFn: ({ signal }) => getConversationTimeline(conversationId, run, beforeRun, from, afterFinding, signal),
    retry: false,
    placeholderData: previous => previous && run && previous.run?.id === run ? previous : undefined,
    refetchInterval: state => state.state.data?.run?.running || (!run && !beforeRun && running) ? 2500 : false,
  })
  const graph = query.data
  const active = graph?.run
  const select = (item: TraceSelection) => { if (active) setSelection({ run: active.id, item }) }
  const selectRun = (id: string, cursor = beforeRun) => {
    setRun(id); setBeforeRun(cursor); setFrom(1); setFindingCursors([""]); setSelection(null)
  }
  const go = React.useCallback((step: number) => {
    if (!active) return
    setRun(active.id); setFrom(step); setSelection(null)
    listRef.current?.scrollIntoView({ block: "nearest" })
  }, [active?.id])
  const record = (title: string, item?: TimelineText) => select({ kind: "record", title, text: item?.text || t("noSummary"), id: item?.activityId })
  const stateLabel = (state: string) => t.has(`states.${state}`) ? t(`states.${state}`) : state
  const selectFindingPage = (forward: boolean) => {
    if (!active) return
    setRun(active.id); setSelection(null)
    setFindingCursors(value => forward && graph?.nextFinding ? [...value, graph.nextFinding] : value.length > 1 ? value.slice(0, -1) : value)
  }
  const choices = active && !graph?.runs.some(item => item.id === active.id) ? [active, ...(graph?.runs ?? [])] : graph?.runs ?? []
  const selectedSection = graph?.sections.find(s => s.first <= from && from <= s.last)
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
  return <div className="flex h-[76vh] min-h-0 flex-col gap-3">
    <div className="flex flex-wrap items-center gap-2">
      <Select value={active?.id ?? ""} onValueChange={id => selectRun(id)} disabled={!choices.length}>
        <SelectTrigger aria-label={t("selectRun")} className="w-full sm:w-auto"><SelectValue placeholder={t("selectRun")} /></SelectTrigger>
        <SelectContent>{choices.map(r => <SelectItem key={r.id} value={r.id}>{new Date(r.createdAt).toLocaleString(locale)} · {stateLabel(r.status)}</SelectItem>)}</SelectContent>
      </Select>
      <Button variant="outline" size="sm" onClick={() => { if (!run && !beforeRun && from === 1) void query.refetch(); selectRun("", "") }}>{t("latestRun")}</Button>
      {graph?.runsBefore && <Button variant="ghost" size="sm" onClick={() => selectRun("", graph.runsBefore)}>{t("olderRuns")}</Button>}
      <Button size="icon-sm" variant="ghost" aria-label={t("refresh")} onClick={() => void query.refetch()} disabled={query.isFetching}><RefreshCw className="size-4" /></Button>
    </div>
    {query.isError ? <div role="alert" className="p-4">{t("loadError")} <Button size="sm" variant="link" onClick={() => void query.refetch()}>{t("refresh")}</Button></div>
      : !graph ? <p>{t("loading")}</p> : !active || !counts ? <p>{t("empty")}</p> : <div className="min-h-0 flex-1 space-y-3 overflow-y-auto pr-1">
        <div className="grid gap-2 md:grid-cols-2">
          <button className="min-w-0 rounded-xl border bg-card p-3 text-left" onClick={() => record(t("request"), graph.request)}>
            <span className="flex items-center gap-2 text-xs font-medium text-muted-foreground"><Flag className="size-4" />{t("request")}</span>
            <span className="mt-1.5 line-clamp-2 break-words text-sm">{graph.request.text || t("noSummary")}</span>
          </button>
          <button className="min-w-0 rounded-xl border bg-card p-3 text-left" onClick={() => record(t("runOutcome"), graph.conclusion)}>
            <span className="flex flex-wrap items-center gap-2 text-xs font-medium text-muted-foreground"><CheckCircle2 className="size-4" />{t("runOutcome")} · {stateLabel(active.status)}
              {active.terminalReason && active.terminalReason !== active.status && <span title={active.terminalReason}>{t.has(`terminalReasons.${active.terminalReason}`) ? t(`terminalReasons.${active.terminalReason}`) : active.terminalReason}</span>}
            </span>
            <span className="mt-1.5 line-clamp-2 break-words text-sm">{graph.conclusion.text || t(active.running ? "inProgress" : "noConclusion")}</span>
          </button>
        </div>
        <div className="flex flex-wrap gap-x-4 gap-y-1 px-1 text-xs text-muted-foreground" aria-label={t("wholeRun")}>
          <strong className="text-foreground">{t("wholeRun")} · {t("stepCount", { count: counts.steps })}</strong>
          {[["returned", counts.returned], ["failed", counts.failed], ["interrupted", counts.interrupted], ["pending", counts.pending], ["missing", counts.missing], ["overlap", counts.overlap]].map(([label, value]) => Number(value) > 0 && <span key={label}>{t(`totals.${label}`)} {value}</span>)}
          <span>{t("registrations", { count: graph.findingCount })}</span>
          {counts.steps > 0 && <span>{counts.measured ? t("timedSteps", { count: counts.measured, total: counts.steps }) : t("eventOrderOnly")}</span>}
        </div>
        <TimelineMap key={active.id} graph={graph} from={from} choose={go} goal={() => record(t("request"), graph.request)} outcome={() => record(t("runOutcome"), graph.conclusion)} receipts={finding => select({ kind: "finding", finding })} />
        <div className="grid items-start gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(15rem,29%)]">
          <section ref={listRef} className="min-w-0 space-y-2 rounded-xl border bg-muted/20 p-3" aria-busy={query.isPlaceholderData}>
            {selectedSection && <header data-slot="section-task" className="space-y-2 border-b pb-3">
              <div className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
                <span>{t("taskPurpose.label")}</span><span>·</span><span>{t("range", { first: selectedSection.first, last: selectedSection.last })}</span>
              </div>
              <h3 className="break-words text-base font-semibold">{timelineTaskTitle(t, selectedSection.purpose)}</h3>
              {(selectedSection.purpose?.subject || selectedSection.purpose?.topics?.[1]) && <div className="flex flex-wrap gap-x-4 gap-y-1 text-sm text-muted-foreground">
                {selectedSection.purpose.subject && <span>{t("taskPurpose.subject", { subject: selectedSection.purpose.subject })}</span>}
                {selectedSection.purpose.topics?.[1] && <span>{t("taskPurpose.also", { topic: t(`taskPurpose.topics.${selectedSection.purpose.topics[1]}`) })}</span>}
              </div>}
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                <span>{t(`taskPurpose.sources.${selectedSection.purpose?.source ?? "unknown"}`)}</span>
                {(selectedSection.purpose?.supportingSteps ?? 0) > 0 && <span>{t("taskPurpose.support", { count: selectedSection.purpose!.supportingSteps!, total: selectedSection.steps })}</span>}
                {selectedSection.purpose?.basis?.map(b => <button key={b.activityId} type="button"
                  className="rounded underline decoration-dotted underline-offset-2 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                  title={t("taskPurpose.basisRecord", { id: b.activityId })}
                  onClick={() => record(t("taskPurpose.basis"), { activityId: b.activityId, text: timelineTaskTitle(t, selectedSection.purpose) })}>
                  {b.step ? t("taskPurpose.basisStep", { number: b.step }) : t("taskPurpose.basisRecord", { id: b.activityId })}
                </button>)}
              </div>
              {selectedSection.firstFinding && <button type="button" className="flex max-w-full items-start gap-1.5 text-left text-xs text-amber-700 hover:underline dark:text-amber-300"
                onClick={() => select({ kind: "finding", finding: selectedSection.firstFinding! })}><Link2 className="size-3.5 shrink-0" />{t("taskPurpose.registered", { title: selectedSection.firstFinding.title })}</button>}
            </header>}
            <div className="flex flex-wrap items-center gap-2">
              <h4 className="mr-auto text-sm font-medium">{t("steps")}</h4>
              {selectedSection && <Select aria-label={t("chooseSection")} value={String(selectedSection.first)} onValueChange={v => go(Number(v))}>
                <SelectTrigger size="sm" className="w-full max-w-full sm:w-64" aria-label={t("chooseSection")}><SelectValue>{timelineTaskTitle(t, selectedSection.purpose)}</SelectValue></SelectTrigger>
                <SelectContent>{graph.sections.map(s => <SelectItem key={s.first} value={String(s.first)}>{timelineTaskTitle(t, s.purpose)} · {t("range", { first: s.first, last: s.last })}</SelectItem>)}</SelectContent>
              </Select>}
              <Button variant="outline" size="icon-sm" aria-label={t("previousSteps")} disabled={!graph.previous || query.isPlaceholderData} onClick={() => graph.previous && go(graph.previous)}><ArrowLeft className="size-4" /></Button>
              <Button variant="outline" size="icon-sm" aria-label={t("nextSteps")} disabled={!graph.next || query.isPlaceholderData} onClick={() => graph.next && go(graph.next)}><ArrowRight className="size-4" /></Button>
            </div>
            {graph.steps.length ? <TimelineSteps steps={graph.steps} select={select} /> : <p className="py-4 text-sm text-muted-foreground">{t("noSteps")}</p>}
          </section>
          <aside className="grid gap-3">
            <section className="space-y-2 rounded-xl border bg-card p-3">
              <h3 className="text-sm font-semibold">{t("coverage")}</h3>
              {graph.checks.length ? <ul className="max-h-64 space-y-2 overflow-y-auto">{graph.checks.map((item, i) =>
                <li key={`${item.activityId}:${i}`} className="border-l-2 border-border pl-2 text-xs">
                  <button className="text-left" onClick={() => record(t(`checkSources.${item.source}`), { text: item.text, activityId: item.activityId })}>{item.text}</button>
                  <div className="mt-0.5 text-muted-foreground">{t(`checkSources.${item.source}`)} · <button onClick={() => record(t("coverage"), { text: item.text, activityId: item.statusActivityId || item.activityId })}>{t(`checks.${item.state}`)}</button></div>
                </li>)}</ul> : <p className="text-xs text-muted-foreground">{t("noBaseline")}</p>}
              {graph.checksTruncated && <p className="text-xs text-muted-foreground">{t("checksPartial")}</p>}
            </section>
            <section className="space-y-2 rounded-xl border bg-card p-3">
              <h3 className="text-sm font-semibold">{t("evidenceBranch")} · {graph.findingCount}</h3>
              {graph.findings.map(f => <button key={f.receiptId} className="block w-full rounded-md border p-2 text-left text-xs" onClick={() => select({ kind: "finding", finding: f })}>
                <strong className="break-words font-medium">{f.title}</strong>
                <span className="mt-1 flex items-center gap-1 text-muted-foreground"><Link2 className="size-3" />{f.step ? t("stepNumber", { number: f.step }) : t("unlinked")} · {t(f.reviewed ? "states.reviewed" : "states.pending_review")}</span>
              </button>)}
              {!graph.findingCount && <p className="text-xs text-muted-foreground">{t("noReceipts")}</p>}
              {(graph.nextFinding || afterFinding) && <div className="flex gap-1"><Button size="sm" variant="outline" disabled={findingCursors.length === 1 || query.isPlaceholderData} onClick={() => selectFindingPage(false)}>{t("previous")}</Button><Button size="sm" variant="outline" disabled={!graph.nextFinding || query.isPlaceholderData} onClick={() => selectFindingPage(true)}>{t("next")}</Button></div>}
            </section>
          </aside>
        </div>
      </div>}
    {detail && <TimelineDetails key={active?.id + ":" + (detail.kind === "record" ? detail.id : detail.kind === "step" ? detail.step.activityId : detail.finding.receiptId)} selected={detail} user={user} conversation={conversationId} close={() => setSelection(null)} go={go} />}
  </div>
}
