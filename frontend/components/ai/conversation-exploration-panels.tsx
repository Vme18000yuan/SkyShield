"use client"

import { useTranslations } from "next-intl"
import { ArrowLeft, ArrowRight, semanticIcons } from "@/components/icons"
import { textRole } from "@/lib/typography"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import type { ConversationTimeline, TimelineText } from "@/types/conversation-exploration.types"
import { timelineTaskTitle } from "./conversation-timeline-labels"
import { TimelineSteps, type TraceSelection } from "./conversation-timeline-steps"

export type TimelinePanel = "steps" | "checks" | "findings"

type Props = {
  panel: TimelinePanel
  change: (panel: TimelinePanel) => void
  close: () => void
  graph: ConversationTimeline
  from: number
  busy: boolean
  go: (step: number) => void
  select: (item: TraceSelection) => void
  record: (title: string, item?: TimelineText) => void
  selectFindingPage: (forward: boolean) => void
  findingCursors: string[]
}

export function TimelinePanels({ panel, change, close, graph, from, busy, go, select, record, selectFindingPage, findingCursors }: Props) {
  const t = useTranslations("aiChat.exploration")
  const selectedSection = graph.sections.find(section => section.first <= from && from <= section.last)
  const afterFinding = findingCursors[findingCursors.length - 1]
  return <Sheet open onOpenChange={open => { if (!open) close() }}>
    <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-xl">
      <SheetHeader className="border-b pr-12"><SheetTitle>{t(`panels.${panel}`)}</SheetTitle><SheetDescription>{t("panels.description")}</SheetDescription></SheetHeader>
      <Tabs value={panel} onValueChange={value => change(value as TimelinePanel)} className="flex min-h-0 flex-1 flex-col"><div className="px-4 pt-3"><TabsList variant="content"><TabsTrigger value="steps">{t("steps")}</TabsTrigger><TabsTrigger value="checks">{t("panels.checks")}</TabsTrigger><TabsTrigger value="findings">{t("evidenceBranch")}</TabsTrigger></TabsList></div>
      <div className="min-h-0 flex-1 overflow-auto p-4">
          <TabsContent value="steps" className="min-w-0 space-y-3" aria-busy={busy}>
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
                {selectedSection.purpose?.basis?.map(b => <Button key={b.activityId} size="content" variant="link" layout="textCellLink"
                  title={t("taskPurpose.basisRecord", { id: b.activityId })}
                  onClick={() => record(t("taskPurpose.basis"), { activityId: b.activityId, text: timelineTaskTitle(t, selectedSection.purpose) })}>
                  {b.step ? t("taskPurpose.basisStep", { number: b.step }) : t("taskPurpose.basisRecord", { id: b.activityId })}
                </Button>)}
              </div>
              {selectedSection.firstFinding && <Button size="content" variant="link" layout="textCellLink"
                onClick={() => select({ kind: "finding", finding: selectedSection.firstFinding! })}><semanticIcons.action.view className="size-3.5 shrink-0" />{t("taskPurpose.registered", { title: selectedSection.firstFinding.title })}</Button>}
            </header>}
            <div className="flex flex-wrap items-center gap-2">
              <h4 className="mr-auto text-sm font-medium">{t("steps")}</h4>
              {selectedSection && <Select aria-label={t("chooseSection")} value={String(selectedSection.first)} onValueChange={v => go(Number(v))}>
                <SelectTrigger size="sm" className="w-full max-w-full sm:w-64" aria-label={t("chooseSection")}><SelectValue>{timelineTaskTitle(t, selectedSection.purpose)}</SelectValue></SelectTrigger>
                <SelectContent>{graph.sections.map(s => <SelectItem key={s.first} value={String(s.first)}>{timelineTaskTitle(t, s.purpose)} · {t("range", { first: s.first, last: s.last })}</SelectItem>)}</SelectContent>
              </Select>}
              <Button variant="outline" size="icon-sm" aria-label={t("previousSteps")} disabled={!graph.previous || busy} onClick={() => graph.previous && go(graph.previous)}><ArrowLeft className="size-4" /></Button>
              <Button variant="outline" size="icon-sm" aria-label={t("nextSteps")} disabled={!graph.next || busy} onClick={() => graph.next && go(graph.next)}><ArrowRight className="size-4" /></Button>
            </div>
            {busy ? <Skeleton className="h-48 w-full" /> : graph.steps.length ? <TimelineSteps steps={graph.steps} select={select} /> : <p className="py-4 text-sm text-muted-foreground">{t("noSteps")}</p>}
            <details className="rounded-lg border p-3">
              <summary className={textRole.compactPrimary}>{t("wholeRun")} · {t("stepCount", { count: graph.counts.steps })}</summary>
              <dl className="mt-3 grid grid-cols-2 gap-2">{(["returned", "failed", "interrupted", "pending", "missing", "overlap"] as const).map(key => <div key={key} className="flex justify-between gap-2"><dt className={textRole.compactCaption}>{t(`totals.${key}`)}</dt><dd className={textRole.compactPrimary}>{graph.counts[key]}</dd></div>)}</dl>
              <p className={textRole.compactCaption}>{graph.counts.measured ? t("timedSteps", { count: graph.counts.measured, total: graph.counts.steps }) : t("eventOrderOnly")}</p>
            </details>
          </TabsContent>
          <TabsContent value="checks" className="space-y-3">
              <h3 className={textRole.sectionTitle}>{t("coverage")}</h3>
              {graph.checks.length ? <ul className="space-y-3">{graph.checks.map((item, i) =>
                <li key={`${item.activityId}:${i}`} className="border-l-2 border-border pl-2 text-xs">
                  <Button size="content" variant="link" layout="textCellLink" onClick={() => record(t(`checkSources.${item.source}`), { text: item.text, activityId: item.activityId })}>{item.text}</Button>
                  <div className="mt-0.5 text-muted-foreground">{t(`checkSources.${item.source}`)} · <Button size="content" variant="link" layout="textCellLink" onClick={() => record(t("coverage"), { text: item.text, activityId: item.statusActivityId || item.activityId })}>{t(`checks.${item.state}`)}</Button></div>
                </li>)}</ul> : <p className="text-xs text-muted-foreground">{t("noBaseline")}</p>}
              {graph.checksTruncated && <p className="text-xs text-muted-foreground">{t("checksPartial")}</p>}
            </TabsContent>
          <TabsContent value="findings" className="space-y-3">
              <h3 className={textRole.sectionTitle}>{t("evidenceBranch")} · {graph.findingCount}</h3>
              {graph.findings.map(f => <Button key={f.receiptId} size="action-card" variant="outline" className="block text-left" onClick={() => select({ kind: "finding", finding: f })}>
                <strong className="break-words font-medium">{f.title}</strong>
                <span className="mt-1 flex items-center gap-1 text-muted-foreground"><semanticIcons.action.view className="size-3" />{f.step ? t("stepNumber", { number: f.step }) : t("unlinked")} · {t(f.reviewed ? "states.reviewed" : "states.pending_review")}</span>
              </Button>)}
              {!graph.findingCount && <p className="text-xs text-muted-foreground">{t("noReceipts")}</p>}
              {(graph.nextFinding || afterFinding) && <div className="flex gap-1"><Button size="sm" variant="outline" disabled={findingCursors.length === 1 || busy} onClick={() => selectFindingPage(false)}>{t("previous")}</Button><Button size="sm" variant="outline" disabled={!graph.nextFinding || busy} onClick={() => selectFindingPage(true)}>{t("next")}</Button></div>}
            </TabsContent>
      </div>
      </Tabs>
    </SheetContent>
  </Sheet>
}
