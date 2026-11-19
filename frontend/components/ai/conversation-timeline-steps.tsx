"use client"

import * as React from "react"
import { useQuery } from "@tanstack/react-query"
import { useLocale, useTranslations } from "next-intl"
import { ChevronRight, Clock3, Link2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { VulnerabilityEvidencePanel } from "@/components/vulnerabilities/vulnerability-evidence-panel"
import { getConversationExplorationDetail } from "@/services/conversation-exploration.service"
import type { TimelineFinding, TimelineStep } from "@/types/conversation-exploration.types"
import { timelineCaption } from "./conversation-timeline-labels"

export type TraceSelection =
  | { kind: "step"; step: TimelineStep }
  | { kind: "finding"; finding: TimelineFinding }
  | { kind: "record"; id?: string; title: string; text: string }

function StepCard({ step, select }: { step: TimelineStep; select: (s: TraceSelection) => void }) {
  const t = useTranslations("aiChat.exploration")
  const title = timelineCaption(t, step)
  return <article className="rounded-lg border bg-card p-3">
    <button className="block w-full text-left focus-visible:ring-2 focus-visible:ring-ring" onClick={() => select({ kind: "step", step })}>
      <span className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span className="font-mono">#{step.number}</span><span>{step.tool}</span>
        {step.round && <span>{t("roundBatch", { round: step.round, batch: step.batch ?? 0 })}</span>}
        {step.overlap && <span className="inline-flex items-center gap-1 text-indigo-600 dark:text-indigo-300"><Clock3 className="size-3" />{t("overlap")}</span>}
        <span className={`ml-auto ${step.state === "failed" || step.state === "interrupted" ? "text-amber-700 dark:text-amber-300" : ""}`}>{t(`states.${step.state}`)}</span>
      </span>
      <strong className="mt-1.5 block break-words text-sm font-medium">{title}</strong>
      <span className="mt-1 block break-words text-xs text-muted-foreground">{[step.outcomeCount !== undefined ? t("resultCount", { count: step.outcomeCount }) : "", step.outcome].filter(Boolean).join(" · ") || (step.resultId ? t("recordAvailable") : t("noResult"))}</span>
    </button>
    {step.finding && <Button size="sm" variant="link" className="mt-1 h-auto max-w-full justify-start p-0 text-amber-700 dark:text-amber-300" onClick={() => select({ kind: "finding", finding: step.finding! })}><Link2 className="size-3" /><span className="truncate">{step.finding.title}</span></Button>}
  </article>
}

export function groupSteps(steps: TimelineStep[]) {
  const groups: TimelineStep[][] = []
  for (const step of steps) {
    const group = groups[groups.length - 1]
    const last = group?.[group.length - 1]
    if (last && !last.finding && !step.finding && !last.overlap && !step.overlap && last.state === "succeeded" && step.state === "succeeded" && last.tool === step.tool && last.action === step.action && last.subject === step.subject && last.batch === step.batch) {
      group.push(step)
    } else groups.push([step])
  }
  return groups
}

function StepGroup({ group, select }: { group: TimelineStep[]; select: (s: TraceSelection) => void }) {
  const t = useTranslations("aiChat.exploration")
  const first = group[0]
  const title = timelineCaption(t, first)
  if (group.length === 1) return <StepCard step={first} select={select} />
  return <details className="group rounded-lg border bg-card">
    <summary className="flex cursor-pointer list-none items-center gap-2 p-3 text-sm">
      <ChevronRight className="size-4 shrink-0 transition-transform group-open:rotate-90" />
      <span className="min-w-0 flex-1"><span className="block text-xs text-muted-foreground">#{first.number}–#{group[group.length - 1].number} · {first.tool}</span><strong className="mt-1 block font-medium">{title}</strong></span>
      <span className="text-xs text-muted-foreground">{t("similar", { count: group.length })}</span>
    </summary>
    <div className="grid gap-2 px-3 pb-3">{group.map(step => <StepCard key={step.activityId} step={step} select={select} />)}</div>
  </details>
}
export function TimelineSteps({ steps, select }: { steps: TimelineStep[]; select: (s: TraceSelection) => void }) {
  return <div className="grid gap-2">{groupSteps(steps).map(group => <StepGroup key={group[0].activityId} group={group} select={select} />)}</div>
}

function SavedRecord({ user, conversation, id, label }: { user: number; conversation: string; id: string; label: string }) {
  const t = useTranslations("aiChat.exploration")
  const [open, setOpen] = React.useState(false)
  const query = useQuery({ queryKey: ["conversation-source", user, conversation, id], enabled: open, gcTime: 0, retry: false, queryFn: ({ signal }) => getConversationExplorationDetail(conversation, id, signal) })
  return <details onToggle={event => setOpen(event.currentTarget.open)} className="rounded-md border p-2 text-sm">
    <summary className="cursor-pointer font-medium">{label} <span className="font-mono text-xs text-muted-foreground">#{id}</span></summary>
    {open && (query.isError ? <Button size="sm" variant="link" onClick={() => void query.refetch()}>{t("retryDetail")}</Button> : query.isPending ? <p>{t("loading")}</p> : <pre className="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-all rounded bg-muted p-2 font-mono text-xs">{query.data || t("noSummary")}</pre>)}
  </details>
}
export function TimelineDetails({ selected, user, conversation, close, go }: { selected: TraceSelection; user: number; conversation: string; close: () => void; go: (step: number) => void }) {
  const t = useTranslations("aiChat.exploration")
  const locale = useLocale()
  const step = selected.kind === "step" ? selected.step : null
  const finding = selected.kind === "finding" ? selected.finding : step?.finding
  const sources = new Map(finding?.evidenceSources?.map(source => [source.activityId, source] as const))
  const format = (value: string) => new Date(value).toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit", second: "2-digit", fractionalSecondDigits: 3 } as Intl.DateTimeFormatOptions)
  return <Sheet open onOpenChange={open => { if (!open) close() }}><SheetContent side="right" className="flex w-full flex-col sm:max-w-xl">
    <SheetHeader><SheetTitle>{step ? t("stepNumber", { number: step.number }) : finding ? t("evidenceBranch") : t("sourceRecord")}</SheetTitle>
      <SheetDescription className="break-words">{selected.kind === "record" ? selected.title : step ? timelineCaption(t, step) : finding?.title}</SheetDescription></SheetHeader>
    <div className="min-h-0 flex-1 space-y-3 overflow-auto text-sm">
      {step && <>
        <p>{step.tool} · {t(`states.${step.state}`)}{step.round ? ` · ${t("roundBatch", { round: step.round, batch: step.batch ?? 0 })}` : ""}</p>
        <p className="text-xs text-muted-foreground">{t("recordedAt")} {format(step.recordedAt)}</p>
        {step.startedAt && <p>{t("actualStart")} {format(step.startedAt)}{step.finishedAt && <> · {t("actualEnd")} {format(step.finishedAt)}</>}</p>}
        {step.overlapStep && <Button size="sm" variant="outline" onClick={() => go(step.overlapStep!)}>{t("overlapWith", { number: step.overlapStep })}</Button>}
        <SavedRecord user={user} conversation={conversation} id={step.activityId} label={t("callRecord")} />
        {step.resultId && <SavedRecord user={user} conversation={conversation} id={step.resultId} label={t("returnRecord")} />}
      </>}
      {selected.kind === "record" && <><p className="whitespace-pre-wrap break-words">{selected.text}</p>{selected.id && <SavedRecord user={user} conversation={conversation} id={selected.id} label={t("sourceRecord")} />}</>}
      {finding && <>
        <p>{t("vulnerabilityId", { id: finding.vulnerabilityId })} · {t("currentReview")} · {t(finding.reviewed ? "states.reviewed" : "states.pending_review")}</p>
        {finding.step ? <Button size="sm" variant="outline" onClick={() => go(finding.step!)}>{t("goToStep", { number: finding.step })}</Button> : <p>{t("unlinked")}</p>}
        {finding.sourceActivityId && selected.kind === "finding" && <SavedRecord user={user} conversation={conversation} id={finding.sourceActivityId} label={t("callRecord")} />}
        {Array.from(sources.values(), source => <div key={source.activityId} className="space-y-1">
          <Button size="sm" variant="link" onClick={() => go(source.step)}>{t("sharedEvidence", { number: source.step })}</Button>
          <SavedRecord user={user} conversation={conversation} id={source.activityId} label={t("returnRecord")} />
        </div>)}
        <h4 className="font-medium">{t("evidence")}</h4><VulnerabilityEvidencePanel vulnerabilityId={finding.vulnerabilityId} />
      </>}
    </div>
  </SheetContent></Sheet>
}
