"use client"

import * as React from "react"
import Link from "next/link"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { FindingRetestPanel } from "@/components/ai/finding-retest-panel"
import { FindingTrafficPanel } from "@/components/ai/artex/task-traffic"
import { TaskFindingCases } from "@/components/ai/artex/task-finding-cases"
import { FindingDuplicates, type FindingCaseMergeSeed } from "@/components/ai/artex/finding-duplicates"
import { Markdown } from "@/components/settings/ai/artex-markdown"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { useAuth } from "@/hooks/use-auth"
import { api } from "@/lib/api-client"
import type { AIFindingStatus, AITaskFinding, AITaskFindingPage } from "@/types/ai-task-finding.types"

const statuses:AIFindingStatus[]=["pending","in_progress","confirmed","resolved","fixed","false_positive","ignored","duplicate","risk_accepted"]
function title(item:AITaskFinding){return item.name||item.vulnclass||"—"}

export function TaskFindings({taskId}:{taskId:string}) {
  const user=useAuth().data?.user?.id
  return <TaskFindingsInner key={`${user??"anonymous"}:${taskId}`} taskId={taskId}/>
}
function TaskFindingsInner({taskId}:{taskId:string}) {
  const t=useTranslations("aiFindings")
  const user=useAuth().data?.user?.id
  const cache=useQueryClient()
  const [severity,setSeverity]=React.useState("")
  const [draft,setDraft]=React.useState("")
  const [query,setQuery]=React.useState("")
  const [page,setPage]=React.useState(0)
  const [selected,setSelected]=React.useState<AITaskFinding|null>(null)
  const [linkedId,setLinkedId]=React.useState<string|null>(null)
  const [duplicateFinding,setDuplicateFinding]=React.useState<AITaskFinding|null>(null)
  const [mergeSeed,setMergeSeed]=React.useState<FindingCaseMergeSeed|null>(null)
  const consumeMergeSeed=React.useCallback(()=>setMergeSeed(null),[])
  React.useEffect(()=>{const timer=window.setTimeout(()=>{setQuery(draft.trim());setPage(0)},300);return()=>window.clearTimeout(timer)},[draft])
  const base=`/aiTasks/${encodeURIComponent(taskId)}/findings`
  const key=["task-findings",user,taskId]
  const list=useQuery({queryKey:[...key,severity,query,page],enabled:!!user,gcTime:0,queryFn:async()=>(await api.get<AITaskFindingPage>(base,{params:{severity:severity||undefined,q:query||undefined,page,size:25}})).data,refetchInterval:page===0?5000:false})
  const detail=useQuery({queryKey:[...key,"detail",selected?.id],enabled:!!user&&!!selected,gcTime:0,queryFn:async()=>(await api.get<AITaskFinding>(`${base}/${encodeURIComponent(selected!.id)}`)).data})
  const linked=useQuery({queryKey:[...key,"detail",linkedId],enabled:!!user&&!!linkedId,gcTime:0,retry:false,queryFn:async()=>(await api.get<AITaskFinding>(`${base}/${encodeURIComponent(linkedId!)}`)).data})
  React.useEffect(()=>{const id=new URLSearchParams(window.location.search).get("finding");if(id&&/^[1-9][0-9]{0,18}$/.test(id))setLinkedId(id)},[])
  React.useEffect(()=>{if(!linkedId)return;if(linked.data){setSelected(linked.data);setLinkedId(null)}else if(linked.isError){toast.error(t("loadError"));setLinkedId(null)}},[linkedId,linked.data,linked.isError,t])
  const update=useMutation({mutationFn:({id,status}:{id:string;status:AIFindingStatus})=>api.patch(`${base}/${encodeURIComponent(id)}/status`,{status}),onSuccess:async()=>{await cache.invalidateQueries({queryKey:key});toast.success(t("updated"))},onError:()=>toast.error(t("updateError"))})
  return <div className="space-y-3">
    <p className="text-xs text-muted-foreground">{t("statusHint")}</p>
    <div className="flex flex-wrap items-center gap-2"><label className="text-sm" htmlFor="finding-severity">{t("severity")}</label><select id="finding-severity" aria-label={t("severity")} className="h-9 rounded border bg-background px-2 text-sm" value={severity} onChange={event=>{setSeverity(event.target.value);setPage(0)}}><option value="">{t("all")}</option>{["critical","high","medium","low","info"].map(value=><option key={value} value={value}>{t(`severityLabel.${value}`)}</option>)}</select><Input aria-label={t("search")} placeholder={t("search")} className="max-w-sm" value={draft} onChange={event=>setDraft(event.target.value)}/></div>
    <TaskFindingCases taskId={taskId} severity={severity} query={query} mergeSeed={mergeSeed} onMergeSeedConsumed={consumeMergeSeed}/>
    {list.isError?<p role="alert" className="text-sm text-destructive">{t("loadError")}</p>:<div className="rounded border">{list.data?.items.map(item=><article key={`${item.task_id}:${item.id}`} className="flex flex-wrap items-center gap-3 border-b p-3 text-sm last:border-b-0"><Badge variant="outline">{item.severity&&["critical","high","medium","low","info"].includes(item.severity)?t(`severityLabel.${item.severity}`):item.severity||t("unknown")}</Badge><div className="min-w-0 flex-1"><strong className="break-words">{title(item)}</strong><p className="break-words text-xs text-muted-foreground">{item.summary}</p><div className="mt-1 flex flex-wrap gap-1">{item.assets.slice(0,2).map(asset=><span className="max-w-48 truncate rounded bg-muted px-1 text-xs" title={asset.label} key={asset.id}>{asset.label}</span>)}{item.assets.length>2&&<span className="text-xs text-muted-foreground">+{item.assets.length-2}</span>}{item.inherited&&<span className="text-xs text-muted-foreground">{t("inherited",{name:item.task_name})}</span>}</div></div><span className="text-xs text-muted-foreground">{t("trafficCount",{count:item.traffic_count})}</span>{item.inherited?<span className="text-xs text-muted-foreground">{statuses.includes(item.status)?t(`status.${item.status}`):item.status}</span>:<select aria-label={t("statusFor",{name:title(item)})} className="h-8 rounded border bg-background px-2 text-xs" value={item.status} disabled={update.isPending} onChange={event=>update.mutate({id:item.id,status:event.target.value as AIFindingStatus})}>{!statuses.includes(item.status)&&<option value={item.status}>{item.status||t("unknown")}</option>}{statuses.map(status=><option key={status} value={status}>{t(`status.${status}`)}</option>)}</select>}<Button size="sm" variant="outline" aria-label={t("similarFor",{name:title(item)})} onClick={()=>setDuplicateFinding(item)}>{t("similar")}</Button><Button size="sm" variant="outline" onClick={()=>setSelected(item)}>{t("details")}</Button></article>)}{!list.isLoading&&!list.data?.items.length&&<p className="p-3 text-sm text-muted-foreground">{t("empty")}</p>}</div>}
    <div className="flex items-center justify-end gap-2 text-sm"><span>{t("total",{count:list.data?.total??0})} · {t("page",{page:page+1})}</span><Button size="sm" variant="outline" disabled={page===0||list.isFetching} onClick={()=>setPage(value=>value-1)}>{t("previous")}</Button><Button size="sm" variant="outline" disabled={list.isFetching||!list.data||(page+1)*list.data.size>=list.data.total} onClick={()=>setPage(value=>value+1)}>{t("next")}</Button></div>
    <Dialog open={!!selected} onOpenChange={open=>{if(!open)setSelected(null)}}><DialogContent className="max-h-[90vh] overflow-auto sm:max-w-4xl"><DialogHeader><DialogTitle>{selected?title(selected):t("details")}</DialogTitle><DialogDescription>{selected?.inherited?t("inherited",{name:selected.task_name}):selected?.summary}</DialogDescription></DialogHeader>{detail.isError?<p role="alert" className="text-destructive">{t("loadError")}</p>:detail.data?<div className="space-y-4"><section><h3 className="text-sm font-semibold">{t("evidence")}</h3><pre className="mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded bg-muted p-3 text-xs">{detail.data.evidence||t("noEvidence")}</pre></section>{detail.data.report&&<section><h3 className="text-sm font-semibold">{t("report")}</h3>{detail.data.report_evidence_version<detail.data.evidence_version&&<p className="text-xs text-muted-foreground">{t("reportOutdated")}</p>}<div className="mt-2"><Markdown text={detail.data.report}/></div></section>}{detail.data.inherited?<p className="text-sm text-muted-foreground">{t("readOnly")} <Link className="text-primary underline" href={`/ai/tasks/${encodeURIComponent(detail.data.task_id)}/`}>{detail.data.task_name}</Link></p>:<><details className="rounded border p-3"><summary className="cursor-pointer text-sm font-medium">{t("trafficEvidence")}</summary><div className="mt-3"><FindingTrafficPanel taskId={taskId} findingId={detail.data.id}/></div></details>{detail.data.node_id&&<details className="rounded border p-3"><summary className="cursor-pointer text-sm font-medium">{t("retest")}</summary><div className="mt-3"><FindingRetestPanel findingId={detail.data.id} findingName={title(detail.data)}/></div></details>}</>}</div>:<p>{t("loading")}</p>}</DialogContent></Dialog>
    {duplicateFinding&&<FindingDuplicates taskId={taskId} finding={duplicateFinding} onClose={()=>setDuplicateFinding(null)} onMerge={seed=>{setDuplicateFinding(null);setMergeSeed(seed)}}/>}
  </div>
}
