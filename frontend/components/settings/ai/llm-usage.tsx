"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { Bar, BarChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useAIUsageSummary } from "@/hooks/use-ai-usage"

export function LLMUsageOverview() {
  const t = useTranslations("aiUsage")
  const [days, setDays] = React.useState(30)
  const result = useAIUsageSummary(days)
  const daily = React.useMemo(() => {
    const byDay = new Map<string,{date:string;input:number;output:number}>()
    for (const item of result.data?.daily ?? []) {
      const day = byDay.get(item.date) ?? {date:item.date,input:0,output:0}
      day.input += item.input_tokens; day.output += item.output_tokens; byDay.set(item.date,day)
    }
    if (!byDay.size) return []
    const today = new Date(); today.setUTCHours(0,0,0,0)
    return Array.from({length:days},(_,index)=>{const day=new Date(today);day.setUTCDate(today.getUTCDate()-(days-index-1));const date=day.toISOString().slice(0,10);return byDay.get(date)??{date,input:0,output:0}})
  },[result.data,days])
  if (result.isError) return <div role="alert" className="flex items-center gap-2 text-sm text-destructive">{t("loadError")}<Button size="sm" variant="outline" onClick={()=>result.refetch()}>{t("retry")}</Button></div>
  if (!result.data) return <p className="text-sm text-muted-foreground">{t("loading")}</p>
  const total = result.data.totals
  return <div className="space-y-4">
    <p className="text-sm text-muted-foreground">{t("description")}</p>
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">{([["calls",total.calls],["input",total.input_tokens],["output",total.output_tokens],["cacheRead",total.cache_read_tokens]] as const).map(([key,value])=><Card key={key} className="py-3"><CardContent className="px-4"><p className="text-xs text-muted-foreground">{t(key)}</p><p className="text-xl font-semibold tabular-nums">{value.toLocaleString()}</p></CardContent></Card>)}</div>
    <Card><CardHeader className="flex flex-row items-center justify-between gap-2"><div><CardTitle>{t("daily")}</CardTitle><CardDescription>{t("utc")}</CardDescription></div><select aria-label={t("period")} className="h-9 rounded border bg-background px-2 text-sm" value={days} onChange={event=>setDays(Number(event.target.value))}><option value={7}>{t("days",{count:7})}</option><option value={30}>{t("days",{count:30})}</option><option value={90}>{t("days",{count:90})}</option></select></CardHeader><CardContent>{daily.length?<div className="h-64 w-full" aria-label={t("daily")}><ResponsiveContainer width="100%" height="100%"><BarChart data={daily}><CartesianGrid strokeDasharray="3 3" vertical={false}/><XAxis dataKey="date" tickFormatter={value=>String(value).slice(5)} tick={{fontSize:11}}/><YAxis tick={{fontSize:11}}/><Tooltip/><Legend/><Bar dataKey="input" name={t("input")} stackId="tokens" fill="var(--chart-1)"/><Bar dataKey="output" name={t("output")} stackId="tokens" fill="var(--chart-2)"/></BarChart></ResponsiveContainer></div>:<p className="text-sm text-muted-foreground">{t("empty")}</p>}</CardContent></Card>
    <Card><CardHeader><CardTitle>{t("profiles")}</CardTitle></CardHeader><CardContent><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>{t("profile")}</TableHead><TableHead>{t("calls")}</TableHead><TableHead>{t("input")}</TableHead><TableHead>{t("output")}</TableHead><TableHead>{t("cacheRead")}</TableHead><TableHead>{t("cacheWrite")}</TableHead></TableRow></TableHeader><TableBody>{result.data.profiles.map((item,index)=><TableRow key={`${item.profile_id}:${item.profile_name}:${index}`}><TableCell>{item.profile_name||t("environment")}</TableCell><TableCell>{item.calls.toLocaleString()}</TableCell><TableCell>{item.input_tokens.toLocaleString()}</TableCell><TableCell>{item.output_tokens.toLocaleString()}</TableCell><TableCell>{item.cache_read_tokens.toLocaleString()}</TableCell><TableCell>{item.cache_write_tokens.toLocaleString()}</TableCell></TableRow>)}{!result.data.profiles.length&&<TableRow><TableCell colSpan={6}>{t("empty")}</TableCell></TableRow>}</TableBody></Table></div></CardContent></Card>
  </div>
}
