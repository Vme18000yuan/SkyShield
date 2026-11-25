// Adapted from ARTEX's traffic viewer, AGPL-3.0-only.
"use client"

import * as React from "react"
import Link from "next/link"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { useAuth } from "@/hooks/use-auth"
import { AIDataDeleteButton } from "@/components/ai/ai-data-delete-button"
import { api } from "@/lib/api-client"
import { HttpRawMessage, httpRawLabels, joinHttpHeadBody, trafficRawPreview, HTTP_RAW_EXCHANGE_LAYOUT } from "@/components/shared/response-evidence"

type TrafficItem = { id:string; host:string; method:string; url:string; status:number; content_type:string; response_length:number; captured_at:string; source_type?:"task"|"conversation"; source_id?:string; source_name?:string }
type TrafficPage = { items:TrafficItem[]; total:number; page:number; size:number }
type Exchange = { request:string; response:string }

function BlobSlice({base,hash}:{base:string;hash:string}) {
  const t=useTranslations("aiTasks.traffic")
  const user=useAuth().data?.user?.id
  const [offset,setOffset]=React.useState(0)
  const query=useQuery({queryKey:["task-traffic-blob",user,base,hash,offset],enabled:!!user,gcTime:0,queryFn:async()=>(await api.get<{data:string|null;total:number}>(`${base}/blob`,{params:{hash,offset,length:8192}})).data})
  const decoded=React.useMemo(()=>{if(!query.data?.data)return "";try{const bytes=Uint8Array.from(atob(query.data.data),c=>c.charCodeAt(0));return new TextDecoder().decode(bytes)}catch{return ""}},[query.data])
  return <div className="space-y-2 rounded border p-2"><div className="flex flex-wrap items-center gap-2 text-xs"><span className="break-all font-mono">{hash}</span><span>{t("blobPosition",{offset,total:query.data?.total??0})}</span><Button size="sm" variant="outline" disabled={offset===0||query.isFetching} onClick={()=>setOffset(Math.max(0,offset-8192))}>{t("previous")}</Button><Button size="sm" variant="outline" disabled={query.isFetching||!query.data||offset+8192>=query.data.total} onClick={()=>setOffset(offset+8192)}>{t("next")}</Button></div>{query.isError?<p role="alert" className="text-destructive">{t("loadError")}</p>:<pre className="max-h-60 overflow-auto whitespace-pre-wrap break-all text-xs">{decoded}</pre>}</div>
}
function RawSide({base,side,value}:{base:string;side:"request"|"response";value:string}) {
  const labels=httpRawLabels(useTranslations("httpRaw"))
  const hashes=React.useMemo(()=>Array.from(new Set(Array.from(value.matchAll(/@blob sha256:([a-f0-9]{64})/g),item=>item[1]))),[value])
  return <section className="min-w-0 space-y-2"><HttpRawMessage side={side} labels={labels} value={trafficRawPreview(value).text} partial={hashes.length>0}/>{hashes.map(hash=><BlobSlice key={hash} base={base} hash={hash}/>)}</section>
}
type FindingOption = { id:string; title:string }
type FindingOptionPage = { items:Array<{id:string;name:string;vulnclass:string;inherited:boolean}>; total:number }
type EvidenceBinding = { id:string; role:string; note:string; position:number; snapshot:{ source_traffic_id:string; url:string; method:string; status:number } }
type EvidenceList = { finding_id:string; version:number; report_version:number; bindings:EvidenceBinding[] }

