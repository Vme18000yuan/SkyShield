// Adapted from ARTEX, AGPL-3.0-only; see third_party/artex/SKYSHIELD-NOTICE.md.
"use client"

import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { PlusIcon, TargetIcon, Trash2Icon } from "lucide-react"
import { useOrganizations } from "@/hooks/use-organizations"
import { api } from "@/lib/api-client"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import type { Organization } from "@/types/organization.types"

type ScopeKind = "company" | "root_domain" | "subdomain" | "ip" | "cidr" | "icp" | "keyword"
type Scope = { id: string; task_id: string; kind: ScopeKind; company_name?: string; domain?: string; net?: string; value?: string; source: string; reason?: string; inherited?: boolean; source_task_id?: string }
type Coverage = { enabled: boolean; scope_rows: number; denominator: number; tested: number; pct: number | null; by_type: { type: string; total: number; tested: number }[] }
const labels: Record<ScopeKind, string> = { company: "组织", root_domain: "根域名", subdomain: "子域名", ip: "IP", cidr: "CIDR 网段", icp: "ICP备案", keyword: "关键词" }
const assetLabels: Record<string, string> = { root_domain: "根域名", subdomain: "子域名", ip: "IP", service: "服务", endpoint: "接口", app: "应用" }

export function TaskScopeCard({ taskId, sourceNames, running }: { taskId: string; sourceNames: Record<string, string>; running: boolean }) {
  const cache = useQueryClient()
  const base = `/aiTasks/${encodeURIComponent(taskId)}`
  const scopeKey = ["aiTaskScope", taskId]
  const coverageKey = ["aiTaskCoverage", taskId]
  const scopes = useQuery({ queryKey: scopeKey, queryFn: async () => (await api.get<{ scope: Scope[] }>(`${base}/scope`)).data.scope ?? [], refetchInterval: running ? 15000 : false })
  const coverage = useQuery({ queryKey: coverageKey, queryFn: async () => (await api.get<Coverage>(`${base}/coverage`)).data, refetchInterval: running ? 15000 : false })
  const [kind, setKind] = React.useState<ScopeKind>("root_domain")
  const [value, setValue] = React.useState("")
  const [reason, setReason] = React.useState("")
  const [error, setError] = React.useState("")
  const [organization, setOrganization] = React.useState<Organization | null>(null)
  const [search, setSearch] = React.useState("")
  const [query, setQuery] = React.useState("")
  const [tokens, setTokens] = React.useState([""])
  const [page, setPage] = React.useState(0)
  const organizations = useOrganizations({ pageSize: 20, pageToken: tokens[page] || undefined, filter: query ? `displayName=${JSON.stringify(query)}` : undefined }, { enabled: kind === "company" })
  React.useEffect(() => { const timer = window.setTimeout(() => { setQuery(search.trim()); setTokens([""]); setPage(0) }, 300); return () => window.clearTimeout(timer) }, [search])
  const choices = [...(organization && !organizations.data?.organizations.some(item => item.id === organization.id) ? [organization] : []), ...(organizations.data?.organizations ?? [])]
  const refresh = async () => { await Promise.all([cache.invalidateQueries({ queryKey: scopeKey }), cache.invalidateQueries({ queryKey: coverageKey })]) }
  const add = useMutation({
    mutationFn: () => api.post(`${base}/scope`, { kind, value: kind === "company" ? String(organization?.id ?? "") : value.trim(), reason: reason.trim() }),
    onSuccess: async () => { setValue(""); setReason(""); setOrganization(null); setError(""); await refresh() },
    onError: () => setError("添加范围失败，请检查内容和权限。"),
  })
  const remove = useMutation({ mutationFn: (id: string) => api.delete(`${base}/scope/${encodeURIComponent(id)}`), onSuccess: async () => { setError(""); await refresh() }, onError: () => setError("删除范围失败，请重试。") })
  const busy = add.isPending || remove.isPending
  const canAdd = kind === "company" ? Boolean(organization) : Boolean(value.trim())
  return <Card className="mt-4">
    <CardHeader><CardTitle className="flex items-center gap-2 text-base"><TargetIcon className="size-4" />任务范围</CardTitle></CardHeader>
    <CardContent className="space-y-3">
      <p className="text-xs text-muted-foreground">范围用于归集资产和估算覆盖情况；已选择的任务目标仍是实际边界。</p>
      {coverage.data?.enabled && <div className="rounded-md border bg-muted/30 p-3 text-sm">
        <div className="flex flex-wrap items-baseline gap-2"><span className="font-medium">资产覆盖度</span><strong className="text-lg">{coverage.data.pct == null ? "—" : `${Math.round(coverage.data.pct * 100)}%`}</strong><span className="text-muted-foreground">有确认事实 {coverage.data.tested} / 范围内 {coverage.data.denominator}</span></div>
        <p className="mt-1 text-xs text-muted-foreground">按确认事实粗略估算，仅供参考。</p>
        {!!coverage.data.by_type.length && <div className="mt-2 flex flex-wrap gap-2 text-xs">{coverage.data.by_type.map(row => <span className="rounded bg-background px-2 py-1" key={row.type}>{assetLabels[row.type] ?? row.type} {row.tested}/{row.total}</span>)}</div>}
      </div>}
      <div className="flex flex-wrap items-center gap-2">
        <select aria-label="范围类型" className="h-8 rounded-md border bg-background px-2 text-sm" value={kind} onChange={event => { setKind(event.target.value as ScopeKind); setValue(""); setOrganization(null) }} disabled={busy}>{Object.entries(labels).map(([key, label]) => <option value={key} key={key}>{label}</option>)}</select>
        {kind === "company" ? <>
          <Input className="min-w-36 flex-1" aria-label="搜索组织" placeholder="搜索组织" value={search} onChange={event => setSearch(event.target.value)} />
          <select aria-label="选择组织" className="h-8 min-w-40 rounded-md border bg-background px-2 text-sm" value={organization ? String(organization.id) : ""} onChange={event => setOrganization(choices.find(item => String(item.id) === event.target.value) ?? null)} disabled={busy}><option value="">选择组织</option>{choices.map(item => <option key={item.id} value={String(item.id)}>{item.name}</option>)}</select>
          <Button size="sm" variant="outline" disabled={page === 0 || organizations.isFetching} onClick={() => setPage(value => value - 1)}>上一页</Button>
          <Button size="sm" variant="outline" disabled={!organizations.data?.nextPageToken || organizations.isFetching} onClick={() => { const next = organizations.data?.nextPageToken; if (next) { setTokens(current => [...current.slice(0, page + 1), next]); setPage(value => value + 1) } }}>下一页</Button>
        </> : <Input className="min-w-44 flex-1" aria-label="范围内容" placeholder={kind === "cidr" ? "192.0.2.0/24" : kind === "ip" ? "192.0.2.1" : kind === "icp" ? "备案号" : kind === "keyword" ? "关键词" : "example.com"} value={value} onChange={event => setValue(event.target.value)} disabled={busy} />}
        <Input className="min-w-36 flex-1" aria-label="范围依据" placeholder="依据（可选）" value={reason} onChange={event => setReason(event.target.value)} disabled={busy} />
        <Button size="sm" variant="outline" disabled={busy || !canAdd} onClick={() => add.mutate()}><PlusIcon className="size-3.5" />添加</Button>
      </div>
      {(error || scopes.isError || coverage.isError || kind === "company" && organizations.isError) && <p role="alert" className="text-xs text-destructive">{error || "加载任务范围或覆盖度失败。"}</p>}
      {scopes.data?.length ? <div className="space-y-1.5">{scopes.data.map(row => <div key={`${row.task_id}-${row.id}`} className="flex flex-wrap items-center gap-2 rounded-md border px-2.5 py-1.5 text-sm">
        <span className="rounded bg-muted px-1.5 py-0.5 text-xs">{labels[row.kind]}</span><span className="min-w-0 flex-1 break-all">{row.company_name || row.domain || row.net || row.value}</span>
        {row.reason && <span className="text-xs text-muted-foreground">{row.reason}</span>}
        <span className="text-xs text-muted-foreground">{row.inherited ? `继承 · ${sourceNames[row.source_task_id ?? ""] ?? "来源任务"}` : row.source === "auto" ? "自动发现" : row.source === "agent" ? "Agent" : "手动"}</span>
        {!row.inherited && <Button aria-label={`删除范围 ${row.company_name || row.domain || row.net || row.value}`} size="sm" variant="ghost" disabled={busy} onClick={() => remove.mutate(row.id)}><Trash2Icon className="size-3.5" /></Button>}
      </div>)}</div> : !scopes.isLoading && !scopes.isError && <p className="text-sm text-muted-foreground">暂无任务范围</p>}
    </CardContent>
  </Card>
}
