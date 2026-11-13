"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { ContentHandoff } from "@/components/shared/loading/content-handoff"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { useAIActivities, useAIConversations } from "@/hooks/use-ai-chat"
import { useAIAgentCatalog } from "@/hooks/use-ai-agent-catalog"
import { normalizeError } from "@/lib/errors/normalize-error"
import { textRole } from "@/lib/typography"
import { cn } from "@/lib/utils"
import type { AIActivity } from "@/types/ai-chat.types"

function Activity({ activity }: { activity: AIActivity }) {
  const t = useTranslations("aiChat")
  if (activity.kind === "usage") return null
  const isUser = activity.kind === "user"
  const isTool = activity.kind === "tool_use" || activity.kind === "tool_result"
  const label = isUser ? t("user") : activity.kind === "thinking" ? t("thinking") :
    activity.kind === "tool_use" ? t("toolUse") : activity.kind === "tool_result" ? t("toolResult") : t("assistant")
  return (
    <div className={cn("flex flex-col gap-1", isUser ? "items-end" : "items-start")}>
      <span className={textRole.helperText}>{label}{activity.tool ? ` · ${activity.tool}` : ""}</span>
      <div className={cn("max-w-full rounded-lg border border-border px-3 py-2", isUser ? "bg-secondary" : "bg-card", activity.isError && "border-destructive")}>
        {isTool ? <details><summary className="cursor-pointer">{activity.summary || activity.tool}</summary><pre className="overflow-auto whitespace-pre-wrap break-all">{activity.detail}</pre></details>
          : <p className="whitespace-pre-wrap break-words">{activity.detail || activity.summary}</p>}
      </div>
    </div>
  )
}

