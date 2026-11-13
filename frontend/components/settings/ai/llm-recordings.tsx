"use client"

import * as React from "react"
import Link from "next/link"
import axios from "axios"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { AlertDialog, AlertDialogClose, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useAuth } from "@/hooks/use-auth"
import { api } from "@/lib/api-client"
import type { AILLMRecord, AILLMRecordDetail, AILLMRecordPage, AILLMRecordTasks } from "@/types/ai-usage.types"

const CHUNK_SIZE=65536
function bodyChunk(text:string,page:number) {
  let start=page*CHUNK_SIZE, end=Math.min(text.length,(page+1)*CHUNK_SIZE)
  const high=(value:number)=>value>=0xD800&&value<=0xDBFF
  const low=(value:number)=>value>=0xDC00&&value<=0xDFFF
  if(start>0&&low(text.charCodeAt(start))&&high(text.charCodeAt(start-1)))start++
  if(end<text.length&&high(text.charCodeAt(end-1))&&low(text.charCodeAt(end)))end++
  return text.slice(start,end)
}
function Body({title,text}:{title:string;text:string}) {
  const t=useTranslations("aiRecording")
  const [page,setPage]=React.useState(0)
  const pages=Math.max(1,Math.ceil(text.length/CHUNK_SIZE))
  React.useEffect(()=>setPage(0),[text])
  return <section className="space-y-2"><div className="flex flex-wrap items-center justify-between gap-2"><h3 className="text-sm font-semibold">{title}</h3><div className="flex items-center gap-2 text-xs text-muted-foreground"><span>{t("part",{page:page+1,total:pages})}</span><Button variant="outline" size="sm" disabled={page===0} onClick={()=>setPage(value=>value-1)}>{t("previous")}</Button><Button variant="outline" size="sm" disabled={page+1>=pages} onClick={()=>setPage(value=>value+1)}>{t("next")}</Button></div></div><pre className="max-h-80 overflow-auto whitespace-pre-wrap break-all rounded bg-muted p-3 text-xs">{bodyChunk(text,page)||t("noBody")}</pre></section>
}

