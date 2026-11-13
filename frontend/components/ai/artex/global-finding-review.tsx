"use client"

import * as React from "react"
import Link from "next/link"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useAuth } from "@/hooks/use-auth"
import { api } from "@/lib/api-client"
import { loadReviewFindingGroup } from "@/components/ai/artex/finding-review-selection"
import type { AIFindingReviewCandidate, AIFindingReviewCandidatePage } from "@/types/ai-finding-review-candidate.types"
import type { AIFindingReview } from "@/types/ai-finding-review.types"

const nameOf = (item: AIFindingReviewCandidate) => item.name || item.vulnclass || "—"

export function GlobalFindingReview() {
  const user = useAuth().data?.user?.id
  return <GlobalFindingReviewInner key={user ?? "anonymous"} />
}

function GlobalFindingReviewInner() {
  const t = useTranslations("aiGlobalFindingReview")
  const findingsT = useTranslations("aiFindings")
  const user = useAuth().data?.user?.id
  const cache = useQueryClient()
  const [draft, setDraft] = React.useState("")
  const [query, setQuery] = React.useState("")
  const [severity, setSeverity] = React.useState("")
  const [page, setPage] = React.useState(0)
  const [jump, setJump] = React.useState("")
  const [selected, setSelected] = React.useState<Record<string, AIFindingReviewCandidate>>({})
  const [selecting, setSelecting] = React.useState(false)
  const selectionBusy = React.useRef(false)
  const selectionEpoch = React.useRef(0)
  const [submitted, setSubmitted] = React.useState<Array<{ review: AIFindingReview; taskName: string }>>([])
  React.useEffect(() => { const timer = window.setTimeout(() => { setQuery(draft.trim()); setPage(0) }, 300); return () => window.clearTimeout(timer) }, [draft])
  React.useEffect(() => () => { selectionEpoch.current++ }, [])
  const list = useQuery({ queryKey: ["finding-review-candidates", user, query, severity, page], enabled: !!user, gcTime: 0,
    queryFn: async () => (await api.get<AIFindingReviewCandidatePage>("/aiFindingReviewCandidates", { params: { q: query || undefined, severity: severity || undefined, page, size: 25 } })).data })
  const items = Object.values(selected)
  const taskCount = new Set(items.map(item => item.task_id)).size
  const create = useMutation({
    mutationFn: async () => {
      const tasks = new Map<string, string[]>()
      for (const item of items) tasks.set(item.task_id, [...(tasks.get(item.task_id) || []), item.id])
      return (await api.post<{ reviews: AIFindingReview[] }>("/aiFindingCaseReviews", { batches: Array.from(tasks, ([task_id, finding_ids]) => ({ task_id, finding_ids })) })).data
    },
    onSuccess: async result => {
      const names = new Map(items.map(item => [item.task_id, item.task_name]))
      setSubmitted(result.reviews.map(review => ({ review, taskName: names.get(review.task_id) || t("unknownTask") })))
      setSelected({})
      await cache.invalidateQueries({ queryKey: ["finding-case-reviews", user] })
      toast.success(t("started", { count: result.reviews.length }))
    },
    onError: () => toast.error(t("startError")),
  })
  const pageCount = Math.max(1, Math.ceil((list.data?.total ?? 0) / (list.data?.size || 25)))
  const applyJump = () => { const target = Number(jump); if (Number.isInteger(target) && target >= 1 && target <= pageCount) { setPage(target - 1); setJump("") } }
  const removeSelected = (id: string) => setSelected(current => {
    const item = current[id]
    return Object.fromEntries(Object.entries(current).filter(([key, value]) => item?.case_id ? value.task_id !== item.task_id || value.case_id !== item.case_id : key !== id))
  })
  const selectFinding = async (item: AIFindingReviewCandidate, checked: boolean) => {
    if (selectionBusy.current) return
    if (!checked) { removeSelected(item.id); return }
    const add = (additions: Record<string, AIFindingReviewCandidate>) => {
      const next = { ...selected, ...additions }
      if (Object.keys(next).length > 200 || new Set(Object.values(next).map(value => value.task_id)).size > 20) { toast.error(t("selectionLimit")); return false }
      setSelected(current => ({ ...current, ...additions })); return true
    }
    if (!item.case_id) { add({ [item.id]: item }); return }
    selectionBusy.current = true; setSelecting(true)
    const epoch = selectionEpoch.current
    try {
      const group = await loadReviewFindingGroup(item.task_id, item.case_id)
      if (epoch !== selectionEpoch.current) return
      if (!group.members!.some(member => member.id === item.id)) throw new Error("finding no longer belongs to group")
      const additions = Object.fromEntries(group.members!.map(member => [member.id, { ...item, ...member, case_id: group.id, in_group: true }]))
      if (add(additions)) toast.success(t("groupExpanded", { count: group.members!.length }))
    } catch { if (epoch === selectionEpoch.current) toast.error(t("groupLoadError")) }
    finally { if (epoch === selectionEpoch.current) { selectionBusy.current = false; setSelecting(false) } }
  }
  return <div className="space-y-4">
    <div className="flex flex-wrap items-center gap-2"><Input aria-label={t("search")} placeholder={t("search")} className="max-w-sm" value={draft} onChange={event => setDraft(event.target.value)} /><label className="text-sm" htmlFor="global-review-severity">{findingsT("severity")}</label><select id="global-review-severity" className="h-9 rounded border bg-background px-2 text-sm" value={severity} onChange={event => { setSeverity(event.target.value); setPage(0) }}><option value="">{findingsT("all")}</option>{["critical", "high", "medium", "low", "info"].map(value => <option key={value} value={value}>{findingsT(`severityLabel.${value}`)}</option>)}</select></div>
    <div className="flex flex-wrap items-center justify-between gap-2"><span className="text-sm text-muted-foreground">{t("selection", { findings: items.length, tasks: taskCount })} · {t("selectGroupHint")}</span><Button disabled={selecting || create.isPending || items.length < 1 || items.length > 200 || taskCount > 20} onClick={() => create.mutate()}>{t("start")}</Button></div>
    {items.length > 0 && <div className="flex flex-wrap gap-1">{items.map(item => <button type="button" key={item.id} disabled={selecting || create.isPending} className="max-w-64 truncate rounded bg-muted px-2 py-1 text-xs" aria-label={t("removeSelected", { name: nameOf(item), task: item.task_name })} onClick={() => removeSelected(item.id)}>{item.task_name} · {nameOf(item)} ×</button>)}</div>}
    {submitted.length > 0 && <section className="space-y-2 rounded border p-3"><h3 className="text-sm font-medium">{t("queuedTitle")}</h3>{submitted.map(({ review, taskName }) => <div key={review.id} className="flex flex-wrap items-center gap-2 text-sm"><span className="flex-1">{taskName}</span><Badge variant="outline">{t("queued")}</Badge>{review.conversation_id && <Link className="text-primary underline" href={`/ai/chat/?c=${encodeURIComponent(review.conversation_id)}`}>{t("conversation")}</Link>}<Link className="text-primary underline" href={`/ai/tasks/${encodeURIComponent(review.task_id)}/?tab=findings`}>{t("task")}</Link></div>)}</section>}
    {list.isError ? <p role="alert" className="text-sm text-destructive">{t("loadError")}</p> : <div className="divide-y rounded border">{list.data?.items?.map(item => <article key={item.id} className="flex flex-wrap items-start gap-3 p-3 text-sm"><input type="checkbox" aria-label={t("select", { name: nameOf(item), task: item.task_name })} disabled={selecting || create.isPending} checked={item.id in selected} onChange={event => void selectFinding(item, event.target.checked)} /><div className="min-w-0 flex-1"><strong className="break-words">{nameOf(item)}</strong><p className="break-words text-xs text-muted-foreground">{item.summary}</p><p className="mt-1 text-xs text-muted-foreground">{item.task_name}{item.target_name ? ` · ${item.target_name}` : ""}</p></div><Badge variant="outline">{["critical", "high", "medium", "low", "info"].includes(item.severity) ? findingsT(`severityLabel.${item.severity}`) : item.severity || findingsT("unknown")}</Badge>{item.in_group && <span className="text-xs text-muted-foreground">{t("inGroup")}</span>}<Link className="text-xs text-primary underline" href={`/ai/tasks/${encodeURIComponent(item.task_id)}/?tab=findings&finding=${encodeURIComponent(item.id)}`}>{t("viewFinding")}</Link></article>)}{!list.isLoading && !list.data?.items?.length && <p className="p-3 text-sm text-muted-foreground">{t("empty")}</p>}</div>}
    <div className="flex flex-wrap items-center justify-end gap-2 text-sm"><span>{findingsT("total", { count: list.data?.total ?? 0 })} · {t("page", { page: page + 1, pages: pageCount })}</span><Button size="sm" variant="outline" disabled={page === 0 || list.isFetching} onClick={() => setPage(value => value - 1)}>{findingsT("previous")}</Button><Button size="sm" variant="outline" disabled={list.isFetching || page + 1 >= pageCount} onClick={() => setPage(value => value + 1)}>{findingsT("next")}</Button><Input aria-label={t("jumpPage")} inputMode="numeric" className="h-8 w-20" value={jump} onChange={event => setJump(event.target.value)} onKeyDown={event => { if (event.key === "Enter") applyJump() }} /><Button size="sm" variant="outline" onClick={applyJump}>{t("jump")}</Button></div>
  </div>
}