export function AIChatWorkbench() {
  const t = useTranslations("aiChat")
  const { list, create, send, stop, models, setModel, updateMetadata } = useAIConversations()
  const agentCatalog = useAIAgentCatalog()
  const [selected, setSelected] = React.useState<string | null>(null)
  const [newAgentKey, setNewAgentKey] = React.useState("auto")
  const [message, setMessage] = React.useState("")
  const [renameOpen, setRenameOpen] = React.useState(false)
  const [titleDraft, setTitleDraft] = React.useState("")
  const activities = useAIActivities(selected)
  const conversation = list.data?.find(item => item.id === selected)
  const conversationAgent = agentCatalog.list.data?.find(item => item.key === (conversation?.agentKey === "assistant" ? "mainagent" : conversation?.agentKey))

  React.useEffect(() => {
    if (!selected && list.data?.length) setSelected(list.data[0].id)
  }, [selected, list.data])

  React.useEffect(() => {
    const available = agentCatalog.list.data?.filter(agent => agent.enabled) ?? []
    if (!available.length || available.some(agent => agent.key === newAgentKey)) return
    setNewAgentKey(available.find(agent => agent.key === "auto")?.key ?? available[0].key)
  }, [agentCatalog.list.data, newAgentKey])

  const createConversation = async () => {
    try {
      const conversation = await create.mutateAsync({ title: t("newTitle"), agentKey: newAgentKey })
      setSelected(conversation.id)
    } catch { toast.error(t("error")) }
  }

  const sendMessage = async () => {
    const value = message.trim()
    if (!selected || !value || activities.running || send.isPending) return
    try {
      await send.mutateAsync({ id: selected, message: value })
      setMessage("")
      activities.refresh()
    } catch { toast.error(t("error")) }
  }

  const stopRun = async () => {
    if (!selected) return
    try { await stop.mutateAsync(selected); activities.refresh() } catch { toast.error(t("error")) }
  }

  const changeModel = async (value: string) => {
    if (!selected) return
    try { await setModel.mutateAsync({ id: selected, profileId: value === "__default__" ? null : value }) }
    catch { toast.error(t("modelError")) }
  }

  const updateConversation = async (input: { title?: string; pinned?: boolean }) => {
    if (!selected) return
    try { await updateMetadata.mutateAsync({ id: selected, input }); setRenameOpen(false) }
    catch { toast.error(t("error")) }
  }

  if (list.error) {
    return <AppErrorState error={normalizeError(list.error, { notFoundKind: "unexpected-error" })} onRetry={list.refetch} />
  }

  return (
    <ContentHandoff
      owner="ai-chat-workbench-content"
      layer="workspace"
      isLoading={list.isPending}
      skeleton={<div className="flex flex-col gap-2" aria-label={t("loading")}>
        {[0, 1, 2, 3].map(row => <Skeleton key={row} className="h-10 w-full" />)}
      </div>}
    >
      <div className="flex min-h-120 flex-col gap-3 md:flex-row">
        <aside className="flex min-w-0 flex-col gap-2 rounded-lg border border-border p-3 md:w-64 md:shrink-0">
          <div className="grid gap-1">
            <span className={textRole.helperText}>{t("newAgent")}</span>
            <Select value={newAgentKey} onValueChange={setNewAgentKey} disabled={create.isPending}>
              <SelectTrigger aria-label={t("newAgent")}><SelectValue /></SelectTrigger>
              <SelectContent>
                {(agentCatalog.list.data ?? []).filter(agent => agent.enabled).map(agent => <SelectItem key={agent.key} value={agent.key}>{agent.name}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
          <Button onClick={createConversation} disabled={create.isPending}>{t("new")}</Button>
          <div className="flex flex-col gap-1 overflow-auto">
            {list.data?.map(conversation => (
              <Button key={conversation.id} variant={selected === conversation.id ? "secondary" : "ghost"}
                className="w-full justify-start" onClick={() => setSelected(conversation.id)}>
                <span className="flex min-w-0 flex-col items-start"><span className="truncate">{conversation.pinned ? `📌 ${conversation.title}` : conversation.title}</span><span className="truncate text-xs text-muted-foreground">{agentCatalog.list.data?.find(agent => agent.key === (conversation.agentKey ?? "assistant"))?.name ?? conversation.agentKey ?? "assistant"}</span></span>
              </Button>
            ))}
          </div>
        </aside>
        <section className="flex min-h-120 min-w-0 flex-col rounded-lg border border-border md:flex-1">
          {!selected ? <div className="flex flex-1 items-center justify-center p-4"><span className={textRole.helperText}>{t("empty")}</span></div> : <>
            <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
              <span className="font-medium">{conversation?.title}</span>
              <Button type="button" size="sm" variant="ghost" onClick={() => { setTitleDraft(conversation?.title ?? ""); setRenameOpen(true) }}>{t("rename")}</Button>
              <Button type="button" size="sm" variant="ghost" disabled={updateMetadata.isPending} onClick={() => void updateConversation({ pinned: !conversation?.pinned })}>{conversation?.pinned ? t("unpin") : t("pin")}</Button>
              <span className={textRole.helperText}>{t("model")}</span>
              <Select value={conversation?.llmProfileId ?? "__default__"} onValueChange={(value: string) => void changeModel(value)} disabled={models.isPending || Boolean(models.error) || setModel.isPending || activities.running}>
                <SelectTrigger className="w-56" aria-label={t("model")}><SelectValue /></SelectTrigger>
                <SelectContent><SelectItem value="__default__">{t("modelDefault")}</SelectItem>{models.data?.map(model => <SelectItem key={model.id} value={model.id}>{model.displayName} ({model.model})</SelectItem>)}</SelectContent>
              </Select>
              {conversationAgent?.llmProfileId ? <span className={textRole.helperText}>{t("agentModelPriority")}</span> : null}
              {models.error ? <span className="text-xs text-destructive">{t("modelError")}</span> : null}
            </div>
            <div className="flex flex-1 flex-col gap-4 overflow-auto p-4" aria-live="polite">
              {!activities.items.length && <p className={textRole.helperText}>{t("noMessages")}</p>}
              {activities.items.map(item => <Activity key={item.id} activity={item} />)}
              {activities.running && <p className={textRole.helperText}>{t("running")}</p>}
              {activities.runStatus === "failed" && <p className={textRole.helperText}>{t("failed")}</p>}
              {activities.runStatus === "stopped" && <p className={textRole.helperText}>{t("stopped")}</p>}
              {activities.runStatus === "interrupted" && <p className={textRole.helperText}>{t("interrupted")}</p>}
              {activities.error != null && <p className={textRole.helperText}>{t("error")}</p>}
            </div>
            <form className="flex items-end gap-2 border-t border-border p-3" onSubmit={event => { event.preventDefault(); void sendMessage() }}>
              <Textarea value={message} onChange={event => setMessage(event.target.value)} placeholder={t("placeholder")}
                aria-label={t("placeholder")} onKeyDown={event => {
                  if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); void sendMessage() }
                }} />
              {activities.running ? <Button type="button" variant="outline" onClick={stopRun} disabled={stop.isPending}>{t("stop")}</Button>
                : <Button type="submit" disabled={!message.trim() || send.isPending}>{t("send")}</Button>}
            </form>
          </>}
        </section>
      </div>
      <Dialog open={renameOpen} onOpenChange={setRenameOpen}><DialogContent>
        <DialogHeader><DialogTitle>{t("rename")}</DialogTitle><DialogDescription>{t("renameDescription")}</DialogDescription></DialogHeader>
        <form onSubmit={event => { event.preventDefault(); void updateConversation({ title: titleDraft }) }} className="grid gap-3">
          <Input value={titleDraft} onChange={event => setTitleDraft(event.target.value)} maxLength={200} required autoFocus />
          <DialogFooter><Button type="submit" disabled={!titleDraft.trim() || updateMetadata.isPending}>{t("save")}</Button></DialogFooter>
        </form>
      </DialogContent></Dialog>
    </ContentHandoff>
  )
}