export function TaskTraffic({taskId,findings}:{taskId:string;findings:FindingOption[]}) {
  const user=useAuth().data?.user?.id
  return <TrafficViewer key={`${user??"anonymous"}:${taskId}`} base={`/aiTasks/${encodeURIComponent(taskId)}/traffic`} taskId={taskId} findings={findings}/>
}
export function ConversationTraffic({conversationId}:{conversationId:string}) {
  const user=useAuth().data?.user?.id
  return <TrafficViewer key={`${user??"anonymous"}:${conversationId}`} base={`/aiConversations/${encodeURIComponent(conversationId)}/traffic`} conversationId={conversationId} findings={[]}/>
}
export function GlobalTraffic() {
  const user=useAuth().data?.user?.id
  return <TrafficViewer key={user??"anonymous"} base="/aiTraffic" findings={[]} global/>
}
function TrafficViewer({base,taskId,conversationId,findings,global=false}:{base:string;taskId?:string;conversationId?:string;findings:FindingOption[];global?:boolean}) {
  const t=useTranslations("aiTasks.traffic")
  const user=useAuth().data?.user?.id
  const [host,setHost]=React.useState("")
  const [hostPicker,setHostPicker]=React.useState(false)
  const [hostSearch,setHostSearch]=React.useState("")
  const [query,setQuery]=React.useState("")
  const [advanced,setAdvanced]=React.useState({method:"",status:"",body:"",path:"",respMin:"",respMax:"",sort:"",order:""})
  const [filters,setFilters]=React.useState({host:"",q:"",...advanced})
  const [page,setPage]=React.useState(0)
  const [selected,setSelected]=React.useState<TrafficItem|null>(null)
  const [checked,setChecked]=React.useState<string[]>([])
  const [bindingOpen,setBindingOpen]=React.useState(false)
  const [finding,setFinding]=React.useState("")
  const [findingSearch,setFindingSearch]=React.useState("")
  const [findingQuery,setFindingQuery]=React.useState("")
  const [role,setRole]=React.useState("proof")
  const [note,setNote]=React.useState("")
  React.useEffect(()=>{setSelected(null);setChecked([]);setBindingOpen(false);setPage(0)},[user,base])
  React.useEffect(()=>{const timer=window.setTimeout(()=>setFindingQuery(findingSearch.trim()),300);return()=>window.clearTimeout(timer)},[findingSearch])
  const detailBase=global&&selected?.source_id?`/${selected.source_type==="task"?"aiTasks":"aiConversations"}/${encodeURIComponent(selected.source_id)}/traffic`:base
  const bind=useMutation({mutationFn:async()=>api.post(`/aiTasks/${encodeURIComponent(taskId ?? "")}/findings/${encodeURIComponent(finding)}/traffic`,{traffic_refs:checked.map(traffic_id=>({traffic_id,role,note:note.trim()}))}),onSuccess:()=>{setChecked([]);setBindingOpen(false);setNote("");toast.success(t("bindSuccess"))},onError:()=>toast.error(t("bindError"))})
  const list=useQuery({queryKey:["traffic",user,base,page,filters],enabled:!!user,gcTime:0,queryFn:async()=>(await api.get<TrafficPage>(base,{params:{...filters,page,size:25}})).data,refetchInterval:page===0?(global?10000:5000):false})
  const hosts=useQuery({queryKey:["traffic-hosts",user],enabled:!!user&&global&&hostPicker,gcTime:0,queryFn:async()=>(await api.get<{hosts:Array<{host:string;count:number}>}>("/aiTraffic/hosts")).data})
  const matchingHosts=React.useMemo(()=>hosts.data?.hosts.filter(item=>item.host.toLowerCase().includes(hostSearch.trim().toLowerCase()))??[],[hosts.data,hostSearch])
  const findingPage=useQuery({queryKey:["traffic-finding-options",user,taskId,findingQuery],enabled:!!user&&!!taskId&&bindingOpen,gcTime:0,queryFn:async()=>(await api.get<FindingOptionPage>(`/aiTasks/${encodeURIComponent(taskId!)}/findings`,{params:{own:1,q:findingQuery||undefined,page:0,size:100}})).data})
  const findingOptions=React.useMemo(()=>findingPage.data?.items?findingPage.data.items.filter(item=>!item.inherited).map(item=>({id:item.id,title:item.name||item.vulnclass||t("finding")})):findings.filter(item=>item.title.toLowerCase().includes(findingQuery.toLowerCase())),[findingPage.data,findings,findingQuery,t])
  React.useEffect(()=>{if(bindingOpen&&!findingOptions.some(item=>item.id===finding))setFinding(findingOptions[0]?.id??"")},[bindingOpen,findingOptions,finding])
  const detail=useQuery({queryKey:["traffic-exchange",user,detailBase,selected?.id],enabled:!!user&&!!selected,gcTime:0,queryFn:async()=>(await api.get<Exchange>(`${detailBase}/exchange`,{params:{id:selected?.id}})).data})
  return <div className="space-y-3">
    {!global && (taskId || conversationId) && <div className="flex justify-end"><AIDataDeleteButton clear disabled={list.isPending||list.isError} request={{kind:"traffic",source:taskId?"task":"conversation",id:taskId??conversationId!,all:true}} onDeleted={()=>{setSelected(null);setChecked([]);setPage(0)}}/></div>}
    {taskId&&<div className="flex items-center justify-between gap-2"><span className="text-sm text-muted-foreground">{t("selected",{count:checked.length})}</span><Button variant="outline" size="sm" disabled={!checked.length} onClick={()=>{setFindingSearch("");setFindingQuery("");setFinding(findings[0]?.id??"");setBindingOpen(true)}}>{t("bind")}</Button></div>}
    <form className="flex flex-wrap items-end gap-2" onSubmit={event=>{event.preventDefault();setPage(0);setFilters({host:host.trim(),q:query.trim(),...advanced,body:advanced.body.trim(),path:advanced.path.trim()})}}><label className="grid gap-1 text-sm">{t("host")}<Input value={host} onChange={event=>setHost(event.target.value)} maxLength={500}/></label>{global&&<Button type="button" variant="outline" onClick={()=>setHostPicker(value=>!value)}>{t("hosts")}</Button>}<label className="grid gap-1 text-sm">{t("search")}<Input value={query} onChange={event=>setQuery(event.target.value)} maxLength={500}/></label><Button type="submit" variant="outline">{t("filter")}</Button>
      {global&&hostPicker&&<div className="w-full space-y-2 rounded border p-2 text-sm"><Input aria-label={t("searchHosts")} placeholder={t("searchHosts")} value={hostSearch} onChange={event=>setHostSearch(event.target.value)} className="max-w-sm"/>{hosts.isError?<p role="alert" className="text-destructive">{t("loadError")}</p>:matchingHosts.length?<><div className="flex max-h-40 flex-wrap gap-2 overflow-auto">{matchingHosts.slice(0,200).map(item=><Button type="button" key={item.host} size="sm" variant="outline" onClick={()=>{setHost(item.host);setPage(0);setFilters({host:item.host,q:query.trim(),...advanced,body:advanced.body.trim(),path:advanced.path.trim()});setHostPicker(false)}}>{item.host} · {item.count}</Button>)}</div>{matchingHosts.length>200&&<p className="text-xs text-muted-foreground">{t("narrowHosts")}</p>}</>:<p className="text-muted-foreground">{hosts.isLoading?t("loading"):t("empty")}</p>}</div>}
      <details className="w-full text-sm"><summary className="cursor-pointer text-muted-foreground">{t("advanced")}</summary><div className="mt-2 flex flex-wrap items-end gap-2">
        <label className="grid gap-1">{t("method")}<select aria-label={t("method")} className="h-9 rounded border bg-background px-2" value={advanced.method} onChange={event=>setAdvanced(value=>({...value,method:event.target.value}))}><option value="">{t("all")}</option>{["GET","POST","PUT","DELETE","PATCH","HEAD","OPTIONS"].map(method=><option key={method}>{method}</option>)}</select></label>
        <label className="grid gap-1">{t("status")}<select aria-label={t("status")} className="h-9 rounded border bg-background px-2" value={advanced.status} onChange={event=>setAdvanced(value=>({...value,status:event.target.value}))}><option value="">{t("all")}</option>{["2xx","3xx","4xx","5xx"].map(status=><option key={status}>{status}</option>)}</select></label>
        <label className="grid gap-1">{t("path")}<Input value={advanced.path} maxLength={500} onChange={event=>setAdvanced(value=>({...value,path:event.target.value}))}/></label>
        <label className="grid gap-1">{t("body")}<Input value={advanced.body} minLength={3} maxLength={500} placeholder={t("bodyHint")} onChange={event=>setAdvanced(value=>({...value,body:event.target.value}))}/></label>
        <label className="grid gap-1">{t("respMin")}<Input className="w-28" type="number" min="0" step="1" value={advanced.respMin} onChange={event=>setAdvanced(value=>({...value,respMin:event.target.value}))}/></label>
        <label className="grid gap-1">{t("respMax")}<Input className="w-28" type="number" min={advanced.respMin||"0"} step="1" value={advanced.respMax} onChange={event=>setAdvanced(value=>({...value,respMax:event.target.value}))}/></label>
        <label className="grid gap-1">{t("sort")}<select aria-label={t("sort")} className="h-9 rounded border bg-background px-2" value={advanced.sort} onChange={event=>setAdvanced(value=>({...value,sort:event.target.value}))}><option value="">{t("time")}</option><option value="status">{t("status")}</option><option value="resp_len">{t("size")}</option></select></label>
        <label className="grid gap-1">{t("order")}<select aria-label={t("order")} className="h-9 rounded border bg-background px-2" value={advanced.order} onChange={event=>setAdvanced(value=>({...value,order:event.target.value}))}><option value="">{t("descending")}</option><option value="asc">{t("ascending")}</option></select></label>
      </div></details>
    </form>
    {list.isError?<p role="alert" className="text-sm text-destructive">{t("loadError")}</p>:<div className="overflow-x-auto rounded border"><table className="w-full text-left text-sm"><thead><tr className="border-b">{taskId&&<th className="p-2">{t("select")}</th>}<th className="p-2">{t("time")}</th>{global&&<th className="p-2">{t("source")}</th>}<th className="p-2">{t("method")}</th><th className="p-2">URL</th><th className="p-2">{t("status")}</th><th className="p-2">{t("size")}</th><th className="p-2"/></tr></thead><tbody>{list.data?.items.map(item=><tr key={`${item.source_type??""}:${item.source_id??""}:${item.id}`} className="border-b last:border-0">{taskId&&<td className="p-2"><input type="checkbox" aria-label={t("selectItem",{url:item.url})} checked={checked.includes(item.id)} onChange={event=>setChecked(current=>event.target.checked?[...current,item.id]:current.filter(id=>id!==item.id))}/></td>}<td className="whitespace-nowrap p-2">{new Date(item.captured_at).toLocaleString()}</td>{global&&<td className="p-2">{item.source_id?<Link className="text-primary underline" href={item.source_type==="task"?`/ai/tasks/${encodeURIComponent(item.source_id)}/`:`/ai/chat/?c=${encodeURIComponent(item.source_id)}`}>{item.source_name||t("source")}</Link>:t("source")}</td>}<td className="p-2">{item.method}</td><td className="max-w-xl break-all p-2">{item.url}</td><td className="p-2">{item.status}</td><td className="p-2">{item.response_length}</td><td className="p-2"><div className="flex gap-2"><Button size="sm" variant="outline" onClick={()=>setSelected(item)}>{t("details")}</Button><AIDataDeleteButton subject={`${item.method} ${item.url}`} request={{kind:"traffic",source:global?(item.source_type??"conversation"):taskId?"task":"conversation",id:global?(item.source_id??""):taskId??conversationId!,ids:[item.id]}} disabled={global&&!item.source_id} onDeleted={()=>{setSelected(null);setChecked(current=>current.filter(id=>id!==item.id));setPage(0)}}/></div></td></tr>)}{!list.isLoading&&!list.data?.items.length&&<tr><td colSpan={(taskId?7:6)+(global?1:0)} className="p-3 text-muted-foreground">{t("empty")}</td></tr>}</tbody></table></div>}
    <div className="flex items-center justify-end gap-2 text-sm"><span>{t("total",{count:list.data?.total??0})} · {t("page",{page:page+1})}</span><Button size="sm" variant="outline" disabled={page===0||list.isFetching} onClick={()=>setPage(value=>value-1)}>{t("previous")}</Button><Button size="sm" variant="outline" disabled={list.isFetching||!list.data||(page+1)*list.data.size>=list.data.total} onClick={()=>setPage(value=>value+1)}>{t("next")}</Button></div>
    <Dialog open={bindingOpen} onOpenChange={setBindingOpen}><DialogContent><DialogHeader><DialogTitle>{t("bind")}</DialogTitle><DialogDescription>{t("selected",{count:checked.length})}</DialogDescription></DialogHeader><form className="space-y-3" onSubmit={event=>{event.preventDefault();if(finding&&checked.length)bind.mutate()}}><label className="grid gap-1 text-sm">{t("findingSearch")}<Input value={findingSearch} onChange={event=>setFindingSearch(event.target.value)} maxLength={500}/></label><label className="grid gap-1 text-sm">{t("finding")}<select aria-label={t("finding")} className="h-9 rounded border bg-background px-2" value={finding} onChange={event=>setFinding(event.target.value)}>{findingOptions.map(item=><option key={item.id} value={item.id}>{item.title}</option>)}</select></label>{findingPage.data&&findingPage.data.total>100&&<p className="text-xs text-muted-foreground">{t("narrowFindings")}</p>}{findingPage.isError&&<p role="alert" className="text-xs text-destructive">{t("loadError")}</p>}<label className="grid gap-1 text-sm">{t("role")}<select aria-label={t("role")} className="h-9 rounded border bg-background px-2" value={role} onChange={event=>setRole(event.target.value)}><option value="proof">{t("proof")}</option><option value="baseline">{t("baseline")}</option><option value="verification">{t("verification")}</option><option value="supporting">{t("supporting")}</option></select></label><label className="grid gap-1 text-sm">{t("note")}<Input value={note} onChange={event=>setNote(event.target.value)} maxLength={4000}/></label><Button type="submit" disabled={bind.isPending||!finding}>{t("confirmBind")}</Button></form></DialogContent></Dialog>
    <Dialog open={!!selected} onOpenChange={open=>{if(!open)setSelected(null)}}><DialogContent className="max-h-[90vh] overflow-auto sm:max-w-4xl"><DialogHeader><DialogTitle>{t("exchange")}</DialogTitle><DialogDescription className="break-all">{selected?.method} {selected?.url}</DialogDescription></DialogHeader>{detail.isError?<p role="alert" className="text-destructive">{t("loadError")}</p>:detail.data?<div className={HTTP_RAW_EXCHANGE_LAYOUT}><RawSide base={detailBase} side="request" value={detail.data.request}/><RawSide base={detailBase} side="response" value={detail.data.response}/></div>:<p>{t("loading")}</p>}</DialogContent></Dialog>
  </div>
}

