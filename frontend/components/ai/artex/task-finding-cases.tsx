"use client"

import * as React from "react"
import Link from "next/link"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { Markdown } from "@/components/settings/ai/artex-markdown"
import { FindingCaseSuggestions } from "@/components/ai/artex/finding-case-suggestions"
import { FindingCaseReviews, type FindingReviewSeed } from "@/components/ai/artex/finding-case-reviews"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { useAuth } from "@/hooks/use-auth"
import { api } from "@/lib/api-client"
import type { AITaskFinding, AITaskFindingPage } from "@/types/ai-task-finding.types"
import type { AITaskFindingCase, AITaskFindingCasePage } from "@/types/ai-task-finding-case.types"
import type { FindingCaseMergeSeed } from "@/components/ai/artex/finding-duplicates"

const label = (item: Pick<AITaskFinding, "name" | "vulnclass">) => item.name || item.vulnclass || "—"

export function TaskFindingCases({ taskId, severity = "", query = "", mergeSeed = null, onMergeSeedConsumed }: { taskId: string; severity?: string; query?: string; mergeSeed?: FindingCaseMergeSeed | null; onMergeSeedConsumed?: () => void }) {
  const user = useAuth().data?.user?.id
  return <TaskFindingCasesInner key={`${user ?? "anonymous"}:${taskId}`} taskId={taskId} severity={severity} query={query} mergeSeed={mergeSeed} onMergeSeedConsumed={onMergeSeedConsumed} />
}

