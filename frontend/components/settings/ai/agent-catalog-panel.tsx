"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"

import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { useAIAgentCatalog } from "@/hooks/use-ai-agent-catalog"
import { useAILLMProfiles } from "@/hooks/use-ai-llm-profiles"
import { normalizeError } from "@/lib/errors/normalize-error"
import type { AIAgentCatalogItem, AIAgentPromptVersion } from "@/types/ai-agent-catalog.types"

type Draft = {
  key: string
  name: string
  description: string
  systemPrompt: string
  enabled: boolean
  maxTurns: number
  runSeconds: number
  llmProfileId: string
  builtin: boolean
  defaultPrompt: string
  wrapupPrompt: string
  wrapupMaxTurns: number
  taskTimeoutPrompt: string
  taskTimeoutMaxTurns: number
  defaultWrapupPrompt: string
  defaultWrapupMaxTurns: number
  defaultTaskTimeoutPrompt: string
}

const EMPTY_DRAFT: Draft = { key: "", name: "", description: "", systemPrompt: "", enabled: true, maxTurns: 0, runSeconds: 1200, llmProfileId: "", builtin: false, defaultPrompt: "", wrapupPrompt: "", wrapupMaxTurns: 0, taskTimeoutPrompt: "", taskTimeoutMaxTurns: 0, defaultWrapupPrompt: "", defaultWrapupMaxTurns: 10, defaultTaskTimeoutPrompt: "" }

function promptVariables(key: string): string[] {
  const roleVars: Record<string, string[]> = {
    goals: ["EngagementDescription"], planner: ["Goal", "Scope", "AssetSummary"],
    worker: ["ProxyAddr", "WorkerName"], mainagent: ["Goal", "AssetSummary", "FindingsSummary"],
  }
  return [...(roleVars[key] ?? []), "DataDir", "Now"]
}

