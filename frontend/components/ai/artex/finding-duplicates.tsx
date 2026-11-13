"use client"

import * as React from "react"
import { useQuery } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { useAuth } from "@/hooks/use-auth"
import { api } from "@/lib/api-client"
import type { AITaskFinding } from "@/types/ai-task-finding.types"
import type { AIFindingDuplicate, AIFindingDuplicatePage } from "@/types/ai-finding-duplicate.types"

export interface FindingCaseMergeSeed {
  title: string
  findings: Record<string, string>
}

const title = (item: { name: string; vulnclass: string }) => item.name || item.vulnclass || "—"

export function FindingDuplicates({ taskId, finding, onClose, onMerge }: {
  taskId: string
  finding: AITaskFinding
  onClose: () => void
  onMerge: (seed: FindingCaseMergeSeed) => void
}) {
  const t = useTranslations("aiFindingDuplicates")
  const findingsT = useTranslations("aiFindings")
  const user = useAuth().data?.user?.id
  const [page, setPage] = React.useState(0)
  const [selected, setSelected] = React.useState<Record<string, AIFindingDuplicate>>({})
  const candidates = useQuery({
    queryKey: ["finding-duplicates", user, taskId, finding.id, page], enabled: !!user, gcTime: 0,
    queryFn: async () => (await api.get<AIFindingDuplicatePage>(`/aiTasks/${encodeURIComponent(taskId)}/findings/${encodeURIComponent(finding.id)}/duplicates`, { params: { page, size: 20 } })).data,
  })
  return <Dialog open onOpenChange={open => { if (!open) onClose() }}><DialogContent className="max-h-[90vh] overflow-auto sm:max-w-3xl"><DialogHeader><DialogTitle>{t("title", { name: title(finding) })}</DialogTitle><DialogDescription>{t("description")}</DialogDescription></DialogHeader>
    {finding.inherited && <p className="text-sm text-muted-foreground">{t("readOnly")}</p>}
    <p className="text-xs text-muted-foreground">{t("counts", { total: candidates.data?.total ?? 0, strong: candidates.data?.strong_total ?? 0 })}</p>
    {candidates.isError ? <p role="alert" className="text-sm text-destructive">{t("loadError")}</p> : <div className="divide-y rounded border">{candidates.data?.items?.map(item => <label key={item.id} className="flex items-start gap-2 p-3 text-sm">{!finding.inherited && <input type="checkbox" checked={item.id in selected} onChange={event => setSelected(current => { const next = { ...current }; if (event.target.checked) next[item.id] = item; else delete next[item.id]; return next })} />}<span className="min-w-0 flex-1"><strong className="break-words">{title(item)}</strong><span className="ml-2 text-xs text-muted-foreground">{item.summary}</span><span className="mt-1 flex flex-wrap gap-1">{item.hit_reasons.map(reason => <Badge key={reason} variant="outline">{["same_type", "asset_overlap", "same_name"].includes(reason) ? t(`reason.${reason}`) : reason}</Badge>)}{item.assets.slice(0, 2).map(asset => <span key={asset.id} className="max-w-48 truncate rounded bg-muted px-1 text-xs" title={asset.label}>{asset.label}</span>)}{item.case_id && <span className="text-xs text-muted-foreground">{t("inGroup")}</span>}</span></span><Badge variant="outline">{item.tier === "strong" ? t("strong") : t("weak")}</Badge></label>)}{!candidates.isLoading && !candidates.data?.items?.length && <p className="p-3 text-sm text-muted-foreground">{t("empty")}</p>}</div>}
    <div className="flex flex-wrap items-center justify-between gap-2 text-sm"><span>{findingsT("total", { count: candidates.data?.total ?? 0 })} · {findingsT("page", { page: page + 1 })}</span><div className="flex gap-2"><Button size="sm" variant="outline" disabled={page === 0 || candidates.isFetching} onClick={() => setPage(value => value - 1)}>{findingsT("previous")}</Button><Button size="sm" variant="outline" disabled={candidates.isFetching || !candidates.data || (page + 1) * candidates.data.size >= candidates.data.total} onClick={() => setPage(value => value + 1)}>{findingsT("next")}</Button></div></div>
    {!finding.inherited && <div className="flex justify-end"><Button disabled={Object.keys(selected).length === 0 || Object.keys(selected).length >= 100} onClick={() => onMerge({ title: title(finding), findings: { [finding.id]: title(finding), ...Object.fromEntries(Object.values(selected).map(item => [item.id, title(item)])) } })}>{t("prepareMerge", { count: Object.keys(selected).length + 1 })}</Button></div>}
  </DialogContent></Dialog>
}
