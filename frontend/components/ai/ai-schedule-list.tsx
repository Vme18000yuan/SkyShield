"use client"

import * as React from "react"
import Link from "next/link"
import { useLocale, useTranslations } from "next-intl"
import { toast } from "sonner"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { ContentHandoff } from "@/components/shared/loading/content-handoff"
import { AlertDialog, AlertDialogClose, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useAISchedule, useAISchedules } from "@/hooks/use-ai-schedules"
import { normalizeError } from "@/lib/errors/normalize-error"
import type { AISchedule } from "@/types/ai-schedule.types"
import type { AIScheduleAction } from "@/types/ai-schedule.types"
import { AIScheduleDialog } from "./ai-schedule-dialog"

function nextBoundary(item: AISchedule): string | null {
  const values = [item.nextStart, item.nextEnd].filter((value): value is string => Boolean(value))
  return values.sort((a, b) => new Date(a).getTime() - new Date(b).getTime())[0] ?? null
}

export function AIScheduleList() {
  const t = useTranslations("aiSchedules")
  const locale = useLocale()
  const { list, remove, action } = useAISchedules()
  const [editOpen, setEditOpen] = React.useState(false)
  const [editing, setEditing] = React.useState<AISchedule | null>(null)
  const [pendingDelete, setPendingDelete] = React.useState<AISchedule | null>(null)
  const [detailId, setDetailId] = React.useState<string | null>(null)
  const detail = useAISchedule(detailId)
  const perform = async (item: AISchedule, operation: AIScheduleAction) => {
    try {
      const result = await action.mutateAsync({ id: item.id, action: operation })
      if (operation === "run" && result.errors?.length) toast.warning(t("runPartial", { count: result.started ?? 0 }), { description: result.errors.join("；") })
      else toast.success(t("actionSuccess"))
    }
    catch { toast.error(t("actionError")) }
  }
  if (list.error) return <AppErrorState error={normalizeError(list.error, { notFoundKind: "unexpected-error" })} onRetry={list.refetch} />
  return <div className="flex min-w-0 flex-col gap-3">
    <div className="flex justify-end"><Button onClick={() => { setEditing(null); setEditOpen(true) }}>{t("create")}</Button></div>
    <ContentHandoff owner="ai-schedule-list-content" layer="workspace" isLoading={list.isPending}
      skeleton={<div className="flex flex-col gap-2" aria-label={t("loading")}>{[0, 1, 2].map(row => <Skeleton key={row} className="h-10 w-full" />)}</div>}>
      <Table><TableHeader><TableRow><TableHead>{t("name")}</TableHead><TableHead>{t("rule")}</TableHead><TableHead>{t("status")}</TableHead><TableHead>{t("nextBoundary")}</TableHead><TableHead /></TableRow></TableHeader>
        <TableBody>{list.data?.map(item => <TableRow key={item.id}><TableCell>{item.displayName}</TableCell>
          <TableCell>{t(item.scheduleType)} · {item.startTime.slice(0, 5)}–{item.endTime ? item.endTime.slice(0, 5) : t("unbounded")}</TableCell>
          <TableCell><Badge variant={item.status === "running" ? "success" : item.status === "error" ? "error" : "outline"}>{t(item.status)}</Badge></TableCell>
          <TableCell>{nextBoundary(item) ? new Date(nextBoundary(item)!).toLocaleString(locale) : "—"}</TableCell>
          <TableCell><div className="flex flex-wrap justify-end gap-2">
            <Button variant="outline" size="sm" onClick={() => setDetailId(item.id)}>{t("history")}</Button>
            <Button variant="outline" size="sm" disabled={action.isPending} onClick={() => void perform(item, "run")}>{t("runNow")}</Button>
            <Button variant="outline" size="sm" disabled={action.isPending} onClick={() => void perform(item, item.enabled ? "disable" : "enable")}>{item.enabled ? t("disable") : t("enable")}</Button>
            <Button variant="outline" size="sm" onClick={() => { setEditing(item); setEditOpen(true) }}>{t("edit")}</Button>
            <Button variant="outline" size="sm" onClick={() => setPendingDelete(item)}>{t("delete")}</Button>
          </div></TableCell></TableRow>)}{!list.data?.length && <TableRow><TableCell colSpan={5}>{t("empty")}</TableCell></TableRow>}</TableBody>
      </Table>
    </ContentHandoff>
    <AIScheduleDialog open={editOpen} onOpenChange={setEditOpen} schedule={editing} />
    <AlertDialog open={Boolean(pendingDelete)} onOpenChange={value => { if (!value) setPendingDelete(null) }}><AlertDialogContent>
      <AlertDialogHeader><AlertDialogTitle>{t("deleteTitle")}</AlertDialogTitle><AlertDialogDescription>{t("deleteDescription")}</AlertDialogDescription></AlertDialogHeader>
      <AlertDialogFooter><AlertDialogClose variant="outline">{t("cancel")}</AlertDialogClose><Button variant="destructive" disabled={remove.isPending} onClick={async () => {
        if (!pendingDelete) return
        try { await remove.mutateAsync(pendingDelete.id); setPendingDelete(null); toast.success(t("deleted")) } catch { toast.error(t("actionError")) }
      }}>{t("delete")}</Button></AlertDialogFooter>
    </AlertDialogContent></AlertDialog>
    <Dialog open={Boolean(detailId)} onOpenChange={value => { if (!value) setDetailId(null) }}><DialogContent>
      <DialogHeader><DialogTitle>{detail.data?.displayName || t("history")}</DialogTitle><DialogDescription>{t("historyDescription")}</DialogDescription></DialogHeader>
      {detail.error ? <AppErrorState error={normalizeError(detail.error)} onRetry={detail.refetch} /> : detail.isPending ? <Skeleton className="h-32 w-full" /> : <div className="flex max-h-150 flex-col gap-2 overflow-auto text-sm">
        <div>{t("boundTasks", { count: detail.data?.taskIds.length ?? 0 })}: {detail.data?.taskIds.map(id => <Button key={id} size="sm" variant="link" render={<Link href={`/ai/tasks/${id}/`} />}>#{id}</Button>)}</div>
        {detail.data?.lastError && <p className="text-destructive">{detail.data.lastError}</p>}
        {detail.data?.history.map(entry => <div key={entry.id} className="rounded-md border p-2"><div className="flex flex-wrap gap-2"><Badge variant={entry.success ? "outline" : "error"}>{entry.action}</Badge>{entry.taskId && <span>#{entry.taskId}</span>}<span className="text-muted-foreground">{new Date(entry.createdAt).toLocaleString(locale)}</span></div>{entry.message && <p className="mt-1">{entry.message}</p>}</div>)}
        {!detail.data?.history.length && <p className="text-muted-foreground">{t("noHistory")}</p>}
      </div>}
    </DialogContent></Dialog>
  </div>
}
