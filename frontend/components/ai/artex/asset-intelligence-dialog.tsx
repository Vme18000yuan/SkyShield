// Adapted from ARTEX's asset intelligence view, AGPL-3.0-only.
"use client"

import * as React from "react"
import Link from "next/link"
import { useQuery } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { useAuth } from "@/hooks/use-auth"
import { api } from "@/lib/api-client"
import type { CredentialAnalysis, CredentialTrafficDetail } from "@/types/artex-ai.types"

type Item = {id:string;task_id:string;task_name?:string;kind:"credential"|"lead"|"sensitive_data";title:string;content:string;evidence:string;confidence:string;status:string;credentials?:CredentialAnalysis}
type Page = {items:Item[];total:number;enabled:boolean}
export function AssetIntelligenceDialog({asset,open,onOpenChange}:{asset:{id:string;label:string}|null;open:boolean;onOpenChange:(open:boolean)=>void}) {
  const t=useTranslations("aiTasks.intelligence")
  const a=useTranslations("assetInventory")
  const user=useAuth().data?.user?.id
  const [kind,setKind]=React.useState("")
  const [draft,setDraft]=React.useState("")
  const [query,setQuery]=React.useState("")
  const [page,setPage]=React.useState(0)
  const [selected,setSelected]=React.useState<{intel:string;id:string}|null>(null)
  React.useEffect(()=>{setKind("");setDraft("");setQuery("");setPage(0);setSelected(null)},[asset?.id,user])
  React.useEffect(()=>{const timer=window.setTimeout(()=>{setQuery(draft.trim());setPage(0)},300);return()=>window.clearTimeout(timer)},[draft])
  const base=`/aiAssets/${encodeURIComponent(asset?.id??"")}/intelligence`
  const list=useQuery({queryKey:["asset-intelligence",user,asset?.id,kind,query,page],enabled:!!user&&open&&!!asset,gcTime:0,queryFn:async()=>(await api.get<Page>(base,{params:{kind:kind||undefined,q:query||undefined,limit:25,offset:page*25}})).data})
  const detail=useQuery({queryKey:["asset-credential-traffic",user,asset?.id,selected?.intel,selected?.id],enabled:!!user&&open&&!!asset&&!!selected,gcTime:0,queryFn:async()=>(await api.get<CredentialTrafficDetail>(`${base}/${encodeURIComponent(selected!.intel)}/traffic/${encodeURIComponent(selected!.id)}`)).data})
  const labels={credential:t("credential"),lead:t("lead"),sensitive_data:t("sensitiveData")}
  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent className="max-h-[90vh] overflow-auto sm:max-w-4xl"><DialogHeader><DialogTitle>{a("intelligence")}</DialogTitle><DialogDescription className="break-all">{asset?.label}</DialogDescription></DialogHeader>
    {list.data&&!list.data.enabled&&<p className="text-sm text-muted-foreground">{t("disabled")}</p>}
    <div className="flex flex-wrap gap-2"><select aria-label={t("kindFilter")} className="h-9 rounded border bg-background px-2 text-sm" value={kind} onChange={event=>{setKind(event.target.value);setPage(0)}}><option value="">{t("allKinds")}</option>{Object.entries(labels).map(([key,label])=><option key={key} value={key}>{label}</option>)}</select><Input className="max-w-sm" value={draft} onChange={event=>setDraft(event.target.value)} aria-label={t("search")} placeholder={t("search")}/></div>
    {list.isError?<p role="alert" className="text-destructive">{t("loadError")}</p>:list.data?.items.map(item=><article key={item.id} className="rounded border p-3 text-sm"><div className="flex flex-wrap items-center gap-2"><span className="rounded bg-muted px-2 py-0.5 text-xs">{labels[item.kind]}</span><strong>{item.title}</strong><span className="text-xs text-muted-foreground">{t(`status.${item.status}`)}</span></div><p className="mt-1 text-xs text-muted-foreground">{a("sourceTask")}: <Link className="text-primary underline" href={`/ai/tasks/${encodeURIComponent(item.task_id)}/`}>{item.task_name||a("sourceTask")}</Link></p><details className="mt-2"><summary className="cursor-pointer text-muted-foreground">{t("viewDetails")}</summary><div className="mt-2 grid gap-2"><div><strong className="text-xs">{t("content")}</strong><pre className="whitespace-pre-wrap break-all rounded bg-muted p-2 text-xs">{item.content}</pre></div><div><strong className="text-xs">{t("evidence")}</strong><pre className="whitespace-pre-wrap break-all rounded bg-muted p-2 text-xs">{item.evidence}</pre></div>{item.credentials?.accounts?.map((account,index)=><pre key={index} className="whitespace-pre-wrap break-all rounded bg-muted p-2 text-xs">{JSON.stringify(account,null,2)}</pre>)}{item.credentials?.traffic?.map(ref=><div className="rounded border p-2 text-xs" key={ref.id}><p className="break-all">{ref.method} {ref.url} · {ref.status}</p><pre className="whitespace-pre-wrap break-all">{ref.excerpt}</pre><Button size="sm" variant="outline" onClick={()=>setSelected({intel:item.id,id:ref.id})}>{t("viewTraffic")}</Button></div>)}</div></details></article>)}
    {list.data&&!list.data.items.length&&!list.isError&&<p className="text-sm text-muted-foreground">{t("empty")}</p>}
    <div className="flex items-center justify-end gap-2 text-sm text-muted-foreground"><span>{t("total",{count:list.data?.total??0})}</span><span>{t("page",{number:page+1})}</span><Button size="sm" variant="outline" disabled={page===0||list.isFetching} onClick={()=>setPage(value=>value-1)}>{t("previous")}</Button><Button size="sm" variant="outline" disabled={list.isFetching||(page+1)*25>=(list.data?.total??0)} onClick={()=>setPage(value=>value+1)}>{t("next")}</Button></div>
    {selected&&<section className="space-y-2 rounded border p-3"><div className="flex items-center justify-between"><strong>{t("traffic")}</strong><Button size="sm" variant="ghost" onClick={()=>setSelected(null)}>{a("closeTraffic")}</Button></div>{detail.isError?<p role="alert" className="text-destructive">{t("loadError")}</p>:detail.data?.unavailable?<p className="text-sm text-muted-foreground">{t("trafficUnavailable")}</p>:detail.data?<><p className="break-all text-xs">{detail.data.reference.url}</p><strong className="text-xs">{t("trafficRequest")}</strong><pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all rounded bg-muted p-2 text-xs">{detail.data.req}</pre><strong className="text-xs">{t("trafficResponse")}</strong><pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all rounded bg-muted p-2 text-xs">{detail.data.resp}</pre></>:<p>{t("loadingTraffic")}</p>}</section>}
  </DialogContent></Dialog>
}
