"use client"

import * as React from "react"
import { useAuth } from "@/hooks/use-auth"
import { Button } from "@/components/ui/button"
import { Transcript } from "@/components/ai/artex/transcript"
import { api } from "@/services/artex-ai.service"
import type { Activity, InterceptExecution } from "@/types/artex-ai.types"

type Focus = { id: number; source: InterceptExecution; items: Activity[]; hasBefore: boolean; hasAfter: boolean }

export function TaskApprovalExecution({ taskId }: { taskId: string }) {
  const user = useAuth().data?.user?.id
  return <TaskApprovalExecutionInner key={`${user ?? "anonymous"}:${taskId}`} taskId={taskId} enabled={!!user} />
}

function TaskApprovalExecutionInner({ taskId, enabled }: { taskId: string; enabled: boolean }) {
  const [focus, setFocus] = React.useState<Focus | null>(null)
  const [loading, setLoading] = React.useState(false)
  const [error, setError] = React.useState("")
  const [revision, setRevision] = React.useState(0)
  const [approvalId, setApprovalId] = React.useState<number | null>(null)
  const busy = React.useRef(false)
  React.useEffect(() => {
    const id = Number(new URLSearchParams(window.location.search).get("approval"))
    setApprovalId(Number.isSafeInteger(id) && id > 0 ? id : null)
  }, [taskId])
  React.useEffect(() => {
    if (!enabled || !approvalId) return
    let active = true
    setLoading(true); setError(""); setFocus(null)
    void (async () => {
      const source = await api.interceptExecution(approvalId)
      if (source.task_id !== taskId || source.conversation_id != null || !source.seq || !source.session) throw new Error("审批来源与当前任务不一致")
      const page = await api.interceptExecutionContext(approvalId)
      if (!page.items.some(item => item.seq === source.seq && item.kind === "tool_use")) throw new Error("未找到对应工具调用")
      if (active) setFocus({ id: approvalId, source, items: page.items, hasBefore: page.has_before, hasAfter: page.has_after })
    })().catch(reason => { if (active) setError((reason as Error).message || "无法定位原始执行") }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [approvalId, enabled, taskId, revision])
  const extend = async (older: boolean) => {
    if (!focus || busy.current || !focus.items.length) return
    busy.current = true; setLoading(true); setError("")
    try {
      const cursor = older ? focus.items[0].seq : focus.items[focus.items.length - 1].seq
      const page = await api.interceptExecutionContext(focus.id, older ? cursor : undefined, older ? undefined : cursor)
      setFocus(current => {
        if (!current || current.id !== focus.id) return current
        const byID = new Map([...current.items, ...page.items].map(item => [item.seq, item]))
        return { ...current, items: [...byID.values()].sort((a, b) => a.seq - b.seq), hasBefore: older ? page.has_before : current.hasBefore, hasAfter: older ? current.hasAfter : page.has_after }
      })
    } catch (reason) { setFocus(null); setError((reason as Error).message || "无法加载执行上下文") }
    finally { busy.current = false; setLoading(false) }
  }
  const close = () => {
    const url = new URL(window.location.href); url.searchParams.delete("approval"); window.history.replaceState(null, "", url)
    setApprovalId(null); setFocus(null); setError("")
  }
  if (!approvalId || !enabled) return null
  const session = focus?.source.session ?? ""
  const sessionLabel = session.startsWith("main:") ? `主 Agent 会话 ${session.slice(5)}` : session.startsWith("intent:") ? `意图 ${session.slice(7)}` : "规划执行"
  return <section className="space-y-2 rounded-md border border-primary/40 bg-card p-3" aria-label={`审批 ${approvalId} 的执行位置`}>
    <div className="flex flex-wrap items-center justify-between gap-2"><div className="text-sm font-medium">审批 #{approvalId} · {focus ? sessionLabel : "定位执行"}</div><div className="flex gap-2">{error && <Button size="sm" variant="outline" onClick={() => setRevision(value => value + 1)}>重试</Button>}<Button size="sm" variant="ghost" onClick={close}>关闭定位</Button></div></div>
    {error && <p role="alert" className="text-sm text-destructive">无法定位：{error}</p>}
    {loading && !focus && <p role="status" className="text-sm text-muted-foreground">正在加载执行上下文…</p>}
    {focus && <><div className="flex justify-between gap-2"><Button size="sm" variant="outline" disabled={loading || !focus.hasBefore} onClick={() => void extend(true)}>加载更早记录</Button><Button size="sm" variant="outline" disabled={loading || !focus.hasAfter} onClick={() => void extend(false)}>加载更晚记录</Button></div><div data-slot="scroll-area-viewport" className="max-h-[32rem] overflow-y-auto rounded border p-3"><Transcript activity={focus.items} taskId={taskId} chat={session.startsWith("main:")} focusedSeq={focus.source.seq} /></div></>}
  </section>
}