function EvidenceRow({readOnly=false,item,first,last,busy,onView,onRemove,onSave,onMove}:{readOnly?:boolean;item:EvidenceBinding;first:boolean;last:boolean;busy:boolean;onView:()=>void;onRemove:()=>void;onSave:(role:string,note:string)=>void;onMove:(direction:-1|1)=>void}) {
  const t=useTranslations("aiTasks.traffic")
  const [editing,setEditing]=React.useState(false)
  const [role,setRole]=React.useState(item.role)
  const [note,setNote]=React.useState(item.note)
  React.useEffect(()=>{setRole(item.role);setNote(item.note)},[item.role,item.note])
  return <article className="rounded border p-3 text-sm"><p className="break-all font-medium">{item.snapshot.method} {item.snapshot.url} · {item.snapshot.status}</p><p className="text-muted-foreground">{t(item.role as "proof"|"baseline"|"verification"|"supporting")} {item.note}</p>{!readOnly&&editing&&<div className="mt-2 flex flex-wrap gap-2"><select aria-label={t("role")} className="h-9 rounded border bg-background px-2" value={role} onChange={event=>setRole(event.target.value)}><option value="proof">{t("proof")}</option><option value="baseline">{t("baseline")}</option><option value="verification">{t("verification")}</option><option value="supporting">{t("supporting")}</option></select><Input aria-label={t("note")} className="min-w-48 flex-1" value={note} maxLength={4000} onChange={event=>setNote(event.target.value)}/><Button size="sm" disabled={busy} onClick={()=>{onSave(role,note);setEditing(false)}}>{t("save")}</Button></div>}<div className="mt-2 flex flex-wrap gap-2"><Button size="sm" variant="outline" onClick={onView}>{t("details")}</Button>{!readOnly&&<><Button size="sm" variant="outline" disabled={busy} onClick={()=>setEditing(value=>!value)}>{t("edit")}</Button><Button size="sm" variant="outline" disabled={busy||first} onClick={()=>onMove(-1)}>{t("moveUp")}</Button><Button size="sm" variant="outline" disabled={busy||last} onClick={()=>onMove(1)}>{t("moveDown")}</Button><Button size="sm" variant="outline" disabled={busy} onClick={onRemove}>{t("remove")}</Button></>}</div></article>
}
export function FindingTrafficPanel({taskId,findingId,readOnly=false}:{taskId:string;findingId:string;readOnly?:boolean}) {
  const user=useAuth().data?.user?.id
  return <FindingTrafficPanelInner key={`${user??"anonymous"}:${taskId}:${findingId}`} taskId={taskId} findingId={findingId} readOnly={readOnly}/>
}
function FindingTrafficPanelInner({taskId,findingId,readOnly=false}:{taskId:string;findingId:string;readOnly?:boolean}) {
  const t=useTranslations("aiTasks.traffic")
  const user=useAuth().data?.user?.id
  const cache=useQueryClient()
  const base=`/aiTasks/${encodeURIComponent(taskId)}/findings/${encodeURIComponent(findingId)}/traffic`
  const key=["finding-traffic",user,taskId,findingId]
  const list=useQuery({queryKey:key,enabled:!!user,gcTime:0,queryFn:async()=>(await api.get<EvidenceList>(base)).data})
  const [selected,setSelected]=React.useState<EvidenceBinding|null>(null)
  React.useEffect(()=>{setSelected(null)},[user,taskId,findingId])
  const remove=useMutation({mutationFn:async(id:string)=>api.delete(`${base}/${id}`,{data:{version:list.data?.version}}),onSuccess:async()=>{setSelected(null);await cache.invalidateQueries({queryKey:key});toast.success(t("removeSuccess"))},onError:async()=>{await cache.invalidateQueries({queryKey:key});toast.error(t("editError"))}})
  const edit=useMutation({mutationFn:async(input:{id:string;role:string;note:string})=>api.patch(`${base}/${input.id}`,{version:list.data?.version,role:input.role,note:input.note}),onSuccess:async()=>{await cache.invalidateQueries({queryKey:key});toast.success(t("saveSuccess"))},onError:async()=>{await cache.invalidateQueries({queryKey:key});toast.error(t("editError"))}})
  const reorder=useMutation({mutationFn:async(order:string[])=>api.put(`${base}/order`,{version:list.data?.version,binding_ids:order}),onSuccess:async()=>{await cache.invalidateQueries({queryKey:key})},onError:async()=>{await cache.invalidateQueries({queryKey:key});toast.error(t("editError"))}})
  const move=(index:number,direction:-1|1)=>{const order=list.data?.bindings.map(item=>item.id)??[];const next=index+direction;if(next<0||next>=order.length)return;[order[index],order[next]]=[order[next],order[index]];reorder.mutate(order)}
  return <div className="space-y-3">{list.isError?<p role="alert" className="text-destructive">{t("loadError")}</p>:null}{list.data?.bindings.map((item,index)=><EvidenceRow key={item.id} readOnly={readOnly} item={item} first={index===0} last={index===list.data!.bindings.length-1} busy={remove.isPending||edit.isPending||reorder.isPending} onView={()=>setSelected(item)} onRemove={()=>remove.mutate(item.id)} onSave={(role,note)=>edit.mutate({id:item.id,role,note})} onMove={direction=>move(index,direction)}/>)}{!list.isLoading&&!list.data?.bindings.length&&!list.isError&&<p className="text-sm text-muted-foreground">{t("noEvidence")}</p>}
    {selected&&<div className={HTTP_RAW_EXCHANGE_LAYOUT}>
      <FindingTrafficMessage key={`${selected.id}:request`} base={base} bindingId={selected.id} side="request"/>
      <FindingTrafficMessage key={`${selected.id}:response`} base={base} bindingId={selected.id} side="response"/>
    </div>}
  </div>
}

