"use client"

import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { useAuth } from "@/hooks/use-auth"
import { api } from "@/lib/api-client"
import type { AITaskFinding } from "@/types/ai-task-finding.types"
import type { AIFindingSuggestion, AIFindingSuggestionPage } from "@/types/ai-finding-suggestion.types"

const label = (item: { name: string; vulnclass: string }) => item.name || item.vulnclass || "—"

export function FindingCaseSuggestions({ taskId }: { taskId: string }) {
  const t = useTranslations("aiFindingSuggestions")
  const findingsT = useTranslations("aiFindings")
  const user = useAuth().data?.user?.id
  const cache = useQueryClient()
  const base = `/aiTasks/${encodeURIComponent(taskId)}/findingCaseSuggestions`
  const key = ["finding-case-suggestions", user, taskId]
  const [page, setPage] = React.useState(0)
  const [active, setActive] = React.useState<{ item: AIFindingSuggestion; accept: boolean } | null>(null)
  const [excluded, setExcluded] = React.useState<string[]>([])
  const [evidenceId, setEvidenceId] = React.useState<string | null>(null)
  const suggestions = useQuery({ queryKey: [...key, page], enabled: !!user, gcTime: 0, refetchInterval: page === 0 ? 5000 : false,
    queryFn: async () => (await api.get<AIFindingSuggestionPage>(base, { params: { page, size: 25 } })).data })
  const evidence = useQuery({ queryKey: ["task-findings", user, taskId, "detail", evidenceId], enabled: !!user && !!active && !!evidenceId, gcTime: 0,
    queryFn: async () => (await api.get<AITaskFinding>(`/aiTasks/${encodeURIComponent(taskId)}/findings/${encodeURIComponent(evidenceId!)}`)).data })
  const resolve = useMutation({
    mutationFn: () => api.post(`${base}/${encodeURIComponent(active!.item.id)}/resolve`, { accept: active!.accept, exclude: active!.accept ? excluded : [] }),
    onSuccess: async () => {
      const accepted = active?.accept
      setActive(null); setExcluded([]); setEvidenceId(null)
      await Promise.all([cache.invalidateQueries({ queryKey: key }), cache.invalidateQueries({ queryKey: ["task-finding-cases", user, taskId] }), cache.invalidateQueries({ queryKey: ["finding-duplicates", user, taskId] })])
      toast.success(accepted ? t("accepted") : t("rejected"))
    },
    onError: async () => { setActive(null); await cache.invalidateQueries({ queryKey: key }); toast.error(t("resolveError")) },
  })
  const open = (item: AIFindingSuggestion, accept: boolean) => { setActive({ item, accept }); setExcluded([]); setEvidenceId(null) }
  if (suggestions.isError) return <p role="alert" className="text-sm text-destructive">{t("loadError")}</p>
  if (!suggestions.data?.total) return null
  return <section className="space-y-2 rounded border p-3"><div><h4 className="text-sm font-medium">{t("title", { count: suggestions.data.total })}</h4><p className="text-xs text-muted-foreground">{t("description")}</p></div>
    <div className="divide-y rounded border">{suggestions.data.items.map(item => <article key={item.id} className="space-y-2 p-3 text-sm"><div className="flex flex-wrap items-start gap-2"><div className="min-w-0 flex-1"><strong className="break-words">{item.title}</strong><p className="break-words text-xs text-muted-foreground">{item.reason}</p></div><Badge variant="outline">{t("memberCount", { count: item.members.length })}</Badge></div><p className="break-words text-xs text-muted-foreground">{item.members.map(label).join("、")}</p><div className="flex justify-end gap-2"><Button size="sm" variant="outline" onClick={() => open(item, false)}>{t("reject")}</Button><Button size="sm" onClick={() => open(item, true)}>{t("review")}</Button></div></article>)}</div>
    <div className="flex justify-end gap-2"><Button size="sm" variant="outline" disabled={page === 0 || suggestions.isFetching} onClick={() => setPage(value => value - 1)}>{findingsT("previous")}</Button><Button size="sm" variant="outline" disabled={suggestions.isFetching || (page + 1) * suggestions.data.size >= suggestions.data.total} onClick={() => setPage(value => value + 1)}>{findingsT("next")}</Button></div>
    <Dialog open={!!active} onOpenChange={openState => { if (!openState) { setActive(null); setEvidenceId(null) } }}><DialogContent className="max-h-[90vh] overflow-auto sm:max-w-2xl"><DialogHeader><DialogTitle>{active?.accept ? t("review") : t("reject")}</DialogTitle><DialogDescription>{active?.accept ? t("reviewDescription") : t("rejectDescription")}</DialogDescription></DialogHeader>
      {active && <><p className="text-sm font-medium">{active.item.title}</p><p className="break-words text-sm text-muted-foreground">{active.item.reason}</p><div className="divide-y rounded border">{active.item.members.map(member => <div key={member.id} className="flex flex-wrap items-center gap-2 p-2 text-sm">{active.accept && <input type="checkbox" aria-label={t("includeMember", { name: label(member) })} checked={!excluded.includes(member.id)} onChange={event => setExcluded(current => event.target.checked ? current.filter(id => id !== member.id) : [...current, member.id])} />}<span className="min-w-0 flex-1 break-words">{label(member)}</span><Button size="sm" variant="outline" onClick={() => setEvidenceId(current => current === member.id ? null : member.id)}>{evidenceId === member.id ? t("hideEvidence") : t("evidence")}</Button></div>)}</div>
        {evidenceId && <div className="rounded border p-3"><h5 className="text-sm font-medium">{findingsT("evidence")}</h5>{evidence.isError ? <p role="alert" className="text-sm text-destructive">{t("loadError")}</p> : evidence.data ? <pre className="mt-1 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded bg-muted p-3 text-xs">{evidence.data.evidence || findingsT("noEvidence")}</pre> : <p className="text-sm text-muted-foreground">{findingsT("loading")}</p>}</div>}
        <div className="flex justify-end gap-2"><Button variant="outline" onClick={() => setActive(null)}>{t("cancel")}</Button><Button disabled={resolve.isPending || active.accept && active.item.members.length - excluded.length < 2} onClick={() => resolve.mutate()}>{active.accept ? t("confirmAccept") : t("confirmReject")}</Button></div>
      </>}
    </DialogContent></Dialog>
  </section>
}
