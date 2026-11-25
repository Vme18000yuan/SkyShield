"use client"

import * as React from "react"
import Link from "next/link"
import { useLocale, useTranslations } from "next-intl"
import { useQuery } from "@tanstack/react-query"
import { PinIcon, PinOffIcon } from "lucide-react"
import { toast } from "sonner"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { ContentHandoff } from "@/components/shared/loading/content-handoff"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { TaskCategoryManager } from "@/components/ai/artex/task-category-manager"
import { RemarkEditor, TaskNameEditor } from "@/components/ai/artex/remark-editor"
import { useAuth } from "@/hooks/use-auth"
import { useAITasks, useAITaskMutations } from "@/hooks/use-ai-tasks"
import { normalizeError } from "@/lib/errors/normalize-error"
import { textRole } from "@/lib/typography"
import { api as artexAPI } from "@/services/artex-ai.service"
import type { AITask } from "@/types/ai-task.types"
import { AITaskCreateDialog } from "./ai-task-create-dialog"
import { AIDataDeleteButton } from "./ai-data-delete-button"

export function AITaskStatusBadge({ task }: { task: AITask }) {
  const t = useTranslations("aiTasks")
  const key = task.queued ? "queued" : task.status
  return <Badge variant={key === "done" ? "success" : key === "failed" || key === "timeout" ? "error" : key === "running" ? "info" : "outline"}>{t(key)}</Badge>
}

export function AITaskList() {
  const t = useTranslations("aiTasks")
  const locale = useLocale()
  const user = useAuth().data?.user?.id
  const [cursors, setCursors] = React.useState<string[]>([""])
  const [page, setPage] = React.useState(0)
  const [searchDraft, setSearchDraft] = React.useState("")
  const [query, setQuery] = React.useState("")
  const [status, setStatus] = React.useState("")
  const [category, setCategory] = React.useState("")
  const categories = useQuery({ queryKey:["ai-task-categories", "default-tenant", user], queryFn:artexAPI.taskCategories, enabled:Boolean(user) })
  const { metadata } = useAITaskMutations()
  const resetPage = () => { setCursors([""]); setPage(0) }
  React.useEffect(() => {
    const timer = window.setTimeout(() => { setQuery(searchDraft.trim()); setCursors([""]); setPage(0) }, 300)
    return () => window.clearTimeout(timer)
  }, [searchDraft])
  React.useEffect(() => {
    if (category && category !== "none" && categories.data && !categories.data.some(item => item.id === category)) {
      setCategory(""); setCursors([""]); setPage(0)
    }
  }, [category, categories.data])
  const list = useAITasks(cursors[page], true, { q:query || undefined, status:status || undefined, category:category || undefined })
  const save = async (task: AITask, patch: { displayName?: string; remark?: string; pinned?: boolean; categoryId?: string | null }) => {
    await metadata.mutateAsync({ id:task.id, patch })
  }
  if (list.error) return <AppErrorState error={normalizeError(list.error, { notFoundKind:"unexpected-error" })} onRetry={list.refetch} />
  return <div className="flex min-w-0 flex-col gap-3">
    <div className="flex flex-wrap items-center gap-2">
      <Input aria-label="搜索 AI 任务" className="min-w-48 max-w-sm flex-1" placeholder="搜索任务、备注或分类" value={searchDraft} onChange={event => setSearchDraft(event.target.value)} />
      <select aria-label="任务状态筛选" className="h-9 rounded-md border bg-background px-2 text-sm" value={status} onChange={event => { setStatus(event.target.value); resetPage() }}>
        <option value="">全部状态</option>{(["created","queued","running","paused","done","failed","timeout","stopped"] as const).map(value => <option key={value} value={value}>{t(value)}</option>)}
      </select>
      <select aria-label="任务分类筛选" className="h-9 rounded-md border bg-background px-2 text-sm" value={category} onChange={event => { setCategory(event.target.value); resetPage() }}>
        <option value="">全部分类</option><option value="none">未分类</option>{categories.data?.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
      </select>
      <TaskCategoryManager categories={categories.data ?? []} onChanged={async () => { await Promise.all([categories.refetch(), list.refetch()]) }} />
      <div className="ml-auto"><AITaskCreateDialog /></div>
    </div>
    {categories.error && <p className="text-sm text-destructive">任务分类加载失败，请刷新重试。</p>}
    <ContentHandoff owner="ai-task-list-content" layer="workspace" isLoading={list.isPending}
      skeleton={<div className="flex flex-col gap-2" aria-label={t("loading")}>{[0, 1, 2, 3].map(row => <Skeleton key={row} className="h-10 w-full" />)}</div>}>
      <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>{t("name")}</TableHead><TableHead>备注</TableHead><TableHead>{t("goal")}</TableHead><TableHead>分类</TableHead><TableHead>{t("status")}</TableHead><TableHead>{t("createdAt")}</TableHead><TableHead /></TableRow></TableHeader>
        <TableBody>{list.data?.tasks.map(task => <TableRow key={task.id}>
          <TableCell><div className="flex items-center gap-1"><Button aria-label={`${task.pinned ? "取消置顶" : "置顶"} ${task.displayName}`} variant="ghost" size="icon-sm" disabled={metadata.isPending} onClick={() => void save(task, { pinned:!task.pinned }).catch(error => toast.error((error as Error).message))}>{task.pinned ? <PinOffIcon /> : <PinIcon />}</Button><TaskNameEditor label={`任务 ${task.displayName}`} value={task.displayName} onSave={value => save(task, { displayName:value })} /></div></TableCell>
          <TableCell><RemarkEditor label={`任务 ${task.displayName}`} value={task.remark ?? ""} onSave={value => save(task, { remark:value })} /></TableCell>
          <TableCell className="max-w-md truncate">{task.goal}</TableCell>
          <TableCell><select aria-label={`任务 ${task.displayName} 分类`} className="h-8 max-w-40 rounded-md border bg-background px-2 text-sm" value={task.categoryId ?? ""} disabled={metadata.isPending} onChange={event => void save(task, { categoryId:event.target.value || null }).catch(error => toast.error((error as Error).message))}><option value="">未分类</option>{categories.data?.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></TableCell>
          <TableCell><AITaskStatusBadge task={task} /></TableCell><TableCell>{new Date(task.createdAt).toLocaleString(locale)}</TableCell><TableCell><div className="flex gap-2"><Button variant="outline" size="sm" render={<Link href={`/ai/tasks/${task.id}/`} />}>{t("view")}</Button><AIDataDeleteButton subject={task.displayName} request={{ kind: "task", id: task.id }} onDeleted={resetPage} /></div></TableCell>
        </TableRow>)}{!list.data?.tasks.length && <TableRow><TableCell colSpan={7}>{t("empty")}</TableCell></TableRow>}</TableBody>
      </Table></div>
    </ContentHandoff>
    <div className="flex items-center justify-end gap-2"><span className={textRole.helperText}>{t("page", { number:page + 1 })}</span>
      <Button variant="outline" size="sm" disabled={page === 0} onClick={() => setPage(value => value - 1)}>{t("previous")}</Button>
      <Button variant="outline" size="sm" disabled={!list.data?.nextBefore} onClick={() => { const next = list.data?.nextBefore; if (next) { setCursors(current => [...current.slice(0, page + 1), next]); setPage(value => value + 1) } }}>{t("next")}</Button>
    </div>
  </div>
}
