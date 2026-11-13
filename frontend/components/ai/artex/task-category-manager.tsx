"use client"

import * as React from "react"
import { PencilIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { api } from "@/services/artex-ai.service"
import type { TaskCategory } from "@/types/artex-ai.types"

export function TaskCategoryManager({ categories, onChanged }: { categories: TaskCategory[]; onChanged: () => Promise<unknown> }) {
  const [open, setOpen] = React.useState(false)
  const [name, setName] = React.useState("")
  const [edit, setEdit] = React.useState<string | null>(null)
  const [draft, setDraft] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const save = async (id?: string) => {
    const value = (id ? draft : name).trim()
    if (!value) return
    setBusy(true)
    try {
      if (id) await api.updateTaskCategoryName(id, value)
      else await api.createTaskCategory(value)
      setName(""); setEdit(null)
      await onChanged()
    } catch (error) { toast.error(`保存分类失败：${(error as Error).message}`) }
    finally { setBusy(false) }
  }
  const remove = async (item: TaskCategory) => {
    if (!window.confirm(`删除分类「${item.name}」？已有任务和模板将变为未分类。`)) return
    setBusy(true)
    try { await api.deleteTaskCategory(item.id); await onChanged() }
    catch (error) { toast.error(`删除分类失败：${(error as Error).message}`) }
    finally { setBusy(false) }
  }
  return <Dialog open={open} onOpenChange={setOpen}>
    <Button type="button" variant="outline" size="sm" onClick={() => setOpen(true)}>管理分类</Button>
    <DialogContent className="sm:max-w-xl">
      <DialogHeader><DialogTitle>任务分类管理</DialogTitle><DialogDescription>分类用于整理任务；删除分类不会删除任务或模板。</DialogDescription></DialogHeader>
      <form className="flex gap-2" onSubmit={event => { event.preventDefault(); void save() }}>
        <Input aria-label="新建分类名称" maxLength={80} value={name} onChange={event => setName(event.target.value)} placeholder="输入分类名称" />
        <Button type="submit" disabled={busy || !name.trim()}>新建分类</Button>
      </form>
      <div className="max-h-80 space-y-2 overflow-y-auto">
        {categories.map(item => <div key={item.id} className="flex items-center gap-2 rounded-md border p-2">
          {edit === item.id ? <>
            <Input aria-label={`编辑分类 ${item.name}`} maxLength={80} value={draft} onChange={event => setDraft(event.target.value)} onKeyDown={event => { if (event.key === "Enter") { event.preventDefault(); void save(item.id) } }} />
            <Button size="sm" disabled={busy || !draft.trim()} onClick={() => void save(item.id)}>保存</Button>
            <Button size="sm" variant="ghost" onClick={() => setEdit(null)}>取消</Button>
          </> : <>
            <span className="min-w-0 flex-1 truncate">{item.name}</span><span className="text-xs text-muted-foreground">{item.task_count} 个任务</span>
            <Button aria-label={`编辑分类 ${item.name}`} variant="ghost" size="icon-sm" disabled={busy} onClick={() => { setEdit(item.id); setDraft(item.name) }}><PencilIcon /></Button>
            <Button aria-label={`删除分类 ${item.name}`} variant="ghost" size="icon-sm" disabled={busy} onClick={() => void remove(item)}><Trash2Icon /></Button>
          </>}
        </div>)}
        {!categories.length && <p className="text-sm text-muted-foreground">暂无分类</p>}
      </div>
    </DialogContent>
  </Dialog>
}
