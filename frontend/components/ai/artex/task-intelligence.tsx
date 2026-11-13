// Adapted from ARTEX, AGPL-3.0-only; see third_party/artex/SKYSHIELD-NOTICE.md.
"use client"

import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { useAuth } from "@/hooks/use-auth"
import { api } from "@/lib/api-client"
import type { CredentialAnalysis, CredentialTrafficDetail } from "@/types/artex-ai.types"

type Kind = "credential" | "lead" | "sensitive_data"
type Intelligence = { id:string; task_id:string; kind:Kind; title:string; content:string; evidence:string; confidence:string; status:string; inherited:boolean; source_session?:string; created_at:string; credentials?:CredentialAnalysis }
type Page = { items:Intelligence[]; total:number; counts:Record<Kind,number>; enabled:boolean; credential_agent_enabled:boolean }
const statuses:Record<Kind,string[]> = { credential:["unverified","verified","invalid"],lead:["pending","in_progress","dismissed","converted"],sensitive_data:["suspected","confirmed","dismissed"] }

export function TaskIntelligence({ taskId, sourceNames, running }: { taskId:string; sourceNames:Record<string,string>; running:boolean }) {
  const t=useTranslations("aiTasks.intelligence")
  const user=useAuth().data?.user?.id
  const cache=useQueryClient()
  const [kind,setKind]=React.useState<Kind | "">("")
  const [draft,setDraft]=React.useState("")
  const [query,setQuery]=React.useState("")
  const [page,setPage]=React.useState(0)
  const [error,setError]=React.useState("")
  const [trafficSelection,setTrafficSelection]=React.useState<{intel:string;id:string}|null>(null)
  React.useEffect(()=>{setTrafficSelection(null);setKind("");setDraft("");setQuery("");setPage(0);setError("")},[user,taskId])
  const base=`/aiTasks/${encodeURIComponent(taskId)}/intelligence`
  React.useEffect(()=>{const timer=window.setTimeout(()=>{setQuery(draft.trim());setPage(0)},300);return()=>window.clearTimeout(timer)},[draft])
  const key=["task-intelligence",user,taskId,kind,query,page]
  const items=useQuery({queryKey:key,enabled:!!user,gcTime:0,queryFn:async()=>(await api.get<Page>(base,{params:{kind:kind||undefined,q:query||undefined,limit:25,offset:page*25}})).data,refetchInterval:query=>running||!!(query.state.data?.credential_agent_enabled&&query.state.data.items.some(item=>item.kind==="credential"&&(!item.credentials||item.credentials.state==="queued"||item.credentials.state==="processing")))?3000:false})
  const trafficDetail=useQuery({queryKey:["credential-traffic",user,taskId,trafficSelection?.intel,trafficSelection?.id],enabled:!!user&&!!trafficSelection,gcTime:0,queryFn:async()=>(await api.get<CredentialTrafficDetail>(`${base}/${encodeURIComponent(trafficSelection!.intel)}/traffic/${encodeURIComponent(trafficSelection!.id)}`)).data})
  const update=useMutation({mutationFn:({id,status}:{id:string;status:string})=>api.patch(`${base}/${encodeURIComponent(id)}`,{status}),onSuccess:async()=>{setError("");await cache.invalidateQueries({queryKey:["task-intelligence",user,taskId]})},onError:()=>setError(t("updateError"))})
  const retry=useMutation({mutationFn:(id:string)=>api.post(`${base}/${encodeURIComponent(id)}/credentials/retry`),onSuccess:async()=>{setError("");await cache.invalidateQueries({queryKey:["task-intelligence",user,taskId]})},onError:()=>setError(t("retryError"))})
  const labels:Record<Kind,string>={credential:t("credential"),lead:t("lead"),sensitive_data:t("sensitiveData")}
  return <div className="flex flex-col gap-3">
    {items.data && !items.data.enabled && <p className="text-sm text-muted-foreground">{t("disabled")}</p>}
    <div className="flex flex-wrap items-center gap-2">
      <select aria-label={t("kindFilter")} className="h-9 rounded-md border bg-background px-2 text-sm" value={kind} onChange={event=>{setKind(event.target.value as Kind | "");setPage(0)}}><option value="">{t("allKinds")}</option>{Object.entries(labels).map(([value,label])=><option value={value} key={value}>{label} ({items.data?.counts?.[value as Kind]??0})</option>)}</select>
      <Input className="max-w-sm" aria-label={t("search")} placeholder={t("search")} value={draft} onChange={event=>setDraft(event.target.value)} />
    </div>
    {(error||items.isError)&&<p role="alert" className="text-sm text-destructive">{error||t("loadError")}</p>}
    {items.data?.items.map(item=><article className="rounded-md border bg-card p-3" key={item.id}>
      <div className="flex flex-wrap items-center gap-2 text-sm"><span className="rounded bg-muted px-2 py-0.5 text-xs">{labels[item.kind]}</span><strong className="min-w-0 flex-1 break-words">{item.title}</strong>{item.inherited&&<span className="text-xs text-muted-foreground">{t("inherited",{name:sourceNames[item.task_id]??t("sourceTask")})}</span>}<span className="text-xs text-muted-foreground">{item.confidence==="observed"?t("observed"):t("inferred")}</span>
        {item.inherited?<span className="text-xs text-muted-foreground">{t(`status.${item.status}`)}</span>:<select aria-label={t("statusFor",{name:item.title})} className="h-8 rounded-md border bg-background px-2 text-xs" value={item.status} disabled={update.isPending} onChange={event=>update.mutate({id:item.id,status:event.target.value})}>{statuses[item.kind].map(status=><option key={status} value={status}>{t(`status.${status}`)}</option>)}</select>}
      </div>
      {item.source_session&&<p className="mt-1 text-xs text-muted-foreground">{t("source")}: {item.source_session}</p>}
      {item.kind==="credential"&&<div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground"><span>{t("credentialProcessing")}: {item.credentials?.state?t(`processor.${item.credentials.state}`):t("processor.notStarted")}</span>{item.credentials?.error&&<span className="text-destructive">{item.credentials.error}</span>}{!item.inherited&&<Button size="sm" variant="outline" disabled={!items.data?.credential_agent_enabled||retry.isPending||item.credentials?.state==="processing"} onClick={()=>retry.mutate(item.id)}>{t("retry")}</Button>}</div>}
      <details className="mt-2 text-sm"><summary className="cursor-pointer text-muted-foreground">{t("viewDetails")}</summary><div className="mt-2 grid gap-2"><div><strong className="text-xs">{t("content")}</strong><pre className="whitespace-pre-wrap break-all rounded bg-muted/40 p-2 text-xs">{item.content}</pre></div><div><strong className="text-xs">{t("evidence")}</strong><pre className="whitespace-pre-wrap break-all rounded bg-muted/40 p-2 text-xs">{item.evidence}</pre></div>{!!item.credentials?.accounts?.length&&<div><strong className="text-xs">{t("accounts")}</strong>{item.credentials.accounts.map((account,index)=><pre key={index} className="mt-1 whitespace-pre-wrap break-all rounded bg-muted/40 p-2 text-xs">{JSON.stringify(account,null,2)}</pre>)}</div>}{item.kind==="credential"&&item.credentials?.traffic_state&&<div><strong className="text-xs">{t("traffic")}</strong>{item.credentials.traffic_note&&<p className="text-xs text-muted-foreground">{item.credentials.traffic_note}</p>}{item.credentials.traffic?.map(ref=><div key={ref.id} className="mt-2 rounded border p-2 text-xs"><p className="break-all">{ref.method} {ref.url} · {ref.status}</p><pre className="mt-1 whitespace-pre-wrap break-all">{ref.excerpt}</pre><Button size="sm" variant="outline" className="mt-1" onClick={()=>setTrafficSelection({intel:item.id,id:ref.id})}>{t("viewTraffic")}</Button></div>)}</div>}</div></details>
    </article>)}
    {items.data&&!items.data.items.length&&<p className="text-sm text-muted-foreground">{t("empty")}</p>}
    <Dialog open={!!trafficSelection} onOpenChange={open=>{if(!open)setTrafficSelection(null)}}><DialogContent className="max-h-[85vh] overflow-auto sm:max-w-4xl"><DialogHeader><DialogTitle>{t("traffic")}</DialogTitle><DialogDescription>{trafficDetail.data?.reference.url}</DialogDescription></DialogHeader>{trafficDetail.isError?<p role="alert" className="text-destructive">{t("loadError")}</p>:trafficDetail.data?.unavailable?<p className="text-sm text-muted-foreground">{t("trafficUnavailable")}</p>:trafficDetail.data?<div className="space-y-3"><div><strong>{t("trafficRequest")}</strong><pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded bg-muted p-2 text-xs">{trafficDetail.data.req}</pre></div><div><strong>{t("trafficResponse")}</strong><pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded bg-muted p-2 text-xs">{trafficDetail.data.resp}</pre></div></div>:<p>{t("loadingTraffic")}</p>}</DialogContent></Dialog>
    <div className="flex items-center justify-end gap-2 text-sm text-muted-foreground"><span>{t("total",{count:items.data?.total??0})}</span><span>{t("page",{number:page+1})}</span><Button size="sm" variant="outline" disabled={page===0||items.isFetching} onClick={()=>setPage(value=>value-1)}>{t("previous")}</Button><Button size="sm" variant="outline" disabled={(page+1)*25>=(items.data?.total??0)||items.isFetching} onClick={()=>setPage(value=>value+1)}>{t("next")}</Button></div>
  </div>
}
