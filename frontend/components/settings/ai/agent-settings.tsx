"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { AgentCatalogPanel } from "@/components/settings/ai/agent-catalog-panel"
import { ContentHandoff } from "@/components/shared/loading/content-handoff"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { useAIAgentSettings } from "@/hooks/use-ai-agent-settings"
import { normalizeError } from "@/lib/errors/normalize-error"
import type { AIAgentSetting } from "@/types/ai-agent-setting.types"

export function AIAgentSettings() {
  const t = useTranslations("aiAgents")
  const { list, save } = useAIAgentSettings()
  const [editing, setEditing] = React.useState<AIAgentSetting | null>(null)
  if (list.error) return <AppErrorState error={normalizeError(list.error, { notFoundKind: "unexpected-error" })} onRetry={list.refetch} />
  const submit = async () => {
    if (!editing) return
    try { await save.mutateAsync(editing); setEditing(null); toast.success(t("saved")) }
    catch { toast.error(t("saveError")) }
  }
  return <ContentHandoff owner="ai-agent-settings-content" layer="workspace" isLoading={list.isPending}
    skeleton={<div className="flex flex-col gap-2" aria-label={t("loading")}>{[0, 1, 2].map(row => <Skeleton key={row} className="h-10 w-full" />)}</div>}>
    <div className="flex flex-col gap-4">
    <AgentCatalogPanel />
    <Table><TableHeader><TableRow><TableHead>{t("agent")}</TableHead><TableHead>{t("status")}</TableHead><TableHead>{t("turns")}</TableHead><TableHead>{t("seconds")}</TableHead><TableHead /></TableRow></TableHeader>
      <TableBody>{list.data?.map(item => <TableRow key={item.key}><TableCell>{t(item.key)}</TableCell>
        <TableCell><Badge variant={item.enabled ? "success" : "outline"}>{item.enabled ? t("enabled") : t("disabled")}</Badge></TableCell>
        <TableCell>{item.maxTurns || (item.key === "assistant" ? t("unlimited") : t("defaultLimit"))}</TableCell><TableCell>{item.runSeconds || t("unlimited")}</TableCell>
        <TableCell><Button variant="outline" size="sm" onClick={() => setEditing({ ...item })}>{t("edit")}</Button></TableCell></TableRow>)}</TableBody>
    </Table>
    <Dialog open={Boolean(editing)} onOpenChange={open => { if (!open) setEditing(null) }}><DialogContent>
      <DialogHeader><DialogTitle>{editing ? t(editing.key) : t("edit")}</DialogTitle><DialogDescription>{t("editDescription")}</DialogDescription></DialogHeader>
      {editing && <form className="flex flex-col gap-3" onSubmit={event => { event.preventDefault(); void submit() }}>
        <div className="flex items-center gap-2"><Switch id="ai-agent-enabled" checked={editing.enabled} onCheckedChange={enabled => setEditing({ ...editing, enabled })} /><Label htmlFor="ai-agent-enabled">{t("enabled")}</Label></div>
        <div className="flex flex-col gap-1"><Label htmlFor="ai-agent-turns">{t("turns")}</Label><Input id="ai-agent-turns" type="number" min={0} max={1000} required value={editing.maxTurns} onChange={event => setEditing({ ...editing, maxTurns: Number(event.target.value) })} /><p className="text-sm text-muted-foreground">{editing.key === "assistant" ? t("assistantTurnsHint") : t("taskTurnsHint")}</p></div>
        <div className="flex flex-col gap-1"><Label htmlFor="ai-agent-seconds">{t("seconds")}</Label><Input id="ai-agent-seconds" type="number" min={0} max={86400} required value={editing.runSeconds} onChange={event => setEditing({ ...editing, runSeconds: Number(event.target.value) })} /><p className="text-sm text-muted-foreground">{t("timeHint")}</p></div>
        <div className="flex flex-col gap-1"><Label htmlFor="ai-agent-prompt">{t("prompt")}</Label><Textarea id="ai-agent-prompt" maxLength={32768} value={editing.additionalPrompt} onChange={event => setEditing({ ...editing, additionalPrompt: event.target.value })} /></div>
        <DialogFooter><Button type="submit" disabled={save.isPending}>{t("save")}</Button></DialogFooter>
      </form>}
    </DialogContent></Dialog>
    </div>
  </ContentHandoff>
}