export function LLMRecordings() {
  const user=useAuth().data?.user?.id
  return <LLMRecordingsInner key={user??"anonymous"}/>
}
function LLMRecordingsInner() {
  const t=useTranslations("aiRecording")
  const user=useAuth().data?.user?.id
  const cache=useQueryClient()
  const [taskSearch,setTaskSearch]=React.useState("")
  const [taskQuery,setTaskQuery]=React.useState("")
  const [task,setTask]=React.useState("")
  const [chosenTask,setChosenTask]=React.useState<{id:string;name:string;count:number}|null>(null)
  const [model,setModel]=React.useState("")
  const [session,setSession]=React.useState("")
  const [filters,setFilters]=React.useState({model:"",session:""})
  const [page,setPage]=React.useState(0)
  const [selected,setSelected]=React.useState<AILLMRecord|null>(null)
  const [raw,setRaw]=React.useState(true)
  const [deleteOpen,setDeleteOpen]=React.useState(false)
  React.useEffect(()=>{const timer=window.setTimeout(()=>setTaskQuery(taskSearch.trim()),300);return()=>window.clearTimeout(timer)},[taskSearch])
  const settings=useQuery({queryKey:["ai-recording-settings",user],enabled:!!user,queryFn:async()=>(await api.get<{llm_record:boolean}>("/aiRuntimeSettings")).data})
  const tasks=useQuery({queryKey:["ai-recording-tasks",user,taskQuery],enabled:!!user,queryFn:async()=>(await api.get<AILLMRecordTasks>("/aiLlmRecordTasks",{params:{q:taskQuery||undefined}})).data})
  const list=useQuery({queryKey:["ai-recordings",user,task,page,filters],enabled:!!user,queryFn:async()=>(await api.get<AILLMRecordPage>("/aiLlmRecords",{params:{...filters,task:task||undefined,page,size:25}})).data,refetchInterval:5000})
  const detail=useQuery({queryKey:["ai-recording-detail",user,selected?.id],enabled:!!user&&!!selected,queryFn:async()=>(await api.get<AILLMRecordDetail>(`/aiLlmRecords/${encodeURIComponent(selected!.id)}`)).data,gcTime:0,refetchOnWindowFocus:false})
  const toggle=useMutation({mutationFn:async(value:boolean)=>(await api.patch<{llm_record:boolean}>("/aiRuntimeSettings",{llm_record:value})).data,onSuccess:data=>{cache.setQueryData(["ai-recording-settings",user],data);toast.success(t("saved"))},onError:error=>{const details=axios.isAxiosError(error)?error.response?.data?.error?.details:undefined;toast.error(Array.isArray(details)&&details.some(item=>item?.reason==="AI_ENCRYPTION_KEY_REQUIRED")?t("keyMissing"):t("saveError"))}})
  const remove=useMutation({mutationFn:async()=>(await api.delete<{deleted:number}>("/aiLlmRecords",{params:{task}})).data,onSuccess:async()=>{setDeleteOpen(false);setSelected(null);setTask("");setChosenTask(null);setPage(0);await Promise.all([cache.invalidateQueries({queryKey:["ai-recordings",user]}),cache.invalidateQueries({queryKey:["ai-recording-tasks",user]})]);toast.success(t("deleted"))},onError:()=>toast.error(t("deleteError"))})
  const taskOptions=React.useMemo(()=>{const items=tasks.data?.items??[];return chosenTask&&!items.some(item=>item.id===chosenTask.id)?[chosenTask,...items]:items},[tasks.data,chosenTask])
  const hasRaw=!!(detail.data?.payload.request_body||detail.data?.payload.response_body)
  const showRaw=raw&&hasRaw
  return <div className="space-y-4">
    <Card><CardHeader><CardTitle>{t("title")}</CardTitle><CardDescription>{t("description")}</CardDescription></CardHeader><CardContent className="flex items-center justify-between gap-3"><label htmlFor="llm-recording-toggle" className="text-sm">{t("enable")}</label><Switch id="llm-recording-toggle" checked={!!settings.data?.llm_record} disabled={!settings.data||toggle.isPending} onCheckedChange={value=>toggle.mutate(value)}/></CardContent></Card>
    {settings.isError&&<p role="alert" className="text-sm text-destructive">{t("settingsError")}</p>}
    <div className="flex flex-wrap items-end gap-2"><label className="grid gap-1 text-sm">{t("searchTask")}<Input value={taskSearch} maxLength={500} onChange={event=>setTaskSearch(event.target.value)}/></label><label className="grid gap-1 text-sm">{t("task")}<select aria-label={t("task")} className="h-9 min-w-48 rounded border bg-background px-2" value={task} onChange={event=>{setTask(event.target.value);setChosenTask(taskOptions.find(item=>item.id===event.target.value)??null);setPage(0)}}><option value="">{t("allTasks")}</option>{taskOptions.map(item=><option key={item.id} value={item.id}>{item.name} ({item.count})</option>)}</select></label><Button variant="outline" disabled={!task||remove.isPending} onClick={()=>setDeleteOpen(true)}>{t("deleteTask")}</Button></div>
    {tasks.data?.has_more&&<p className="text-xs text-muted-foreground">{t("narrowTasks")}</p>}
    {tasks.isError&&<p role="alert" className="text-sm text-destructive">{t("loadError")}</p>}
    <form className="flex flex-wrap items-end gap-2" onSubmit={event=>{event.preventDefault();setPage(0);setFilters({model:model.trim(),session:session.trim()})}}><label className="grid gap-1 text-sm">{t("model")}<Input value={model} maxLength={500} onChange={event=>setModel(event.target.value)}/></label><label className="grid gap-1 text-sm">{t("session")}<Input value={session} maxLength={500} onChange={event=>setSession(event.target.value)}/></label><Button type="submit" variant="outline">{t("filter")}</Button></form>
    {list.isError?<p role="alert" className="text-sm text-destructive">{t("loadError")}</p>:<div className="overflow-x-auto rounded border"><Table><TableHeader><TableRow><TableHead>{t("time")}</TableHead><TableHead>{t("task")}</TableHead><TableHead>{t("model")}</TableHead><TableHead>Agent</TableHead><TableHead>{t("status")}</TableHead><TableHead>{t("tokens")}</TableHead><TableHead/></TableRow></TableHeader><TableBody>{list.data?.items.map(item=><TableRow key={item.id}><TableCell className="whitespace-nowrap">{new Date(item.created_at).toLocaleString()}</TableCell><TableCell>{item.task_id?<Link className="text-primary underline" href={`/ai/tasks/${encodeURIComponent(item.task_id)}/`}>{item.task_name||t("task")}</Link>:t("conversation")}</TableCell><TableCell>{item.model}</TableCell><TableCell>{item.worker||"—"}</TableCell><TableCell>{t(`state.${item.status}`)}</TableCell><TableCell>{(item.input_tokens+item.output_tokens).toLocaleString()}</TableCell><TableCell><Button size="sm" variant="outline" onClick={()=>{setRaw(true);setSelected(item)}}>{t("details")}</Button></TableCell></TableRow>)}{!list.isLoading&&!list.data?.items.length&&<TableRow><TableCell colSpan={7}>{t("empty")}</TableCell></TableRow>}</TableBody></Table></div>}
    <div className="flex items-center justify-end gap-2 text-sm"><span>{t("total",{count:list.data?.total??0})} · {t("page",{page:page+1})}</span><Button size="sm" variant="outline" disabled={page===0||list.isFetching} onClick={()=>setPage(value=>value-1)}>{t("previous")}</Button><Button size="sm" variant="outline" disabled={list.isFetching||!list.data||(page+1)*list.data.size>=list.data.total} onClick={()=>setPage(value=>value+1)}>{t("next")}</Button></div>
    <Dialog open={!!selected} onOpenChange={open=>{if(!open)setSelected(null)}}><DialogContent className="max-h-[90vh] overflow-auto sm:max-w-5xl"><DialogHeader><DialogTitle>{t("details")}</DialogTitle><DialogDescription>{selected?.model} · {selected?.task_name||selected?.session_id}</DialogDescription></DialogHeader>{detail.isError?<p role="alert" className="text-destructive">{t("loadError")}</p>:detail.data?<div className="space-y-3">{detail.data.truncated&&<p className="text-sm text-muted-foreground">{t("truncated")}</p>}<div className="flex gap-2"><Button size="sm" disabled={!hasRaw} variant={showRaw?"default":"outline"} onClick={()=>setRaw(true)}>{t("raw")}</Button><Button size="sm" variant={!showRaw?"default":"outline"} onClick={()=>setRaw(false)}>{t("normalized")}</Button></div><Body title={t("request")} text={showRaw?detail.data.payload.request_body:detail.data.payload.normalized_request}/><Body title={t("response")} text={showRaw?detail.data.payload.response_body:detail.data.payload.normalized_response}/></div>:<p>{t("loading")}</p>}</DialogContent></Dialog>
    <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{t("deleteTask")}</AlertDialogTitle><AlertDialogDescription>{t("deleteConfirm",{name:chosenTask?.name||t("task")})}</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogClose variant="outline">{t("cancel")}</AlertDialogClose><Button variant="destructive" disabled={remove.isPending} onClick={()=>remove.mutate()}>{t("confirmDelete")}</Button></AlertDialogFooter></AlertDialogContent></AlertDialog>
  </div>
}