function TaskFindingCasesInner({ taskId, severity, query: filterQuery, mergeSeed, onMergeSeedConsumed }: { taskId: string; severity: string; query: string; mergeSeed: FindingCaseMergeSeed | null; onMergeSeedConsumed?: () => void }) {
  const t = useTranslations("aiFindingCases")
  const findingsT = useTranslations("aiFindings")
  const user = useAuth().data?.user?.id
  const cache = useQueryClient()
  const base = `/aiTasks/${encodeURIComponent(taskId)}/findingCases`
  const key = ["task-finding-cases", user, taskId]
  const [page, setPage] = React.useState(0)
  const [selectedId, setSelectedId] = React.useState<string | null>(null)
  const [mergeOpen, setMergeOpen] = React.useState(false)
  const [mergeTitle, setMergeTitle] = React.useState("")
  const [mergeReason, setMergeReason] = React.useState("")
  const [search, setSearch] = React.useState("")
  const [query, setQuery] = React.useState("")
  const [findingPage, setFindingPage] = React.useState(0)
  const [selected, setSelected] = React.useState<Record<string, string>>({})
  const [detachId, setDetachId] = React.useState<string | null>(null)
  const [detachReason, setDetachReason] = React.useState("")
  const [memberId, setMemberId] = React.useState<string | null>(null)
  const [reportTitle, setReportTitle] = React.useState("")
  const [reportSeverity, setReportSeverity] = React.useState("high")
  const [reportReason, setReportReason] = React.useState("")
  const [report, setReport] = React.useState("")
  const [reviewSeed, setReviewSeed] = React.useState<FindingReviewSeed | null>(null)
  const consumeReviewSeed = React.useCallback(() => setReviewSeed(null), [])

  React.useEffect(() => {
    const timer = window.setTimeout(() => { setQuery(search.trim()); setFindingPage(0) }, 300)
    return () => window.clearTimeout(timer)
  }, [search])
  React.useEffect(() => setPage(0), [severity, filterQuery])
  React.useEffect(() => {
    if (!mergeSeed) return
    setSelected(mergeSeed.findings)
    setMergeTitle(mergeSeed.title)
    setMergeReason("")
    setMergeOpen(true)
    onMergeSeedConsumed?.()
  }, [mergeSeed, onMergeSeedConsumed])
  const list = useQuery({ queryKey: [...key, severity, filterQuery, page], enabled: !!user, gcTime: 0, queryFn: async () => (await api.get<AITaskFindingCasePage>(base, { params: { page, size: 25, severity: severity || undefined, q: filterQuery || undefined } })).data })
  const detail = useQuery({ queryKey: [...key, "detail", selectedId], enabled: !!user && !!selectedId, gcTime: 0, queryFn: async () => (await api.get<AITaskFindingCase>(`${base}/${encodeURIComponent(selectedId!)}`)).data })
  const selectedMember = detail.data?.members?.find(item => item.id === memberId)
  const memberDetail = useQuery({ queryKey: ["task-findings", user, taskId, "detail", memberId], enabled: !!user && !!selectedId && !!selectedMember, gcTime: 0,
    queryFn: async () => (await api.get<AITaskFinding>(`/aiTasks/${encodeURIComponent(taskId)}/findings/${encodeURIComponent(memberId!)}`)).data })
  const choices = useQuery({ queryKey: ["task-finding-case-choices", user, taskId, query, findingPage], enabled: !!user && mergeOpen, gcTime: 0,
    queryFn: async () => (await api.get<AITaskFindingPage>(`/aiTasks/${encodeURIComponent(taskId)}/findings`, { params: { own: 1, q: query || undefined, page: findingPage, size: 25 } })).data })

  React.useEffect(() => {
    if (!detail.data) return
    setReportTitle(detail.data.title)
    setReportSeverity(detail.data.severity || "high")
    setReportReason(detail.data.severity_reason || "")
    setReport(detail.data.report || "")
    setDetachId(null)
    setDetachReason("")
  }, [detail.data])
  React.useEffect(() => setMemberId(null), [selectedId])

  const refresh = async () => {
    await Promise.all([
      cache.invalidateQueries({ queryKey: key }),
      cache.invalidateQueries({ queryKey: ["task-findings", user, taskId] }),
      cache.invalidateQueries({ queryKey: ["task-finding-case-choices", user, taskId] }),
    ])
  }
  const merge = useMutation({
    mutationFn: () => api.post(base, { finding_ids: Object.keys(selected), title: mergeTitle, reason: mergeReason }),
    onSuccess: async () => { setMergeOpen(false); setSelected({}); setMergeTitle(""); setMergeReason(""); setSearch(""); setFindingPage(0); await refresh(); toast.success(t("merged")) },
    onError: () => toast.error(t("mergeError")),
  })
  const detach = useMutation({
    mutationFn: () => api.post(`${base}/${encodeURIComponent(selectedId!)}/members/${encodeURIComponent(detachId!)}/detach`, { reason: detachReason }),
    onSuccess: async () => { setSelectedId(null); setDetachId(null); setDetachReason(""); await refresh(); toast.success(t("detached")) },
    onError: () => toast.error(t("changeError")),
  })
  const saveReport = useMutation({
    mutationFn: () => api.put(`${base}/${encodeURIComponent(selectedId!)}/report`, { version: detail.data!.version, title: reportTitle, severity: reportSeverity, severity_reason: reportReason, report }),
    onSuccess: async () => { await refresh(); toast.success(t("reportSaved")) },
    onError: () => toast.error(t("reportError")),
  })
  const closeMerge = (open: boolean) => {
    setMergeOpen(open)
    if (!open) { setSelected({}); setMergeTitle(""); setMergeReason(""); setSearch(""); setFindingPage(0) }
  }

  return <section className="space-y-3 rounded border p-3">
    <div className="flex flex-wrap items-center justify-between gap-2"><div><h3 className="font-medium">{t("title")}</h3><p className="text-xs text-muted-foreground">{t("description")}</p></div><Button size="sm" variant="outline" onClick={() => setMergeOpen(true)}>{t("merge")}</Button></div>
    <FindingCaseSuggestions taskId={taskId}/>
    <FindingCaseReviews taskId={taskId} seed={reviewSeed} onSeedConsumed={consumeReviewSeed}/>
    {list.isError ? <p role="alert" className="text-sm text-destructive">{t("loadError")}</p> : <div className="divide-y rounded border">
      {list.data?.items?.map(item => <article key={item.id} className="flex flex-wrap items-center gap-2 p-3 text-sm"><div className="min-w-0 flex-1"><strong className="break-words">{item.title}</strong><p className="break-words text-xs text-muted-foreground">{item.reason}</p>{item.inherited && <span className="text-xs text-muted-foreground">{findingsT("inherited", { name: item.task_name })}</span>}</div><Badge variant="outline">{t("memberCount", { count: item.member_count })}</Badge>{item.has_report && item.report_version < item.version && <span className="text-xs text-muted-foreground">{t("reportOutdated")}</span>}<Button size="sm" variant="outline" onClick={() => setSelectedId(item.id)}>{t("details")}</Button></article>)}
      {!list.isLoading && !list.data?.items?.length && <p className="p-3 text-sm text-muted-foreground">{t("empty")}</p>}
    </div>}
    <div className="flex items-center justify-end gap-2 text-sm"><span>{findingsT("total", { count: list.data?.total ?? 0 })} · {findingsT("page", { page: page + 1 })}</span><Button size="sm" variant="outline" disabled={page === 0 || list.isFetching} onClick={() => setPage(value => value - 1)}>{findingsT("previous")}</Button><Button size="sm" variant="outline" disabled={list.isFetching || !list.data || (page + 1) * list.data.size >= list.data.total} onClick={() => setPage(value => value + 1)}>{findingsT("next")}</Button></div>

    <Dialog open={mergeOpen} onOpenChange={closeMerge}><DialogContent className="max-h-[90vh] overflow-auto sm:max-w-2xl"><DialogHeader><DialogTitle>{t("merge")}</DialogTitle><DialogDescription>{t("mergeDescription")}</DialogDescription></DialogHeader>
      <label className="space-y-1 text-sm"><span>{t("groupName")}</span><Input value={mergeTitle} onChange={event => setMergeTitle(event.target.value)} maxLength={300} /></label>
      <label className="space-y-1 text-sm"><span>{t("mergeReason")}</span><Textarea value={mergeReason} onChange={event => setMergeReason(event.target.value)} maxLength={2000} /></label>
      <Input aria-label={t("searchFindings")} placeholder={t("searchFindings")} value={search} onChange={event => setSearch(event.target.value)} />
      <p className="text-xs text-muted-foreground">{t("selectedCount", { count: Object.keys(selected).length })}</p>
      {Object.keys(selected).length > 0 && <div className="flex flex-wrap gap-1" aria-label={t("chosen")}>{Object.entries(selected).map(([id, name]) => <button type="button" key={id} className="max-w-48 truncate rounded bg-muted px-2 py-1 text-xs" aria-label={t("removeSelected", { name })} onClick={() => setSelected(current => { const next = { ...current }; delete next[id]; return next })}>{name} ×</button>)}</div>}
      <div className="max-h-60 divide-y overflow-auto rounded border">{choices.isError ? <p role="alert" className="p-3 text-sm text-destructive">{t("loadError")}</p> : choices.data?.items?.map(item => <label key={item.id} className="flex cursor-pointer items-start gap-2 p-2 text-sm"><input type="checkbox" checked={item.id in selected} onChange={event => setSelected(current => { const next = { ...current }; if (event.target.checked) next[item.id] = label(item); else delete next[item.id]; return next })} /><span className="min-w-0 break-words">{label(item)} <span className="text-xs text-muted-foreground">{item.summary}</span></span></label>)}</div>
      <div className="flex items-center justify-between gap-2 text-sm"><span>{findingsT("total", { count: choices.data?.total ?? 0 })}</span><div className="flex gap-2"><Button size="sm" variant="outline" disabled={findingPage === 0 || choices.isFetching} onClick={() => setFindingPage(value => value - 1)}>{findingsT("previous")}</Button><Button size="sm" variant="outline" disabled={choices.isFetching || !choices.data || (findingPage + 1) * choices.data.size >= choices.data.total} onClick={() => setFindingPage(value => value + 1)}>{findingsT("next")}</Button></div></div>
      <div className="flex justify-end gap-2"><Button variant="outline" onClick={() => closeMerge(false)}>{t("cancel")}</Button><Button disabled={merge.isPending || Object.keys(selected).length < 2 || Object.keys(selected).length > 100 || !mergeTitle.trim() || !mergeReason.trim()} onClick={() => merge.mutate()}>{t("confirmMerge")}</Button></div>
    </DialogContent></Dialog>

    <Dialog open={!!selectedId} onOpenChange={open => { if (!open) setSelectedId(null) }}><DialogContent className="max-h-[90vh] overflow-auto sm:max-w-4xl"><DialogHeader><DialogTitle>{detail.data?.title || t("details")}</DialogTitle><DialogDescription>{detail.data?.reason || t("detailsDescription")}</DialogDescription></DialogHeader>
      {detail.isError ? <p role="alert" className="text-sm text-destructive">{t("loadError")}</p> : detail.data ? <div className="space-y-4">
        {detail.data.inherited && <p className="text-sm text-muted-foreground">{t("readOnly")} <Link className="text-primary underline" href={`/ai/tasks/${encodeURIComponent(detail.data.task_id)}/`}>{detail.data.task_name}</Link></p>}
        {!detail.data.inherited && !!detail.data.members?.length && <div className="flex justify-end"><Button size="sm" variant="outline" onClick={() => { setReviewSeed({ findings: Object.fromEntries(detail.data!.members!.map(item => [item.id, label(item)])), caseId: detail.data!.id }); setSelectedId(null) }}>{t("reviewGroup")}</Button></div>}
        <section><h4 className="text-sm font-medium">{t("members")}</h4><div className="mt-2 divide-y rounded border">{detail.data.members?.map(item => <div key={item.id} className="flex flex-wrap items-center gap-2 p-2 text-sm"><div className="min-w-0 flex-1"><strong className="break-words">{label(item)}</strong><p className="break-words text-xs text-muted-foreground">{item.summary}</p></div><Badge variant="outline">{item.severity || "—"}</Badge><Button size="sm" variant="outline" onClick={() => setMemberId(current => current === item.id ? null : item.id)}>{memberId === item.id ? t("hideEvidence") : t("evidence")}</Button>{!detail.data!.inherited && <Button size="sm" variant="outline" onClick={() => { setDetachId(item.id); setDetachReason("") }}>{t("detach")}</Button>}</div>)}</div>
          {selectedMember && <div className="mt-2 rounded border p-3"><h5 className="text-sm font-medium">{label(selectedMember)} · {findingsT("evidence")}</h5>{memberDetail.isError ? <p role="alert" className="text-sm text-destructive">{findingsT("loadError")}</p> : memberDetail.data ? <><pre className="mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded bg-muted p-3 text-xs">{memberDetail.data.evidence || findingsT("noEvidence")}</pre>{memberDetail.data.report && <div className="mt-2"><Markdown text={memberDetail.data.report} /></div>}</> : <p className="text-sm text-muted-foreground">{findingsT("loading")}</p>}</div>}
        </section>
        {detachId && !detail.data.inherited && <div className="space-y-2 rounded border p-3" role="group" aria-label={t("detachReason")}><p className="text-sm">{t("detachDescription")}</p><Textarea aria-label={t("detachReason")} placeholder={t("detachReason")} value={detachReason} onChange={event => setDetachReason(event.target.value)} maxLength={2000} /><div className="flex justify-end gap-2"><Button size="sm" variant="outline" onClick={() => setDetachId(null)}>{t("cancel")}</Button><Button size="sm" disabled={detach.isPending || !detachReason.trim()} onClick={() => detach.mutate()}>{t("confirmDetach")}</Button></div></div>}
        <section><h4 className="text-sm font-medium">{t("report")}</h4>{detail.data.report_version < detail.data.version && detail.data.has_report && <p className="text-xs text-muted-foreground">{t("reportOutdated")}</p>}{detail.data.report ? <div className="mt-2"><Markdown text={detail.data.report} /></div> : <p className="mt-1 text-sm text-muted-foreground">{t("noReport")}</p>}</section>
        {!detail.data.inherited && <details className="rounded border p-3"><summary className="cursor-pointer text-sm font-medium">{t("history")}</summary>{detail.data.events?.length ? <ul className="mt-2 divide-y">{detail.data.events.map(event => <li key={event.id} className="py-2 text-sm"><span className="font-medium">{["merge", "detach", "member_deleted", "report_updated"].includes(event.action) ? t(`event.${event.action}`) : event.action}</span><span className="ml-2 text-xs text-muted-foreground">{new Date(event.created_at).toLocaleString()}</span><p className="break-words text-xs text-muted-foreground">{event.reason}</p></li>)}</ul> : <p className="mt-2 text-sm text-muted-foreground">{t("noHistory")}</p>}{detail.data.events_truncated && <p className="text-xs text-muted-foreground">{t("historyTruncated")}</p>}</details>}
        {!detail.data.inherited && <details className="rounded border p-3"><summary className="cursor-pointer text-sm font-medium">{t("editReport")}</summary><div className="mt-3 space-y-3"><label className="block space-y-1 text-sm"><span>{t("groupName")}</span><Input value={reportTitle} onChange={event => setReportTitle(event.target.value)} maxLength={300} /></label><label className="block space-y-1 text-sm"><span>{t("severity")}</span><select className="block h-9 rounded border bg-background px-2" value={reportSeverity} onChange={event => setReportSeverity(event.target.value)}>{["critical", "high", "medium", "low"].map(value => <option key={value} value={value}>{findingsT(`severityLabel.${value}`)}</option>)}</select></label><label className="block space-y-1 text-sm"><span>{t("severityReason")}</span><Textarea value={reportReason} onChange={event => setReportReason(event.target.value)} maxLength={2000} /></label><label className="block space-y-1 text-sm"><span>{t("report")}</span><Textarea className="min-h-36" value={report} onChange={event => setReport(event.target.value)} maxLength={200000} /></label><Button size="sm" disabled={saveReport.isPending || !reportTitle.trim() || !reportReason.trim() || !report.trim()} onClick={() => saveReport.mutate()}>{t("saveReport")}</Button></div></details>}
      </div> : <p>{findingsT("loading")}</p>}
    </DialogContent></Dialog>
  </section>
}
