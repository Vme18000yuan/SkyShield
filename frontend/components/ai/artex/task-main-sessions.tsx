// Adapted from ARTEX, AGPL-3.0-only; see third_party/artex/SKYSHIELD-NOTICE.md.
"use client"

import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { Paperclip, X } from "lucide-react"
import { MentionTextarea } from "@/components/ai/artex/mention-textarea"
import { Transcript } from "@/components/ai/artex/transcript"
import { SideQuestionButton, SideQuestionWorkspace } from "@/components/ai/artex/side-question-workspace"
import { useSideQuestions } from "@/hooks/artex/use-side-questions"
import { useAuth } from "@/hooks/use-auth"
import { Button } from "@/components/ui/button"
import { api } from "@/lib/api-client"
import { api as artexAPI } from "@/services/artex-ai.service"
import type { Activity, ChatAttachment } from "@/types/artex-ai.types"
import type { AITaskStatus } from "@/types/ai-task.types"

type Session = { seq: number; createdAt: string | null }

export function TaskMainSessions({ taskId }: { taskId: string; status: AITaskStatus }) {
  const user = useAuth().data?.user?.id
  return <TaskMainSessionsInner key={`${user ?? "anonymous"}:${taskId}`} taskId={taskId} />
}
function TaskMainSessionsInner({ taskId }: { taskId: string }) {
  const t = useTranslations("aiTasks")
  const user = useAuth().data?.user?.id
  const cache = useQueryClient()
  const base = `/aiTasks/${encodeURIComponent(taskId)}`
  const sessionKey = ["task-main-sessions", user, taskId]
  const sessions = useQuery({ queryKey: sessionKey, enabled: !!user, queryFn: async () => (await api.get<{ sessions: Session[]; current: number }>(`${base}/mainSessions`)).data })
  const chatKey = ["task-main-chat", user, taskId]
  const chatStatus = useQuery({ queryKey: chatKey, enabled: !!user, queryFn: async () => (await api.get<{ running: boolean }>(`${base}/mainChat`)).data, refetchInterval: 1000 })
  const running = chatStatus.data?.running ?? false
  const [segment, setSegment] = React.useState<number | null>(null)
  const side = useSideQuestions(segment === null ? null : `/api/tasks/${encodeURIComponent(taskId)}/mainSessions/${segment}`)
  const [cursors, setCursors] = React.useState([""])
  const [page, setPage] = React.useState(0)
  const [message, setMessage] = React.useState("")
  const [attachments, setAttachments] = React.useState<ChatAttachment[]>([])
  React.useEffect(() => { setSegment(null); setCursors([""]); setPage(0); setMessage(""); setAttachments([]) }, [user, taskId])
  const [uploading, setUploading] = React.useState(false)
  const fileInput = React.useRef<HTMLInputElement>(null)
  const pickFiles = async (files: File[]) => {
    if (!files.length) return
    if (files.length + attachments.length > 20) { toast.error(t("tooManyAttachments")); return }
    setUploading(true)
    try { const result = await artexAPI.chatUpload("task", taskId, files); setAttachments(current => [...current, ...result.attachments]) }
    catch { toast.error(t("attachmentUploadError")) }
    finally { setUploading(false) }
  }
  React.useEffect(() => { if (segment === null && sessions.data?.current != null) setSegment(sessions.data.current) }, [segment, sessions.data?.current])
  const before = cursors[page] ?? ""
  const messageKey = ["task-main-messages", user, taskId, segment, before]
  const messages = useQuery({ queryKey: messageKey, enabled: !!user && segment !== null, gcTime: 0, queryFn: async () => (await api.get<{ activities: Activity[]; nextBefore: string }>(`${base}/mainSessions/${segment}/activities`, { params: before ? { before } : {} })).data, refetchInterval: page === 0 ? 2000 : false })
  const create = useMutation({ mutationFn: async () => (await api.post<Session>(`${base}/mainSessions`)).data, onSuccess: async item => { setSegment(item.seq); setCursors([""]); setPage(0); await cache.invalidateQueries({ queryKey: sessionKey }) }, onError: () => toast.error(t("mainSessionError")) })
  const send = useMutation({ mutationFn: async () => api.post(`${base}/mainChat`, { message: message.trim(), segment, attachments }), onSuccess: async () => { setMessage(""); setAttachments([]); setCursors([""]); setPage(0); cache.setQueryData(chatKey, { running: true }); await cache.invalidateQueries({ queryKey: ["task-main-messages", user, taskId, segment] }); toast.success(t("messageSent")) }, onError: () => toast.error(t("chatError")) })
  const stop = useMutation({ mutationFn: async () => api.post(`${base}/mainChat/stop`), onSuccess: async () => { await cache.invalidateQueries({ queryKey: chatKey }) }, onError: () => toast.error(t("chatStopError")) })
  const choose = (next: number) => { setSegment(next); setCursors([""]); setPage(0) }
  return <SideQuestionWorkspace side={side} label={t("mainAgent")}><div className="flex flex-col gap-3">
    <p className="text-sm text-muted-foreground">{t("mainAgentDescription")}</p>
    <div className="flex flex-wrap items-center gap-2">
      <label className="text-sm" htmlFor="main-session-select">{t("mainSession")}</label>
      <select id="main-session-select" aria-label={t("mainSession")} className="h-8 min-w-40 rounded-md border bg-background px-2 text-sm" value={segment ?? ""} onChange={event => choose(Number(event.target.value))} disabled={sessions.isLoading || send.isPending}>
        {segment === null && <option value="">{t("loading")}</option>}
        {(sessions.data?.sessions ?? []).map(item => <option key={item.seq} value={item.seq}>{item.seq === 0 ? t("mainSessionOriginal") : t("mainSessionNumber", { number: item.seq })}</option>)}
      </select>
      <Button size="sm" variant="outline" disabled={create.isPending || send.isPending} onClick={() => create.mutate()}>{t("newMainSession")}</Button>
      <SideQuestionButton side={side} />
    </div>
    {(sessions.isError || messages.isError) && <p role="alert" className="text-sm text-destructive">{t("mainSessionError")}</p>}
    <div className="flex max-h-100 min-h-24 flex-col gap-2 overflow-y-auto rounded-md border bg-card p-3">
      {messages.data?.activities.length ? <Transcript activity={messages.data.activities} live={running} taskId={taskId} chat /> : !messages.isLoading && !messages.isError ? <p className="text-sm text-muted-foreground">{t("noMainAgentMessages")}</p> : null}
    </div>
    <div className="flex items-center justify-end gap-2"><span className="text-sm text-muted-foreground">{t("page", { number: page + 1 })}</span>
      <Button size="sm" variant="outline" disabled={page === 0 || messages.isFetching} onClick={() => setPage(value => value - 1)}>{t("previous")}</Button>
      <Button size="sm" variant="outline" disabled={!messages.data?.nextBefore || messages.isFetching} onClick={() => { const next = messages.data?.nextBefore; if (next) { setCursors(current => [...current.slice(0, page + 1), next]); setPage(value => value + 1) } }}>{t("mainSessionOlder")}</Button>
    </div>
    {running && <p role="status" className="text-sm text-muted-foreground">{t("mainAgentWorking")}</p>}
    <form className="flex flex-col items-end gap-2" onSubmit={event => { event.preventDefault(); if (!attachments.length && side.handleCommand(message, () => setMessage(""))) return; if ((message.trim() || attachments.length > 0) && segment !== null && !uploading) send.mutate() }}>
      {attachments.length > 0 && <div className="flex w-full flex-wrap gap-2">{attachments.map(file => <span key={file.path} className="flex items-center gap-1 rounded border px-2 py-1 text-xs">{file.name} ({file.size} B)<button type="button" aria-label={t("removeAttachment", { name: file.name })} onClick={() => setAttachments(current => current.filter(item => item.path !== file.path))}><X className="size-3" /></button></span>)}</div>}
      <MentionTextarea aria-label={t("messageLabel")} value={message} maxLength={65536} onValueChange={setMessage} placeholder={t("messagePlaceholder")} disabled={running || send.isPending || segment === null} />
      <div className="flex w-full items-center justify-between gap-2"><div><input ref={fileInput} type="file" multiple className="hidden" aria-label={t("uploadAttachment")} onChange={event => { const files = Array.from(event.target.files ?? []); event.target.value = ""; void pickFiles(files) }} /><Button type="button" size="sm" variant="outline" disabled={running || uploading || send.isPending} onClick={() => fileInput.current?.click()}><Paperclip className="mr-1 size-4" />{uploading ? t("uploadingAttachment") : t("uploadAttachment")}</Button></div>
      {running ? <Button type="button" variant="outline" disabled={stop.isPending} onClick={() => stop.mutate()}>{stop.isPending ? t("chatStopping") : t("stopChat")}</Button> : <Button type="submit" disabled={send.isPending || uploading || segment === null || (!message.trim() && attachments.length === 0)}>{send.isPending ? t("sending") : t("sendMessage")}</Button>}</div>
    </form>
  </div></SideQuestionWorkspace>
}
