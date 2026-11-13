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
import { useAIMCP } from "@/hooks/use-ai-mcp"
import { useAIAgentCatalog } from "@/hooks/use-ai-agent-catalog"
import { normalizeError } from "@/lib/errors/normalize-error"
import type { AIMCPInput, AIMCPServer, AIMCPTool } from "@/types/ai-mcp.types"

const empty: AIMCPInput = { displayName: "", endpoint: "", enabled: false, insecure: false, agents: [] }
export function AIMCPSettings() {
  const t = useTranslations("aiMcp")
  const { list, save, remove, test } = useAIMCP()
  const agentCatalog = useAIAgentCatalog()
  const agents = (agentCatalog.list.data ?? []).map(agent => agent.key)
  const [open, setOpen] = React.useState(false)
  const [editing, setEditing] = React.useState<AIMCPServer | null>(null)
  const [pendingDelete, setPendingDelete] = React.useState<AIMCPServer | null>(null)
  const [input, setInput] = React.useState<AIMCPInput>(empty)
  const [headers, setHeaders] = React.useState("")
  const [discovered, setDiscovered] = React.useState<AIMCPTool[] | null>(null)
  const edit = (item?: AIMCPServer) => { setEditing(item ?? null); setHeaders(""); setInput(item ? { displayName: item.displayName, endpoint: item.endpoint, enabled: item.enabled, insecure: item.insecure, agents: item.agents } : { ...empty }); setOpen(true) }
  const submit = async () => {
    let parsed: Record<string, string> | undefined
    if (headers.trim()) {
      try {
        const value: unknown = JSON.parse(headers)
        if (!value || typeof value !== "object" || Array.isArray(value) || Object.values(value).some(item => typeof item !== "string")) throw new Error("invalid")
        parsed = value as Record<string, string>
      } catch { toast.error(t("headersInvalid")); return }
    }
    if (input.clearHeaders && parsed && Object.keys(parsed).length) { toast.error(t("headersConflict")); return }
    try { await save.mutateAsync({ id: editing?.id ?? null, input: { ...input, headers: parsed } }); setHeaders(""); setOpen(false); toast.success(t("saved")) }
    catch { toast.error(t("saveError")) }
  }
  if (list.error) return <AppErrorState error={normalizeError(list.error, { notFoundKind: "unexpected-error" })} onRetry={list.refetch} />
  return <ContentHandoff owner="ai-mcp-settings-content" layer="workspace" isLoading={list.isPending}
    skeleton={<div className="flex flex-col gap-2" aria-label={t("loading")}>{[0, 1, 2].map(row => <Skeleton key={row} className="h-10 w-full" />)}</div>}>
    <div className="flex min-w-0 flex-col gap-3"><div className="flex justify-end"><Button onClick={() => edit()}>{t("create")}</Button></div>
      <Table><TableHeader><TableRow><TableHead>{t("name")}</TableHead><TableHead>{t("endpoint")}</TableHead><TableHead>{t("agents")}</TableHead><TableHead>{t("status")}</TableHead><TableHead /></TableRow></TableHeader>
        <TableBody>{list.data?.map(item => <TableRow key={item.id}><TableCell>{item.displayName}</TableCell><TableCell className="max-w-md break-all">{item.endpoint}</TableCell>
          <TableCell>{item.agents.map(agent => agentCatalog.list.data?.find(candidate => candidate.key === agent)?.name ?? agent).join("、") || "—"}</TableCell><TableCell><Badge variant={item.enabled ? "success" : "outline"}>{item.enabled ? t("enabled") : t("disabled")}</Badge></TableCell>
          <TableCell><div className="flex flex-wrap justify-end gap-2"><Button variant="outline" size="sm" disabled={test.isPending} onClick={async () => { try { setDiscovered(await test.mutateAsync(item.id)) } catch { toast.error(t("testError")) } }}>{t("test")}</Button><Button variant="outline" size="sm" onClick={() => edit(item)}>{t("edit")}</Button><Button variant="outline" size="sm" onClick={() => setPendingDelete(item)}>{t("delete")}</Button></div></TableCell>
        </TableRow>)}{!list.data?.length && <TableRow><TableCell colSpan={5}>{t("empty")}</TableCell></TableRow>}</TableBody>
      </Table>
    </div>
    <Dialog open={open} onOpenChange={value => { setOpen(value); if (!value) setHeaders("") }}><DialogContent><DialogHeader><DialogTitle>{editing ? t("edit") : t("create")}</DialogTitle><DialogDescription>{t("editDescription")}</DialogDescription></DialogHeader>
      <form className="flex max-h-150 flex-col gap-3 overflow-y-auto" onSubmit={event => { event.preventDefault(); void submit() }}>
        <div className="flex flex-col gap-1"><Label htmlFor="mcp-name">{t("name")}</Label><Input id="mcp-name" required pattern="[A-Za-z0-9_-]{1,48}" value={input.displayName} onChange={event => setInput({ ...input, displayName: event.target.value })} /><p className="text-sm text-muted-foreground">{t("nameHint")}</p></div>
        <div className="flex flex-col gap-1"><Label htmlFor="mcp-endpoint">{t("endpoint")}</Label><Input id="mcp-endpoint" type="url" required value={input.endpoint} onChange={event => setInput({ ...input, endpoint: event.target.value })} /></div>
        <div className="flex flex-col gap-1"><Label htmlFor="mcp-headers">{t("headers")}</Label><Textarea id="mcp-headers" autoComplete="off" value={headers} onChange={event => setHeaders(event.target.value)} placeholder={editing?.hasHeaders ? t("keepHeaders") : t("headersExample")} /></div>
        {editing?.hasHeaders && <label className="flex items-center gap-2 text-sm"><Checkbox checked={Boolean(input.clearHeaders)} onCheckedChange={clearHeaders => setInput({ ...input, clearHeaders })} />{t("clearHeaders")}</label>}
        <fieldset className="flex flex-wrap gap-3"><legend className="mb-1 text-sm font-medium">{t("agents")}</legend>{agents.map(agent => <label key={agent} className="flex items-center gap-1 text-sm"><Checkbox checked={input.agents.includes(agent)} onCheckedChange={checked => setInput({ ...input, agents: checked ? [...input.agents, agent] : input.agents.filter(value => value !== agent) })} />{agentCatalog.list.data?.find(candidate => candidate.key === agent)?.name ?? agent}</label>)}</fieldset>
        <div className="flex items-center gap-2"><Switch id="mcp-enabled" checked={input.enabled} onCheckedChange={enabled => setInput({ ...input, enabled })} /><Label htmlFor="mcp-enabled">{t("enabled")}</Label></div>
        <div className="flex items-center gap-2"><Switch id="mcp-insecure" checked={input.insecure} onCheckedChange={insecure => setInput({ ...input, insecure })} /><Label htmlFor="mcp-insecure">{t("insecure")}</Label></div>
        <DialogFooter><Button type="submit" disabled={save.isPending}>{t("save")}</Button></DialogFooter>
      </form>
    </DialogContent></Dialog>
    <Dialog open={discovered !== null} onOpenChange={value => { if (!value) setDiscovered(null) }}><DialogContent><DialogHeader><DialogTitle>{t("testResult")}</DialogTitle><DialogDescription>{t("testDescription")}</DialogDescription></DialogHeader>
      <div className="flex max-h-150 flex-col gap-2 overflow-auto">{discovered?.map(item => <div key={item.name} className="rounded-md border p-2 text-sm"><div className="font-medium">{item.name}</div><p className="text-muted-foreground">{item.description}</p></div>)}{!discovered?.length && <p className="text-sm text-muted-foreground">{t("noTools")}</p>}</div>
    </DialogContent></Dialog>
    <AlertDialog open={Boolean(pendingDelete)} onOpenChange={value => { if (!value) setPendingDelete(null) }}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{t("deleteTitle")}</AlertDialogTitle><AlertDialogDescription>{t("deleteDescription")}</AlertDialogDescription></AlertDialogHeader>
      <AlertDialogFooter><AlertDialogClose variant="outline">{t("cancel")}</AlertDialogClose><Button variant="destructive" disabled={remove.isPending} onClick={async () => { if (!pendingDelete) return; try { await remove.mutateAsync(pendingDelete.id); setPendingDelete(null); toast.success(t("deleted")) } catch { toast.error(t("saveError")) } }}>{t("delete")}</Button></AlertDialogFooter>
    </AlertDialogContent></AlertDialog>
  </ContentHandoff>
}
