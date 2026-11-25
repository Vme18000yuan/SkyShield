"use client"

import * as React from "react"
import { useLocale, useTranslations } from "next-intl"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { ContentHandoff } from "@/components/shared/loading/content-handoff"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useAIToolExecution, useAIToolExecutions } from "@/hooks/use-ai-tool-executions"
import { normalizeError } from "@/lib/errors/normalize-error"
import { textRole } from "@/lib/typography"
import type { AIToolExecutionSource } from "@/types/ai-tool-execution.types"
import { AIDataDeleteButton } from "./ai-data-delete-button"

export function AIToolExecutionList() {
  const t = useTranslations("aiTools")
  const deletionText = useTranslations("aiDataDeletion")
  const locale = useLocale()
  const [search, setSearch] = React.useState("")
  const [query, setQuery] = React.useState("")
  const [cursors, setCursors] = React.useState<string[]>([""])
  const [page, setPage] = React.useState(0)
  const [selected, setSelected] = React.useState<string | null>(null)
  const [source, setSource] = React.useState<AIToolExecutionSource>("conversation")
  const [checked, setChecked] = React.useState<string[]>([])
  const list = useAIToolExecutions(source, query, cursors[page])
  const detail = useAIToolExecution(source, selected)
  React.useEffect(() => { setChecked([]) }, [source, page, query])
  const selectable = list.data?.executions.filter(item => item.status !== "running").map(item => item.id) ?? []
  React.useEffect(() => {
    const timer = window.setTimeout(() => { setQuery(search.trim()); setCursors([""]); setPage(0) }, 300)
    return () => window.clearTimeout(timer)
  }, [search])
  if (list.error) return <AppErrorState error={normalizeError(list.error, { notFoundKind: "unexpected-error" })} onRetry={list.refetch} />

  return <div className="flex min-w-0 flex-col gap-3">
    <Tabs value={source} onValueChange={value => { if (value === "conversation" || value === "task") { setSource(value); setCursors([""]); setPage(0); setSelected(null) } }}>
      <TabsList variant="filter"><TabsTrigger value="conversation">{t("chatSource")}</TabsTrigger><TabsTrigger value="task">{t("taskSource")}</TabsTrigger></TabsList>
    </Tabs>
    <div className="flex flex-wrap items-center gap-2"><Input value={search} onChange={event => setSearch(event.target.value)} placeholder={t("search")} aria-label={t("search")} className="max-w-sm" /><AIDataDeleteButton label={deletionText("deleteSelected", { count: checked.length })} disabled={!checked.length} request={{ kind: "executions", source, ids: checked }} onDeleted={() => { setChecked([]); setSelected(null); setCursors([""]); setPage(0) }} /></div>
    <ContentHandoff owner="ai-tool-execution-content" layer="workspace" isLoading={list.isPending}
      skeleton={<div className="flex flex-col gap-2" aria-label={t("loading")}>{[0, 1, 2, 3].map(row => <Skeleton key={row} className="h-10 w-full" />)}</div>}>
      <Table><TableHeader><TableRow><TableHead><Checkbox aria-label={deletionText("selectPage")} checked={selectable.length > 0 && selectable.every(id => checked.includes(id))} onCheckedChange={value => setChecked(value ? selectable : [])} /></TableHead><TableHead>{t("tool")}</TableHead><TableHead>{source === "task" ? t("taskSource") : t("conversation")}</TableHead><TableHead>{t("status")}</TableHead><TableHead>{t("time")}</TableHead><TableHead /></TableRow></TableHeader>
        <TableBody>
          {list.data?.executions.map(item => <TableRow key={item.id}>
            <TableCell><Checkbox aria-label={deletionText("selectRecord", { id: item.id })} checked={checked.includes(item.id)} disabled={item.status === "running"} onCheckedChange={value => setChecked(previous => value ? [...previous, item.id] : previous.filter(id => id !== item.id))} /></TableCell>
            <TableCell>{item.tool}</TableCell><TableCell>{item.conversation}</TableCell><TableCell>{t(item.status)}</TableCell>
            <TableCell>{new Date(item.createdAt).toLocaleString(locale)}</TableCell>
            <TableCell><div className="flex gap-2"><Button variant="outline" size="sm" onClick={() => setSelected(item.id)}>{t("detail")}</Button><AIDataDeleteButton request={{ kind: "executions", source, ids: [item.id] }} disabled={item.status === "running"} onDeleted={() => { setSelected(null); setCursors([""]); setPage(0) }} /></div></TableCell>
          </TableRow>)}
          {!list.data?.executions.length && <TableRow><TableCell colSpan={6}>{t("empty")}</TableCell></TableRow>}
        </TableBody>
      </Table>
    </ContentHandoff>
    <div className="flex items-center justify-end gap-2">
      <span className={textRole.helperText}>{t("page", { number: page + 1 })}</span>
      <Button variant="outline" size="sm" disabled={page === 0} onClick={() => setPage(value => value - 1)}>{t("previous")}</Button>
      <Button variant="outline" size="sm" disabled={!list.data?.nextBefore} onClick={() => {
        const next = list.data?.nextBefore
        if (next) { setCursors(existing => [...existing.slice(0, page + 1), next]); setPage(value => value + 1) }
      }}>{t("next")}</Button>
    </div>
    <Dialog open={Boolean(selected)} onOpenChange={value => { if (!value) setSelected(null) }}>
      <DialogContent><DialogHeader><DialogTitle>{detail.data?.tool || t("detail")}</DialogTitle><DialogDescription>{detail.data?.conversation}</DialogDescription></DialogHeader>
        {detail.error ? <AppErrorState error={normalizeError(detail.error, { notFoundKind: "unexpected-error" })} onRetry={detail.refetch} /> : detail.isPending ? <Skeleton className="h-32 w-full" /> : <div className="flex min-w-0 flex-col gap-3">
          <div><h3 className={textRole.sectionTitle}>{t("input")}</h3><pre className="overflow-auto whitespace-pre-wrap break-all">{detail.data?.input}</pre></div>
          <div><h3 className={textRole.sectionTitle}>{t("output")}</h3><pre className="overflow-auto whitespace-pre-wrap break-all">{detail.data?.output || "—"}</pre></div>
        </div>}
      </DialogContent>
    </Dialog>
  </div>
}
