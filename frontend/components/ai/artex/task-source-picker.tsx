"use client"

import * as React from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useAITasks } from "@/hooks/use-ai-tasks"
import type { AITask } from "@/types/ai-task.types"

export function TaskSourcePicker({ value, onChange, enabled }: { value: AITask[]; onChange: (tasks: AITask[]) => void; enabled: boolean }) {
  const [draft, setDraft] = React.useState("")
  const [query, setQuery] = React.useState("")
  const [cursors, setCursors] = React.useState([""])
  const [page, setPage] = React.useState(0)
  React.useEffect(() => {
    const timer = window.setTimeout(() => { setQuery(draft.trim()); setCursors([""]); setPage(0) }, 300)
    return () => window.clearTimeout(timer)
  }, [draft])
  const tasks = useAITasks(cursors[page], enabled, { q:query || undefined })
  const selected = new Set(value.map(item => item.id))
  return <div className="grid gap-2 rounded-md border p-3">
    <div className="text-sm font-medium">关联来源任务（{value.length}/8）</div>
    <p className="text-xs text-muted-foreground">只读继承所选任务的资产和已确认结论，不递归继承其来源。</p>
    <Input aria-label="搜索来源任务" placeholder="搜索任务名称或目标" value={draft} onChange={event => setDraft(event.target.value)} />
    {value.length > 0 && <div className="flex flex-wrap gap-1">{value.map(item => <Button key={item.id} type="button" size="sm" variant="secondary" aria-label={`移除来源任务 ${item.displayName}`} onClick={() => onChange(value.filter(task => task.id !== item.id))}>{item.displayName} ×</Button>)}</div>}
    <div className="max-h-40 space-y-1 overflow-y-auto">
      {tasks.data?.tasks.map(item => <label key={item.id} className="flex cursor-pointer items-start gap-2 rounded p-1.5 text-sm hover:bg-muted">
        <input type="checkbox" aria-label={`继承任务 ${item.displayName}`} checked={selected.has(item.id)} disabled={!selected.has(item.id) && value.length >= 8} onChange={event => onChange(event.target.checked ? [...value, item] : value.filter(task => task.id !== item.id))} />
        <span className="min-w-0"><span className="block truncate font-medium">{item.displayName}</span><span className="block truncate text-xs text-muted-foreground">{item.goal}</span></span>
      </label>)}
      {!tasks.isLoading && !tasks.error && !tasks.data?.tasks.length && <p className="text-xs text-muted-foreground">暂无符合条件的来源任务</p>}
      {tasks.error && <p className="text-xs text-destructive">来源任务加载失败，请重试。</p>}
    </div>
    <div className="flex justify-end gap-2"><Button type="button" size="sm" variant="outline" disabled={page === 0 || tasks.isFetching} onClick={() => setPage(n => n - 1)}>上一页</Button><Button type="button" size="sm" variant="outline" disabled={!tasks.data?.nextBefore || tasks.isFetching} onClick={() => { const next=tasks.data?.nextBefore; if (next) { setCursors(current => [...current.slice(0,page+1),next]); setPage(n => n+1) } }}>下一页</Button></div>
  </div>
}
