"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { ContentHandoff } from "@/components/shared/loading/content-handoff"
import { AlertDialog, AlertDialogClose, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useAILLMProfiles } from "@/hooks/use-ai-llm-profiles"
import { normalizeError } from "@/lib/errors/normalize-error"
import type { LLMProfile, LLMProfileInput, LLMProvider } from "@/types/ai-llm-profile.types"

const defaultModels: Record<LLMProvider, string> = {
  anthropic: "claude-opus-4-8",
  openai: "gpt-4o",
  "openai-responses": "gpt-5",
}
const emptyProfile: LLMProfileInput = {
  name: "",
  provider: "anthropic",
  model: defaultModels.anthropic,
  baseUrl: "",
  apiKey: "",
  isDefault: false,
}

export function LLMProfileSettings() {
  const t = useTranslations("aiSettings")
  const { list, save, remove, test } = useAILLMProfiles()
  const [open, setOpen] = React.useState(false)
  const [editing, setEditing] = React.useState<LLMProfile | null>(null)
  const [pendingDelete, setPendingDelete] = React.useState<LLMProfile | null>(null)
  const [input, setInput] = React.useState<LLMProfileInput>(emptyProfile)

  const beginEdit = (profile?: LLMProfile) => {
    setEditing(profile ?? null)
    setInput(profile ? { name: profile.displayName, provider: profile.provider, model: profile.model,
      baseUrl: profile.baseUrl, apiKey: "", isDefault: profile.isDefault } : { ...emptyProfile, isDefault: !list.data?.length })
    setOpen(true)
  }

  const saveProfile = async () => {
    try {
      await save.mutateAsync({ id: editing?.id ?? null, input })
      setOpen(false)
      setInput(emptyProfile)
      setEditing(null)
      toast.success(t("saved"))
    } catch { toast.error(t("error")) }
    finally { save.reset() }
  }

  const deleteProfile = async () => {
    if (!pendingDelete) return
    try { await remove.mutateAsync(pendingDelete.id); setPendingDelete(null); toast.success(t("deleted")) }
    catch { toast.error(t("error")) }
  }

  if (list.error) return <AppErrorState error={normalizeError(list.error, { notFoundKind: "unexpected-error" })} onRetry={list.refetch} />
  return (
    <ContentHandoff owner="ai-llm-profile-content" layer="workspace" isLoading={list.isPending}
      skeleton={<div className="flex flex-col gap-2" aria-label={t("loading")}>{[0, 1, 2].map(row => <Skeleton key={row} className="h-10 w-full" />)}</div>}>
      <div className="flex flex-col gap-3">
        <div className="flex justify-end"><Button onClick={() => beginEdit()}>{t("add")}</Button></div>
        <Table>
          <TableHeader><TableRow><TableHead>{t("name")}</TableHead><TableHead>{t("provider")}</TableHead><TableHead>{t("model")}</TableHead><TableHead>{t("baseUrl")}</TableHead><TableHead /></TableRow></TableHeader>
          <TableBody>
            {list.data?.map(profile => <TableRow key={profile.id}>
              <TableCell>{profile.displayName} {profile.isDefault && <Badge variant="secondary">{t("defaultBadge")}</Badge>}</TableCell>
              <TableCell>{profile.provider}</TableCell><TableCell>{profile.model}</TableCell><TableCell>{profile.baseUrl || "—"}</TableCell>
              <TableCell><div className="flex justify-end gap-2">
                <Button variant="outline" size="sm" disabled={test.isPending} onClick={async () => {
                  try { const result = await test.mutateAsync(profile.id); toast.success(t("testSuccess", { ms: result.latencyMs })) }
                  catch { toast.error(t("error")) }
                }}>{t("test")}</Button>
                <Button variant="outline" size="sm" onClick={() => beginEdit(profile)}>{t("edit")}</Button>
                <Button variant="outline" size="sm" onClick={() => setPendingDelete(profile)}>{t("delete")}</Button>
              </div></TableCell>
            </TableRow>)}
            {!list.data?.length && <TableRow><TableCell colSpan={5}>{t("empty")}</TableCell></TableRow>}
          </TableBody>
        </Table>
      </div>
      <Dialog open={open} onOpenChange={value => {
        setOpen(value)
        if (!value) { setInput(emptyProfile); setEditing(null); save.reset() }
      }}>
        <DialogContent>
          <DialogHeader><DialogTitle>{editing ? t("edit") : t("add")}</DialogTitle><DialogDescription>{t("description")}</DialogDescription></DialogHeader>
          <form className="flex flex-col gap-3" onSubmit={event => { event.preventDefault(); void saveProfile() }}>
            <div className="flex flex-col gap-1"><Label htmlFor="ai-profile-name">{t("name")}</Label><Input id="ai-profile-name" required value={input.name} onChange={event => setInput({ ...input, name: event.target.value })} /></div>
            <div className="flex flex-col gap-1"><Label htmlFor="ai-profile-provider">{t("provider")}</Label>
              <Select value={input.provider} onValueChange={(value: LLMProvider) => setInput(current => ({
                ...current,
                provider: value,
                model: !current.model.trim() || Object.values(defaultModels).includes(current.model.trim())
                  ? defaultModels[value]
                  : current.model,
              }))}>
                <SelectTrigger id="ai-profile-provider"><SelectValue /></SelectTrigger>
                <SelectContent><SelectItem value="openai">OpenAI</SelectItem><SelectItem value="openai-responses">OpenAI Responses</SelectItem><SelectItem value="anthropic">Anthropic</SelectItem></SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1"><Label htmlFor="ai-profile-model">{t("model")}</Label><Input id="ai-profile-model" required value={input.model} onChange={event => setInput({ ...input, model: event.target.value })} /></div>
            <div className="flex flex-col gap-1"><Label htmlFor="ai-profile-url">{t("baseUrl")}</Label><Input id="ai-profile-url" value={input.baseUrl} onChange={event => setInput({ ...input, baseUrl: event.target.value })} /></div>
            <div className="flex flex-col gap-1"><Label htmlFor="ai-profile-key">{t("apiKey")}</Label><Input id="ai-profile-key" type="password" autoComplete="new-password" required={!editing} placeholder={editing ? t("keepKey") : undefined} value={input.apiKey} onChange={event => setInput({ ...input, apiKey: event.target.value })} /></div>
            <label className="flex items-center gap-2"><Checkbox checked={input.isDefault} onCheckedChange={checked => setInput({ ...input, isDefault: checked })} />{t("default")}</label>
            <DialogFooter><Button type="submit" disabled={save.isPending}>{t("save")}</Button></DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <AlertDialog open={Boolean(pendingDelete)} onOpenChange={value => { if (!value) setPendingDelete(null) }}>
        <AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{t("deleteTitle")}</AlertDialogTitle><AlertDialogDescription>{t("deleteDescription")}</AlertDialogDescription></AlertDialogHeader>
          <AlertDialogFooter><AlertDialogClose variant="outline">{t("cancel")}</AlertDialogClose><Button variant="destructive" onClick={deleteProfile} disabled={remove.isPending}>{t("confirmDelete")}</Button></AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </ContentHandoff>
  )
}
