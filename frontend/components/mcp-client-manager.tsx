"use client"

import * as React from "react"
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { CopyButton } from "@/components/shared/feedback/copy-button"
import { ConfirmDialog } from "@/components/shared/feedback/confirm-dialog"
import { useAuth } from "@/hooks/use-auth"
import { mcpKeyKeys } from "@/hooks/use-mcp-key"
import { OrganizationService } from "@/services/organization.service"
import { diagnoseMcp, mcpClients, type McpClient, type McpPolicy, type Diagnostic } from "@/services/mcp-clients.service"

const empty = (): McpPolicy => ({ name: "", enabled: true, allOrganizations: false, tools: [], organizationIds: [] })
function toggle<T>(items: T[], item: T) { return items.includes(item) ? items.filter(value => value !== item) : [...items, item] }

export function McpClientManager({ endpoint, initialKey, onClose }: { endpoint: string; initialKey: string; onClose: () => void }) {
  const t = useTranslations("mcpClients")
  const user = useAuth().data?.user?.id
  const cache = useQueryClient()
  const catalog = useQuery({ queryKey: ["mcp-clients", user], queryFn: mcpClients.list, enabled: !!user, gcTime: 0 })
  const organizations = useInfiniteQuery({
    queryKey: ["mcp-organization-options", user], enabled: !!user,
    initialPageParam: "", queryFn: ({ pageParam }) => OrganizationService.getOrganizations({ pageSize: 100, pageToken: pageParam || undefined }),
    getNextPageParam: page => page.nextPageToken || undefined,
  })
  const [id, setId] = React.useState<number>()
  const [policy, setPolicy] = React.useState<McpPolicy>(empty)
  const [key, setKey] = React.useState(initialKey)
  const [message, setMessage] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const [confirm, setConfirm] = React.useState<"revoke" | "rotate" | null>(null)
  const [diagnostic, setDiagnostic] = React.useState<Diagnostic>()
  const controller = React.useRef<AbortController | null>(null)
  React.useEffect(() => () => controller.current?.abort(), [])
  const selected = catalog.data?.clients.find(item => item.id === id)
  const previewed = React.useRef(false)
  React.useEffect(() => {
    if (previewed.current || !catalog.data) return
    previewed.current = true
    const first = catalog.data.clients.find(item => item.isDefault) ?? catalog.data.clients[0]
    if (first) { setId(first.id); setPolicy(first) }
  }, [catalog.data])

  function select(item?: McpClient) {
    setId(item?.id); setPolicy(item ?? empty()); setKey(""); setDiagnostic(undefined); setMessage("")
  }
  async function change(action: "save" | "rotate" | "revoke") {
    setBusy(true); setMessage(""); setDiagnostic(undefined)
    try {
      if (action === "save" && id) await mcpClients.update(id, policy)
      if (action === "save" && !id) {
        const created = await mcpClients.create(policy)
        setId(created.id); setKey(created.key)
      }
      if (action === "rotate" && id) setKey((await mcpClients.rotate(id)).key)
      if (action === "revoke" && id) { await mcpClients.revoke(id); select() }
      await cache.invalidateQueries({ queryKey: mcpKeyKeys.status })
      await catalog.refetch()
      setMessage(t(action === "revoke" ? "revoked" : action === "rotate" || !id ? "rotated" : "saved"))
    } catch (error) {
      setMessage(error instanceof Error ? error.message : t("failed"))
    } finally { setBusy(false); setConfirm(null) }
  }
  async function probe() {
    setDiagnostic(undefined); setBusy(true)
    const pending = new AbortController()
    controller.current = pending
    const timeout = window.setTimeout(() => pending.abort(), 12000)
    try { setDiagnostic(await diagnoseMcp(key.trim(), pending.signal)) }
    finally { window.clearTimeout(timeout); setBusy(false) }
  }
  const options = organizations.data?.pages.flatMap(page => page.results) ?? []
  return <Dialog open onOpenChange={open => { if (!open && !busy) onClose() }}>
    <DialogContent className="flex max-h-[88vh] flex-col gap-3 overflow-y-auto sm:max-w-2xl">
      <DialogHeader><DialogTitle>{t("title")}</DialogTitle></DialogHeader>
      <fieldset disabled={busy} className="space-y-4 text-sm">
        <label className="grid gap-1.5">{t("client")}
          <select className="h-9 rounded-md border bg-background px-2" value={id ?? ""} onChange={e => select(catalog.data?.clients.find(item => item.id === Number(e.target.value)))}>
            <option value="">{t("new")}</option>
            {catalog.data?.clients.map(item => <option key={item.id} value={item.id}>{item.name} · #{item.id}</option>)}
          </select>
        </label>
        {catalog.isError && <p role="alert">{t("loadFailed")} <Button size="sm" variant="link" onClick={() => void catalog.refetch()}>{t("retry")}</Button></p>}
        {selected?.legacy && <p role="status" className="text-muted-foreground">{t("legacy")}</p>}
        <label className="grid gap-1.5">{t("name")}<Input maxLength={100} value={policy.name} onChange={e => setPolicy({ ...policy, name: e.target.value })} /></label>
        <label className="flex items-center gap-2"><input type="checkbox" checked={policy.enabled} onChange={e => setPolicy({ ...policy, enabled: e.target.checked })} />{t("enabled")}</label>
        <fieldset className="space-y-2"><legend className="font-medium">{t("organizations")}</legend>
          <label className="flex items-center gap-2"><input type="checkbox" checked={policy.allOrganizations} onChange={e => setPolicy({ ...policy, allOrganizations: e.target.checked, organizationIds: [] })} />{t("all")}</label>
          {!policy.allOrganizations && <div className="grid max-h-36 gap-1.5 overflow-auto rounded-md border p-2 sm:grid-cols-2">
            {options.map(org => <label key={org.id} className="flex items-center gap-2"><input type="checkbox" checked={policy.organizationIds.includes(org.id)} onChange={() => setPolicy({ ...policy, organizationIds: toggle(policy.organizationIds, org.id) })} />{org.name}</label>)}
            {organizations.isError && <Button size="sm" variant="link" onClick={() => void organizations.refetch()}>{t("retry")}</Button>}
            {organizations.hasNextPage && <Button size="sm" variant="link" onClick={() => void organizations.fetchNextPage()}>{t("more")}</Button>}
          </div>}
        </fieldset>
        <fieldset><legend className="mb-2 font-medium">{t("tools")}</legend>
          <div className="grid gap-2 sm:grid-cols-2">{catalog.data?.availableTools.map(tool =>
            <label key={tool} className="flex items-center gap-2" title={tool}><input type="checkbox" checked={policy.tools.includes(tool)} onChange={() => setPolicy({ ...policy, tools: toggle(policy.tools, tool) })} />{t(`tool.${tool}`)}</label>)}</div>
        </fieldset>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" disabled={!policy.name.trim() || !catalog.data} onClick={() => void change("save")}>{t(id ? "save" : "create")}</Button>
          {id && <><Button size="sm" variant="outline" onClick={() => setConfirm("rotate")}>{t("rotate")}</Button><Button size="sm" variant="destructive" onClick={() => setConfirm("revoke")}>{t("revoke")}</Button></>}
        </div>
        {message && <p role="status">{message}</p>}
        <div className="space-y-2 border-t pt-3">
          <h3 className="font-medium">{t("diagnostics")}</h3>
          <code className="break-all">{endpoint}</code>
          <label className="grid gap-1.5">{t("key")}<Input type="password" autoComplete="off" spellCheck={false} value={key} placeholder={t("keyHint")} onChange={e => {setKey(e.target.value);setDiagnostic(undefined)}} /></label>
          <div className="flex items-center gap-2"><Button size="sm" variant="outline" onClick={() => void probe()}>{t("test")}</Button>
            {key && <CopyButton value={key} copyLabel={t("copy")} copiedLabel={t("copied")} copyFailedLabel={t("copyFailed")} />}
          </div>
          {diagnostic && <p role="status" className="rounded-md bg-muted p-2">{t(`diagnosis.${diagnostic.state}`)} · {t(diagnostic.stage === "tools" ? "discovery" : "handshake")}
            {diagnostic.status && ` · HTTP ${diagnostic.status}`}{diagnostic.version && ` · MCP ${diagnostic.version}`}
            {diagnostic.count !== undefined && ` · ${t("count", { count: diagnostic.count })}`}</p>}
        </div>
      </fieldset>
      <ConfirmDialog open={confirm !== null} onOpenChange={open => {if (!open) setConfirm(null)}} title={t(confirm === "revoke" ? "revoke" : "rotate")}
        description={t(confirm === "revoke" ? "revokeHint" : "rotateHint")} variant="destructive" loading={busy}
        onConfirm={() => confirm ? change(confirm) : undefined} />
    </DialogContent>
  </Dialog>
}