function FindingTrafficMessage({base,bindingId,side}:{base:string;bindingId:string;side:"request"|"response"}) {
  const t=useTranslations("aiTasks.traffic")
  const labels=httpRawLabels(useTranslations("httpRaw"))
  const user=useAuth().data?.user?.id
  const [offset,setOffset]=React.useState(0)
  const detail=useQuery({queryKey:["finding-traffic-body",user,base,bindingId,side,offset],enabled:!!user,gcTime:0,queryFn:async()=>(await api.get<{head:string;data:string|null;offset:number;total:number}>(`${base}/${bindingId}/body`,{params:{side,offset,length:8192}})).data})
  const body=React.useMemo(()=>{
    try {const bytes=Uint8Array.from(atob(detail.data?.data??""),c=>c.charCodeAt(0));return {text:new TextDecoder().decode(bytes),length:bytes.length,error:false}}
    catch {return {text:"",length:0,error:true}}
  },[detail.data])
  const raw=offset===0?joinHttpHeadBody(detail.data?.head??"",body.text):body.text
  return <section className="min-w-0 space-y-2">
    <HttpRawMessage side={side} labels={labels} value={raw} bodyOnly={offset>0} partial={offset>0||offset+body.length<(detail.data?.total??0)} loading={detail.isPending} error={detail.isError||body.error?t("loadError"):undefined}/>
    <div className="flex flex-wrap items-center gap-2 text-xs"><span>{t("blobPosition",{offset,total:detail.data?.total??0})}</span><Button size="sm" variant="outline" disabled={offset===0||detail.isFetching} onClick={()=>setOffset(Math.max(0,offset-8192))}>{t("previous")}</Button><Button size="sm" variant="outline" disabled={detail.isFetching||!detail.data||offset+8192>=detail.data.total} onClick={()=>setOffset(offset+8192)}>{t("next")}</Button></div>
  </section>
}