export function AgentCatalogPanel() {
  const t = useTranslations("aiAgents")
  const catalog = useAIAgentCatalog()
  const profiles = useAILLMProfiles()
  const [open, setOpen] = React.useState(false)
  const [editing, setEditing] = React.useState(false)
  const [draft, setDraft] = React.useState<Draft>(EMPTY_DRAFT)
  const [versions, setVersions] = React.useState<AIAgentPromptVersion[]>([])
  const [preview, setPreview] = React.useState<string | null>(null)
  const busy = catalog.create.isPending || catalog.update.isPending

  const create = () => {
    setEditing(false)
    setDraft({ ...EMPTY_DRAFT })
    setVersions([])
    setPreview(null)
    setOpen(true)
  }

  const edit = async (item: AIAgentCatalogItem) => {
    try {
      const detail = await catalog.loadDetail(item.key)
      setEditing(true)
      setDraft({
        key: detail.key,
        name: detail.name,
        description: detail.description,
        systemPrompt: detail.systemPrompt ?? "",
        enabled: detail.enabled,
        maxTurns: detail.maxTurns,
        runSeconds: detail.runSeconds,
        llmProfileId: detail.llmProfileId ?? "",
        builtin: detail.builtin,
        defaultPrompt: detail.defaultSystemPrompt ?? "",
        wrapupPrompt: detail.wrapupPrompt ?? "",
        wrapupMaxTurns: detail.wrapupMaxTurns ?? 0,
        taskTimeoutPrompt: detail.taskTimeoutPrompt ?? "",
        taskTimeoutMaxTurns: detail.taskTimeoutMaxTurns ?? 0,
        defaultWrapupPrompt: detail.defaultWrapupPrompt ?? "",
        defaultWrapupMaxTurns: detail.defaultWrapupMaxTurns ?? 10,
        defaultTaskTimeoutPrompt: detail.defaultTaskTimeoutPrompt ?? "",
      })
      setVersions([])
      setPreview(null)
      setOpen(true)
      try { setVersions(await catalog.loadPromptVersions(item.key)) } catch { toast.error(t("catalogHistoryError")) }
    } catch {
      toast.error(t("catalogLoadError"))
    }
  }

  const previewPrompt = async () => {
    try { setPreview(await catalog.previewPrompt.mutateAsync({ key: draft.key, template: draft.systemPrompt })) }
    catch { toast.error(t("catalogPreviewError")) }
  }

  const save = async () => {
    try {
      if (editing) {
        await catalog.update.mutateAsync({
          key: draft.key,
          input: {
            name: draft.name,
            description: draft.description,
            systemPrompt: draft.systemPrompt,
            enabled: draft.enabled,
            maxTurns: draft.maxTurns,
            runSeconds: draft.runSeconds,
            llmProfileId: draft.llmProfileId || null,
            wrapupPrompt: draft.wrapupPrompt,
            wrapupMaxTurns: draft.wrapupMaxTurns,
            taskTimeoutPrompt: draft.taskTimeoutPrompt,
            taskTimeoutMaxTurns: draft.taskTimeoutMaxTurns,
          },
        })
      } else {
        await catalog.create.mutateAsync({
          key: draft.key,
          name: draft.name,
          description: draft.description,
          systemPrompt: draft.systemPrompt,
        })
      }
      setOpen(false)
      toast.success(editing ? t("catalogSaved") : t("catalogCreated"))
    } catch {
      toast.error(t("catalogSaveError"))
    }
  }

  const remove = async (item: AIAgentCatalogItem) => {
    try {
      await catalog.remove.mutateAsync(item.key)
      toast.success(t("catalogDeleted"))
    } catch {
      toast.error(t("catalogDeleteError"))
    }
  }

  if (catalog.list.error) {
    return <AppErrorState error={normalizeError(catalog.list.error, { notFoundKind: "unexpected-error" })} onRetry={catalog.list.refetch} />
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3 border-b px-4 py-3">
        <div className="space-y-1">
          <CardTitle className="text-base">{t("catalogTitle")}</CardTitle>
          <CardDescription>{t("catalogDescription")}</CardDescription>
        </div>
        <Button size="sm" onClick={create}>{t("catalogCreate")}</Button>
      </CardHeader>
      <CardContent className="p-0">
        {catalog.list.isPending ? <div className="flex flex-col gap-2 p-4">{[0, 1, 2].map(row => <Skeleton key={row} className="h-9 w-full" />)}</div> : (
          <Table>
            <TableHeader><TableRow><TableHead>{t("catalogName")}</TableHead><TableHead>{t("catalogKey")}</TableHead><TableHead>{t("catalogType")}</TableHead><TableHead>{t("status")}</TableHead><TableHead /></TableRow></TableHeader>
            <TableBody>
              {catalog.list.data?.map(item => (
                <TableRow key={item.key}>
                  <TableCell className="min-w-44"><div className="font-medium">{item.name}</div><div className="max-w-xl text-xs text-muted-foreground">{item.description}</div></TableCell>
                  <TableCell className="font-mono text-xs">{item.key}</TableCell>
                  <TableCell><Badge variant={item.builtin ? "outline" : "secondary"}>{item.builtin ? t("catalogBuiltin") : t("catalogCustom")}</Badge></TableCell>
                  <TableCell><Badge variant={item.enabled ? "success" : "outline"}>{item.enabled ? t("enabled") : t("disabled")}</Badge></TableCell>
                  <TableCell><div className="flex justify-end gap-2">
                    <Button type="button" variant="outline" size="sm" onClick={() => void edit(item)}>{t("edit")}</Button>
                    {!item.builtin ? <Button type="button" variant="outline" size="sm" disabled={catalog.remove.isPending} onClick={() => void remove(item)}>{t("delete")}</Button> : null}
                  </div></TableCell>
                </TableRow>
              ))}
              {!catalog.list.data?.length ? <TableRow><TableCell colSpan={5}>{t("catalogEmpty")}</TableCell></TableRow> : null}
            </TableBody>
          </Table>
        )}
      </CardContent>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader><DialogTitle>{editing ? t("catalogEdit") : t("catalogCreate")}</DialogTitle><DialogDescription>{t("catalogEditDescription")}</DialogDescription></DialogHeader>
          <form className="flex max-h-[75vh] flex-col gap-3 overflow-y-auto" onSubmit={event => { event.preventDefault(); void save() }}>
            <div className="grid gap-1"><Label htmlFor="ai-agent-key">{t("catalogKey")}</Label><Input id="ai-agent-key" required pattern="[a-z][a-z0-9_]{0,63}" disabled={editing} value={draft.key} onChange={event => setDraft({ ...draft, key: event.target.value })} /></div>
            <div className="grid gap-1"><Label htmlFor="ai-agent-name">{t("catalogName")}</Label><Input id="ai-agent-name" required disabled={draft.builtin} maxLength={120} value={draft.name} onChange={event => setDraft({ ...draft, name: event.target.value })} /></div>
            <div className="grid gap-1"><Label htmlFor="ai-agent-description">{t("catalogDescriptionLabel")}</Label><Textarea id="ai-agent-description" disabled={draft.builtin} maxLength={4096} rows={3} value={draft.description} onChange={event => setDraft({ ...draft, description: event.target.value })} /></div>
            <div className="grid gap-1"><Label htmlFor="ai-agent-prompt">{t("catalogPrompt")}</Label><Textarea id="ai-agent-prompt" maxLength={65536} rows={8} value={draft.systemPrompt} onChange={event => setDraft({ ...draft, systemPrompt: event.target.value })} /><p className="text-xs text-muted-foreground">{t("catalogPromptHelp")}</p></div>
            <p className="text-xs text-muted-foreground">{t("catalogVariables")}: {promptVariables(draft.key).map(name => `{{.${name}}}`).join(", ")}</p>
            {editing ? <div className="flex flex-wrap items-center gap-2">
              {draft.builtin ? <Button type="button" variant="outline" size="sm" onClick={() => setDraft({ ...draft, systemPrompt: draft.defaultPrompt })}>{t("catalogRestore")}</Button> : null}
              <Button type="button" variant="outline" size="sm" onClick={() => void previewPrompt()}>{t("catalogPreview")}</Button>
              {versions.length > 0 ? <Select onValueChange={(value: string) => { const version = versions.find(item => String(item.version) === value); if (version) setDraft({ ...draft, systemPrompt: version.template }) }}>
                <SelectTrigger className="w-48"><SelectValue placeholder={t("catalogHistory")} /></SelectTrigger>
                <SelectContent>{versions.map(version => <SelectItem key={version.version} value={String(version.version)}>v{version.version} · {version.createdAt.startsWith("0001-") ? t("catalogInitialVersion") : new Date(version.createdAt).toLocaleString()}</SelectItem>)}</SelectContent>
              </Select> : null}
            </div> : null}
            {preview !== null ? <pre className="max-h-48 overflow-auto whitespace-pre-wrap rounded border p-2 text-xs">{preview}</pre> : null}
            {editing ? <>
              <div className="grid gap-1"><Label htmlFor="ai-agent-llm">{t("catalogModel")}</Label>
                <Select value={draft.llmProfileId || "__default__"} onValueChange={(value: string) => setDraft({ ...draft, llmProfileId: value === "__default__" ? "" : value })} disabled={profiles.list.isPending || Boolean(profiles.list.error)}>
                  <SelectTrigger id="ai-agent-llm"><SelectValue /></SelectTrigger>
                  <SelectContent><SelectItem value="__default__">{t("catalogModelDefault")}</SelectItem>{profiles.list.data?.map(profile => <SelectItem key={profile.id} value={profile.id}>{profile.displayName} ({profile.model})</SelectItem>)}</SelectContent>
                </Select>
                {profiles.list.error ? <p className="text-xs text-destructive">{t("catalogModelLoadError")}</p> : null}
              </div>
              <div className="flex items-center gap-2"><Switch id="ai-agent-enabled" checked={draft.enabled} onCheckedChange={enabled => setDraft({ ...draft, enabled })} /><Label htmlFor="ai-agent-enabled">{t("enabled")}</Label></div>
              <div className="grid grid-cols-2 gap-3">
                <div className="grid gap-1"><Label htmlFor="ai-agent-turns">{t("turns")}</Label><Input id="ai-agent-turns" type="number" min={0} max={1000} value={draft.maxTurns} onChange={event => setDraft({ ...draft, maxTurns: Number(event.target.value) })} /></div>
                <div className="grid gap-1"><Label htmlFor="ai-agent-seconds">{t("seconds")}</Label><Input id="ai-agent-seconds" type="number" min={0} max={86400} value={draft.runSeconds} onChange={event => setDraft({ ...draft, runSeconds: Number(event.target.value) })} /></div>
              </div>
              <details className="rounded border p-3"><summary className="cursor-pointer text-sm font-medium">{t("catalogWrapup")}</summary>
                <div className="mt-3 grid gap-2">
                  <Label htmlFor="ai-agent-wrapup">{t("catalogWrapupPrompt")}</Label>
                  <Textarea id="ai-agent-wrapup" maxLength={32768} rows={4} value={draft.wrapupPrompt} placeholder={draft.defaultWrapupPrompt} onChange={event => setDraft({ ...draft, wrapupPrompt: event.target.value })} />
                  <Label htmlFor="ai-agent-wrapup-turns">{t("catalogWrapupTurns", { count: draft.defaultWrapupMaxTurns })}</Label>
                  <Input id="ai-agent-wrapup-turns" type="number" min={0} max={1000} value={draft.wrapupMaxTurns} onChange={event => setDraft({ ...draft, wrapupMaxTurns: Number(event.target.value) })} />
                  {draft.defaultTaskTimeoutPrompt ? <>
                    <Label htmlFor="ai-agent-task-timeout">{t("catalogTaskWrapupPrompt")}</Label>
                    <Textarea id="ai-agent-task-timeout" maxLength={32768} rows={4} value={draft.taskTimeoutPrompt} placeholder={draft.defaultTaskTimeoutPrompt} onChange={event => setDraft({ ...draft, taskTimeoutPrompt: event.target.value })} />
                    <Label htmlFor="ai-agent-task-turns">{t("catalogTaskWrapupTurns")}</Label>
                    <Input id="ai-agent-task-turns" type="number" min={0} max={1000} value={draft.taskTimeoutMaxTurns} onChange={event => setDraft({ ...draft, taskTimeoutMaxTurns: Number(event.target.value) })} />
                  </> : null}
                </div>
              </details>
            </> : null}
            <DialogFooter><Button type="submit" disabled={busy || (editing && (profiles.list.isPending || Boolean(profiles.list.error)))}>{busy ? t("saving") : t("save")}</Button></DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </Card>
  )
}
