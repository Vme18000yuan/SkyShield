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
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { useAISkills } from "@/hooks/use-ai-skills"
import { useAIAgentCatalog } from "@/hooks/use-ai-agent-catalog"
import { normalizeError } from "@/lib/errors/normalize-error"
import type { AISkill, AISkillInput } from "@/types/ai-skill.types"

const empty: AISkillInput = { displayName: "", description: "", instructions: "", enabled: true, agents: [] }
export function AISkillSettings() {
  const t = useTranslations("aiSkills")
  const { list, save, remove } = useAISkills()
  const agentCatalog = useAIAgentCatalog()
  const agents = (agentCatalog.list.data ?? []).map(agent => agent.key)
  const [open, setOpen] = React.useState(false)
  const [editing, setEditing] = React.useState<AISkill | null>(null)
  const [pendingDelete, setPendingDelete] = React.useState<AISkill | null>(null)
  const [input, setInput] = React.useState<AISkillInput>(empty)
  const edit = (item?: AISkill) => { setEditing(item ?? null); setInput(item ? { displayName: item.displayName, description: item.description, instructions: item.instructions, enabled: item.enabled, agents: item.agents } : { ...empty }); setOpen(true) }
  const submit = async () => {
    try { await save.mutateAsync({ id: editing?.id ?? null, input }); setOpen(false); toast.success(t("saved")) }
    catch { toast.error(t("saveError")) }
  }
  if (list.error) return <AppErrorState error={normalizeError(list.error, { notFoundKind: "unexpected-error" })} onRetry={list.refetch} />
  return <ContentHandoff owner="ai-skill-settings-content" layer="workspace" isLoading={list.isPending}
    skeleton={<div className="flex flex-col gap-2" aria-label={t("loading")}>{[0, 1, 2].map(row => <Skeleton key={row} className="h-10 w-full" />)}</div>}>
    <div className="flex min-w-0 flex-col gap-3"><div className="flex justify-end"><Button onClick={() => edit()}>{t("create")}</Button></div>
      <Table><TableHeader><TableRow><TableHead>{t("name")}</TableHead><TableHead>{t("summary")}</TableHead><TableHead>{t("agents")}</TableHead><TableHead>{t("status")}</TableHead><TableHead /></TableRow></TableHeader>
        <TableBody>{list.data?.map(item => <TableRow key={item.id}><TableCell><div className="flex items-center gap-2">{item.displayName}{item.builtin ? <Badge variant="outline">{t("builtin")}</Badge> : null}</div></TableCell><TableCell className="max-w-md break-words">{item.description}</TableCell>
          <TableCell>{item.agents.map(agent => agentCatalog.list.data?.find(candidate => candidate.key === agent)?.name ?? agent).join("、") || "—"}</TableCell><TableCell><Badge variant={item.enabled ? "success" : "outline"}>{item.enabled ? t("enabled") : t("disabled")}</Badge></TableCell>
          <TableCell><div className="flex justify-end gap-2"><Button variant="outline" size="sm" onClick={() => edit(item)}>{t("edit")}</Button>{!item.builtin ? <Button variant="outline" size="sm" onClick={() => setPendingDelete(item)}>{t("delete")}</Button> : null}</div></TableCell>
        </TableRow>)}{!list.data?.length && <TableRow><TableCell colSpan={5}>{t("empty")}</TableCell></TableRow>}</TableBody>
      </Table>
    </div>
    <Dialog open={open} onOpenChange={setOpen}><DialogContent><DialogHeader><DialogTitle>{editing ? t("edit") : t("create")}</DialogTitle><DialogDescription>{t("editDescription")}</DialogDescription></DialogHeader>
      <form className="flex max-h-150 flex-col gap-3 overflow-y-auto" onSubmit={event => { event.preventDefault(); void submit() }}>
        <div className="flex flex-col gap-1"><Label htmlFor="skill-name">{t("name")}</Label><Input id="skill-name" required maxLength={120} disabled={Boolean(editing?.builtin)} value={input.displayName} onChange={event => setInput({ ...input, displayName: event.target.value })} /></div>
        <div className="flex flex-col gap-1"><Label htmlFor="skill-description">{t("summary")}</Label><Input id="skill-description" disabled={Boolean(editing?.builtin)} value={input.description} onChange={event => setInput({ ...input, description: event.target.value })} /></div>
        <div className="flex flex-col gap-1"><Label htmlFor="skill-instructions">{t("instructions")}</Label><Textarea id="skill-instructions" required rows={10} disabled={Boolean(editing?.builtin)} value={input.instructions} onChange={event => setInput({ ...input, instructions: event.target.value })} /></div>
        <fieldset className="flex flex-wrap gap-3"><legend className="mb-1 text-sm font-medium">{t("agents")}</legend>{agents.map(agent => <label key={agent} className="flex items-center gap-1 text-sm"><Checkbox checked={input.agents.includes(agent)} onCheckedChange={checked => setInput({ ...input, agents: checked ? [...input.agents, agent] : input.agents.filter(value => value !== agent) })} />{agentCatalog.list.data?.find(candidate => candidate.key === agent)?.name ?? agent}</label>)}</fieldset>
        <div className="flex items-center gap-2"><Switch id="skill-enabled" checked={input.enabled} onCheckedChange={enabled => setInput({ ...input, enabled })} /><Label htmlFor="skill-enabled">{t("enabled")}</Label></div>
        <DialogFooter><Button type="submit" disabled={save.isPending}>{t("save")}</Button></DialogFooter>
      </form>
    </DialogContent></Dialog>
    <AlertDialog open={Boolean(pendingDelete)} onOpenChange={value => { if (!value) setPendingDelete(null) }}><AlertDialogContent>
      <AlertDialogHeader><AlertDialogTitle>{t("deleteTitle")}</AlertDialogTitle><AlertDialogDescription>{t("deleteDescription")}</AlertDialogDescription></AlertDialogHeader>
      <AlertDialogFooter><AlertDialogClose variant="outline">{t("cancel")}</AlertDialogClose><Button variant="destructive" disabled={remove.isPending} onClick={async () => { if (!pendingDelete) return; try { await remove.mutateAsync(pendingDelete.id); setPendingDelete(null); toast.success(t("deleted")) } catch { toast.error(t("saveError")) } }}>{t("delete")}</Button></AlertDialogFooter>
    </AlertDialogContent></AlertDialog>
  </ContentHandoff>
}
