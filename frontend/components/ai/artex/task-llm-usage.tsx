"use client"

import { useQuery } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useAuth } from "@/hooks/use-auth"
import { api } from "@/lib/api-client"
import type { AIUsageModel } from "@/types/ai-usage.types"

export function TaskLLMUsage({taskId,running}:{taskId:string;running:boolean}) {
  const t=useTranslations("aiUsage")
  const user=useAuth().data?.user?.id
  const result=useQuery({queryKey:["ai-task-model-usage",user,taskId],enabled:!!user,queryFn:async()=>(await api.get<{models:AIUsageModel[]}>(`/aiModelUsage/tasks/${encodeURIComponent(taskId)}`)).data,refetchInterval:running?5000:false})
  const models=result.data?.models??[]
  return <section className="mt-3 space-y-2 rounded-md border bg-card p-3"><h3 className="text-sm font-semibold">{t("taskModels")}</h3><p className="text-xs text-muted-foreground">{t("description")}</p>{result.isError?<p role="alert" className="text-sm text-destructive">{t("loadError")} <Button size="sm" variant="outline" onClick={()=>result.refetch()}>{t("retry")}</Button></p>:!result.data?<p className="text-sm text-muted-foreground">{t("loading")}</p>:<div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>{t("model")}</TableHead><TableHead>{t("calls")}</TableHead><TableHead>{t("input")}</TableHead><TableHead>{t("output")}</TableHead><TableHead>{t("cacheRead")}</TableHead><TableHead>{t("cacheWrite")}</TableHead></TableRow></TableHeader><TableBody>{models.map(item=><TableRow key={item.model}><TableCell>{item.model}</TableCell><TableCell>{item.calls.toLocaleString()}</TableCell><TableCell>{item.input_tokens.toLocaleString()}</TableCell><TableCell>{item.output_tokens.toLocaleString()}</TableCell><TableCell>{item.cache_read_tokens.toLocaleString()}</TableCell><TableCell>{item.cache_write_tokens.toLocaleString()}</TableCell></TableRow>)}{!models.length&&<TableRow><TableCell colSpan={6}>{t("empty")}</TableCell></TableRow>}</TableBody></Table></div>}</section>
}
