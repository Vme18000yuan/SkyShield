"use client"

import * as React from "react"
import Link from "next/link"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { useAuth } from "@/hooks/use-auth"
import { api } from "@/lib/api-client"
import { loadReviewFindingGroup } from "@/components/ai/artex/finding-review-selection"
import type { AITaskFinding, AITaskFindingPage } from "@/types/ai-task-finding.types"
import type { AIFindingReview, AIFindingReviewPage } from "@/types/ai-finding-review.types"

export interface FindingReviewSeed { findings: Record<string, string>; caseId?: string }
type SelectedFinding = { name: string; caseId?: string }
const findingName = (item: Pick<AITaskFinding, "name" | "vulnclass">) => item.name || item.vulnclass || "—"

export function FindingCaseReviews({ taskId, seed, onSeedConsumed }: { taskId: string; seed?: FindingReviewSeed | null; onSeedConsumed?: () => void }) {
  const t = useTranslations("aiFindingReviews")
  const findingsT = useTranslations("aiFindings")
  const user = useAuth().data?.user?.id
  const cache = useQueryClient()
  const base = `/aiTasks/${encodeURIComponent(taskId)}/findingCaseReviews`
  const key = ["finding-case-reviews", user, taskId]
  const [page, setPage] = React.useState(0)
  const [open, setOpen] = React.useState(false)
  const [selected, setSelected] = React.useState<Record<string, SelectedFinding>>({})
  const [selecting, setSelecting] = React.useState(false)
  const selectionEpoch = React.useRef(0)
  const selectionBusy = React.useRef(false)
  const [search, setSearch] = React.useState("")
  const [query, setQuery] = React.useState("")
  const [findingPage, setFindingPage] = React.useState(0)
  const [detailId, setDetailId] = React.useState<string | null>(null)
  React.useEffect(() => {
    const timer = window.setTimeout(() => { setQuery(search.trim()); setFindingPage(0) }, 300)
    return () => window.clearTimeout(timer)
  }, [search])
  React.useEffect(() => {
    if (!seed) return
    setSelected(Object.fromEntries(Object.entries(seed.findings).map(([id, name]) => [id, { name, caseId: seed.caseId }]))); setOpen(true); onSeedConsumed?.()
  }, [seed, onSeedConsumed])
  React.useEffect(() => () => { selectionEpoch.current++ }, [])
  const reviews = useQuery({ queryKey: [...key, page], enabled: !!user, gcTime: 0, refetchInterval: page === 0 ? 3000 : false,
    queryFn: async () => (await api.get<AIFindingReviewPage>(base, { params: { page, size: 25 } })).data })
  const detail = useQuery({ queryKey: [...key, "detail", detailId], enabled: !!user && !!detailId, gcTime: 0,
    queryFn: async () => (await api.get<AIFindingReview>(`${base}/${encodeURIComponent(detailId!)}`)).data })
  const choices = useQuery({ queryKey: ["finding-case-review-choices", user, taskId, query, findingPage], enabled: !!user && open, gcTime: 0,
    queryFn: async () => (await api.get<AITaskFindingPage>(`/aiTasks/${encodeURIComponent(taskId)}/findings`, { params: { own: 1, q: query || undefined, page: findingPage, size: 25 } })).data })
  const create = useMutation({ mutationFn: () => api.post(base, { finding_ids: Object.keys(selected) }),
    onSuccess: async () => { setOpen(false); setSelected({}); setSearch(""); setFindingPage(0); setPage(0); await cache.invalidateQueries({ queryKey: key }); toast.success(t("started")) },
    onError: () => toast.error(t("startError")) })
  const stop = useMutation({ mutationFn: (item: AIFindingReview) => item.state === "queued"
    ? api.post(`${base}/${encodeURIComponent(item.id)}/cancel`)
    : api.post(`/aiConversations/${encodeURIComponent(item.conversation_id!)}/stop`),
    onSuccess: async () => { await cache.invalidateQueries({ queryKey: key }); toast.success(t("stopped")) },
    onError: async () => { await cache.invalidateQueries({ queryKey: key }); toast.error(t("stopError")) } })
  const removeSelected = (id: string) => setSelected(current => {
    const group = current[id]?.caseId
    return Object.fromEntries(Object.entries(current).filter(([key, value]) => group ? value.caseId !== group : key !== id))
  })
  const selectFinding = async (item: AITaskFinding, checked: boolean) => {
    if (selectionBusy.current) return
    if (!checked) { removeSelected(item.id); return }
    if (!item.case_id) {
      if (Object.keys(selected).length >= 200) { toast.error(t("selectionLimit")); return }
      setSelected(current => ({ ...current, [item.id]: { name: findingName(item) } })); return
    }
    selectionBusy.current = true; setSelecting(true)
    const epoch = selectionEpoch.current
    try {
      const group = await loadReviewFindingGroup(taskId, item.case_id)
      if (epoch !== selectionEpoch.current) return
      if (!group.members!.some(member => member.id === item.id)) throw new Error("finding no longer belongs to group")
      const additions = Object.fromEntries(group.members!.map(member => [member.id, { name: findingName(member), caseId: group.id }]))
      if (Object.keys({ ...selected, ...additions }).length > 200) { toast.error(t("selectionLimit")); return }
      setSelected(current => ({ ...current, ...additions }))
      toast.success(t("groupExpanded", { count: group.members!.length }))
    } catch { if (epoch === selectionEpoch.current) toast.error(t("groupLoadError")) }
    finally { if (epoch === selectionEpoch.current) { selectionBusy.current = false; setSelecting(false) } }
  }
  const close = (next: boolean) => { setOpen(next); if (!next) { selectionEpoch.current++; selectionBusy.current = false; setSelecting(false); setSelected({}); setSearch(""); setFindingPage(0) } }
  return <section className="space-y-2 rounded border p-3"><div className="flex flex-wrap items-center justify-between gap-2"><div><h4 className="text-sm font-medium">{t("title")}</h4><p className="text-xs text-muted-foreground">{t("description")}</p></div><Button size="sm" variant="outline" onClick={() => setOpen(true)}>{t("start")}</Button></div>
    {reviews.isError ? <p role="alert" className="text-sm text-destructive">{t("loadError")}</p> : reviews.data?.items?.length ? <div className="divide-y rounded border">{reviews.data.items.map(item => <article key={item.id} className="flex flex-wrap items-center gap-2 p-2 text-sm"><Badge variant="outline">{["queued", "running", "completed", "failed", "stopped"].includes(item.state) ? t(`state.${item.state}`) : item.state}</Badge><span className="min-w-0 flex-1">{t("findingCount", { count: item.finding_count })}<span className="ml-2 text-xs text-muted-foreground">{new Date(item.created_at).toLocaleString()}</span>{item.error && <span className="block break-words text-xs text-destructive">{item.error}</span>}</span>{item.conversation_id && <Link className="text-xs text-primary underline" href={`/ai/chat/?c=${encodeURIComponent(item.conversation_id)}`}>{t("conversation")}</Link>}{(item.state === "queued" || item.state === "running") && <Button size="sm" variant="outline" disabled={stop.isPending || item.state === "running" && !item.conversation_id} onClick={() => stop.mutate(item)}>{t("stop")}</Button>}<Button size="sm" variant="outline" onClick={() => setDetailId(item.id)}>{t("details")}</Button></article>)}</div> : <p className="text-sm text-muted-foreground">{t("empty")}</p>}
    {reviews.data && reviews.data.total > reviews.data.size && <div className="flex justify-end gap-2"><Button size="sm" variant="outline" disabled={page === 0 || reviews.isFetching} onClick={() => setPage(value => value - 1)}>{findingsT("previous")}</Button><Button size="sm" variant="outline" disabled={reviews.isFetching || (page + 1) * reviews.data.size >= reviews.data.total} onClick={() => setPage(value => value + 1)}>{findingsT("next")}</Button></div>}
    <Dialog open={open} onOpenChange={close}><DialogContent className="max-h-[90vh] overflow-auto sm:max-w-2xl"><DialogHeader><DialogTitle>{t("start")}</DialogTitle><DialogDescription>{t("selectDescription")}</DialogDescription></DialogHeader><Input aria-label={t("search")} placeholder={t("search")} value={search} onChange={event => setSearch(event.target.value)} /><p className="text-xs text-muted-foreground"><span>{t("selected", { count: Object.keys(selected).length })}</span> · {t("selectGroupHint")}</p>
      {Object.keys(selected).length > 0 && <div className="flex flex-wrap gap-1">{Object.entries(selected).map(([id, item]) => <button type="button" key={id} disabled={selecting || create.isPending} className="max-w-48 truncate rounded bg-muted px-2 py-1 text-xs" aria-label={t("removeSelected", { name: item.name })} onClick={() => removeSelected(id)}>{item.name} ×</button>)}</div>}
      <div className="max-h-60 divide-y overflow-auto rounded border">{choices.isError ? <p role="alert" className="p-3 text-sm text-destructive">{t("loadError")}</p> : choices.data?.items?.map(item => <label key={item.id} className="flex cursor-pointer items-start gap-2 p-2 text-sm"><input type="checkbox" disabled={selecting || create.isPending} checked={item.id in selected} onChange={event => void selectFinding(item, event.target.checked)} /><span className="min-w-0 break-words">{findingName(item)} {item.case_id && <span className="text-xs text-muted-foreground">{t("inGroup")}</span>} <span className="text-xs text-muted-foreground">{item.summary}</span></span></label>)}</div>
      <div className="flex items-center justify-between gap-2 text-sm"><span>{findingsT("total", { count: choices.data?.total ?? 0 })}</span><div className="flex gap-2"><Button size="sm" variant="outline" disabled={findingPage === 0 || choices.isFetching} onClick={() => setFindingPage(value => value - 1)}>{findingsT("previous")}</Button><Button size="sm" variant="outline" disabled={choices.isFetching || !choices.data || (findingPage + 1) * choices.data.size >= choices.data.total} onClick={() => setFindingPage(value => value + 1)}>{findingsT("next")}</Button></div></div>
      <div className="flex justify-end gap-2"><Button variant="outline" onClick={() => close(false)}>{t("cancel")}</Button><Button disabled={selecting || create.isPending || Object.keys(selected).length < 1 || Object.keys(selected).length > 200} onClick={() => create.mutate()}>{t("confirmStart")}</Button></div>
    </DialogContent></Dialog>
    <Dialog open={!!detailId} onOpenChange={next => { if (!next) setDetailId(null) }}><DialogContent className="max-h-[90vh] overflow-auto sm:max-w-2xl"><DialogHeader><DialogTitle>{t("details")}</DialogTitle><DialogDescription>{t("detailsDescription")}</DialogDescription></DialogHeader>{detail.isError ? <p role="alert" className="text-destructive">{t("loadError")}</p> : detail.data ? <div className="space-y-3"><Badge variant="outline">{["queued", "running", "completed", "failed", "stopped"].includes(detail.data.state) ? t(`state.${detail.data.state}`) : detail.data.state}</Badge>{detail.data.error && <p className="break-words text-sm text-destructive">{detail.data.error}</p>}{detail.data.conclusion?.summary && <p className="break-words text-sm">{detail.data.conclusion.summary}</p>}{detail.data.conclusion?.decisions?.map(item => <article key={item.finding_id} className="rounded border p-2 text-sm"><strong>{findingName(detail.data!.findings?.find(finding => finding.id === item.finding_id) || { name: "", vulnclass: `#${item.finding_id}` })} · {t(`verdict.${item.verdict}`)}</strong><p className="break-words text-muted-foreground">{item.reason}</p><p className="break-words text-xs text-muted-foreground">{item.root_cause_evidence}</p></article>)}</div> : <p>{findingsT("loading")}</p>}</DialogContent></Dialog>
  </section>
}
