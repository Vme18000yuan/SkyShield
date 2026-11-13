"use client"

import * as React from "react"
import Link from "next/link"
import { useTranslations } from "next-intl"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useTargets } from "@/hooks/use-targets"
import { vulnerabilityKeys } from "@/hooks/use-vulnerabilities/keys"
import { infoGather, type InfoGatherRecord } from "@/services/info-gather.service"
import type { Target } from "@/types/target.types"

const keyFor = (record: InfoGatherRecord) => `${record.kind}:${record.id}`
const riskCategories = new Set(["ANONYMOUS_DATA", "REVIEW_TOKEN_DIFFERENCE", "PUBLIC_LISTING", "DNS_ZONE_TRANSFER"])

export function InfoGatherPage() {
  const t = useTranslations("infoGather")
  const queryClient = useQueryClient()
  const [runId, setRunId] = React.useState("")
  const [offset, setOffset] = React.useState(0)
  const [selected, setSelected] = React.useState<Set<string>>(() => new Set())
  const [target, setTarget] = React.useState<Target | null>(null)
  const [targetDraft, setTargetDraft] = React.useState("")
  const [targetSearch, setTargetSearch] = React.useState("")
  const [targetTokens, setTargetTokens] = React.useState([""])
  const [targetPage, setTargetPage] = React.useState(0)
  const [allowCdn, setAllowCdn] = React.useState(false)
  const runs = useQuery({ queryKey: ["info-gather-runs"], queryFn: infoGather.runs, refetchInterval: 8000 })
  const records = useQuery({ queryKey: ["info-gather-records", runId, offset], queryFn: () => infoGather.records(runId, offset), enabled: Boolean(runId), refetchInterval: 8000 })
  const targets = useTargets({ pageSize: 20, pageToken: targetTokens[targetPage] || undefined, filter: targetSearch ? `displayName=${JSON.stringify(targetSearch)}` : undefined }, { enabled: Boolean(runId) })
  const run = runs.data?.find(item => item.id === runId)
  const targetOptions = [...(target && !targets.data?.targets.some(item => item.id === target.id) ? [target] : []), ...(targets.data?.targets ?? [])]

  React.useEffect(() => {
    const timer = window.setTimeout(() => { setTargetSearch(targetDraft.trim()); setTargetTokens([""]); setTargetPage(0) }, 300)
    return () => window.clearTimeout(timer)
  }, [targetDraft])
  React.useEffect(() => { if (!runId && runs.data?.length) setRunId(runs.data[0].id) }, [runId, runs.data])
  const sync = useMutation({
    mutationFn: () => infoGather.sync(runId, target!.id, (records.data?.records ?? []).filter(item => selected.has(keyFor(item))).map(({ kind, id }) => ({ kind, id })), allowCdn),
    onSuccess: result => {
      if (result.failed) toast.warning(t("syncPartial", { count: result.imported, failed: result.failed }))
      else toast.success(t("syncSuccess", { count: result.imported }))
      setSelected(new Set())
      void queryClient.invalidateQueries({ queryKey: ["info-gather-runs"] })
      void queryClient.invalidateQueries({ queryKey: ["info-gather-records", runId] })
      void queryClient.invalidateQueries({ queryKey: vulnerabilityKeys.all })
      for (const key of ["asset-entities", "asset-inventory", "asset-ports", "endpoints", "subdomains"]) void queryClient.invalidateQueries({ queryKey: [key] })
    },
    onError: () => toast.error(t("failed")),
  })
  const ignore = useMutation({
    mutationFn: () => infoGather.ignore(runId, (records.data?.records ?? []).filter(item => selected.has(keyFor(item))).map(({ kind, id }) => ({ kind, id }))),
    onSuccess: result => {
      toast.success(t("handledSuccess", { count: result.processed }))
      setSelected(new Set())
      void queryClient.invalidateQueries({ queryKey: ["info-gather-runs"] })
      void queryClient.invalidateQueries({ queryKey: ["info-gather-records", runId] })
    },
    onError: () => toast.error(t("failed")),
  })
  const changeRun = (id: string) => { setRunId(id); setOffset(0); setSelected(new Set()); setTarget(null); setAllowCdn(false) }
  const scopeText = (scope: string) => ({ in_scope: t("scopeIn"), associated: t("scopeAssociated"), out_of_scope: t("scopeOutside"), unverified: t("scopeUnverified") }[scope] ?? t("scopeUnverified"))
  const stageLabels = ["passive", "organization", "dns_brute", "dns", "axfr", "cdn", "http", "ports", "web", "cloud"] as const
  const statuses = ["running", "completed", "partial", "failed", "interrupted", "success", "timeout", "skipped"] as const
  const statusText = (value: string) => statuses.includes(value as typeof statuses[number]) ? t(`statuses.${value as typeof statuses[number]}`) : value

  return <div className="space-y-4">
    <div className="flex flex-wrap gap-2">
      <Button render={<Link href="/ai/chat/?agent=info_gatherer" />}>{t("newRun")}</Button>
      <Button variant="outline" render={<Link href="/targets/" />}>{t("manageTargets")}</Button>
    </div>
    <Card>
      <CardHeader><CardTitle>{t("runs")}</CardTitle></CardHeader>
      <CardContent>
        {runs.isError && <p role="alert" className="text-sm text-destructive">{t("failed")}</p>}
        {!runs.isLoading && !runs.data?.length && <p className="text-sm text-muted-foreground">{t("emptyRuns")}</p>}
        {!!runs.data?.length && <Table><TableHeader><TableRow><TableHead>{t("target")}</TableHead><TableHead>{t("status")}</TableHead><TableHead>{t("created")}</TableHead><TableHead>{t("imported")}</TableHead><TableHead>{t("pending")}</TableHead><TableHead>{t("observations")}</TableHead><TableHead /></TableRow></TableHeader>
          <TableBody>{runs.data.map(item => <TableRow key={item.id} data-state={item.id === runId ? "selected" : undefined}>
            <TableCell className="font-medium">{item.target}</TableCell><TableCell>{statusText(item.status)}</TableCell><TableCell>{new Date(item.createdAt).toLocaleString()}</TableCell><TableCell>{item.imported}</TableCell><TableCell>{item.pending ? <Button variant="link" size="sm" aria-label={t("openPending", { target: item.target })} onClick={() => changeRun(item.id)}>{item.pending}</Button> : 0}</TableCell><TableCell>{item.observations ? <Button variant="link" size="sm" aria-label={t("openObservations", { target: item.target })} onClick={() => changeRun(item.id)}>{item.observations}</Button> : 0}</TableCell><TableCell><Button variant="outline" size="sm" onClick={() => changeRun(item.id)}>{t("records")}</Button></TableCell>
          </TableRow>)}</TableBody></Table>}
      </CardContent>
    </Card>
    {run && <Card>
      <CardHeader><CardTitle>{t("records")} · {run.target}</CardTitle><CardDescription>{t("observationHint")}</CardDescription></CardHeader>
      <CardContent className="space-y-4">
        {run.error && <p role="alert" className="text-sm text-destructive">{run.error}</p>}
        <p className="text-sm text-muted-foreground">{t("managedCoverage")}</p>
        {run.coverage?.steps && <div className="text-sm text-muted-foreground"><span className="font-medium text-foreground">{t("coverage")}：</span>{stageLabels.filter(stage => run.coverage.steps?.[stage]).map(stage => `${t(`stages.${stage}`)} ${statusText(run.coverage.steps![stage])}`).join(" · ")}</div>}
        {records.isError && <p role="alert" className="text-sm text-destructive">{t("failed")}</p>}
        {!records.isLoading && !records.data?.records.length && <p className="text-sm text-muted-foreground">{t("emptyRecords")}</p>}
        {!!records.data?.records.length && <Table><TableHeader><TableRow><TableHead><Checkbox aria-label={t("selectPage")} disabled={sync.isPending || ignore.isPending} checked={records.data.records.every(item => selected.has(keyFor(item)))} onCheckedChange={checked => setSelected(checked ? new Set(records.data!.records.map(keyFor)) : new Set())} /></TableHead><TableHead>{t("kind")}</TableHead><TableHead>{t("record")}</TableHead><TableHead>{t("scope")}</TableHead><TableHead>{t("reason")}</TableHead></TableRow></TableHeader>
          <TableBody>{records.data.records.map(item => <TableRow key={keyFor(item)}>
            <TableCell><Checkbox aria-label={`${t("record")} ${item.payload.value || item.payload.url || item.id}`} disabled={sync.isPending || ignore.isPending} checked={selected.has(keyFor(item))} onCheckedChange={checked => setSelected(current => { const next = new Set(current); if (checked) next.add(keyFor(item)); else next.delete(keyFor(item)); return next })} /></TableCell>
            <TableCell>{item.kind === "asset" ? t("asset") : item.kind === "vulnerability" || riskCategories.has(item.payload.category ?? "") ? t("candidate") : t("observation")}</TableCell>
            <TableCell className="max-w-sm break-all">{item.payload.title && <div>{item.payload.title}</div>}{item.payload.value || item.payload.url}{item.payload.port ? `:${item.payload.port}/${item.payload.protocol}` : ""}{item.payload.evidence?.length ? <div className="text-xs text-muted-foreground">{t("evidence")}：{item.payload.evidence.join(", ")}</div> : null}</TableCell>
            <TableCell>{scopeText(item.payload.scope)}</TableCell><TableCell className="max-w-xs whitespace-normal">{item.reason || "—"}</TableCell>
          </TableRow>)}</TableBody></Table>}
        {records.data && records.data.total > 0 && <div className="flex flex-wrap items-center justify-end gap-2 text-sm"><span>{t("page", { page: Math.floor(offset / 100) + 1, total: records.data.total })}</span><Button variant="outline" size="sm" disabled={offset === 0} onClick={() => { setOffset(value => Math.max(0, value - 100)); setSelected(new Set()) }}>{t("previous")}</Button><Button variant="outline" size="sm" disabled={offset + 100 >= records.data.total} onClick={() => { setOffset(value => value + 100); setSelected(new Set()) }}>{t("next")}</Button></div>}
        <div className="grid gap-3 border-t pt-4 md:max-w-xl">
          <Label htmlFor="info-target">{t("selectTarget")}</Label>
          <Input aria-label={t("searchTarget")} placeholder={t("searchTarget")} value={targetDraft} onChange={event => setTargetDraft(event.target.value)} />
          <Select value={target ? String(target.id) : "none"} onValueChange={value => setTarget(targetOptions.find(item => String(item.id) === value) ?? null)}><SelectTrigger id="info-target"><SelectValue>{target?.name ?? t("chooseTarget")}</SelectValue></SelectTrigger><SelectContent><SelectItem value="none">{t("chooseTarget")}</SelectItem>{targetOptions.map(item => <SelectItem key={item.id} value={String(item.id)}>{item.name}{item.organizations?.length ? ` (${item.organizations.map(org => org.name).join(", ")})` : ""}</SelectItem>)}</SelectContent></Select>
          <div className="flex justify-end gap-2"><Button size="sm" variant="outline" disabled={!targetPage || targets.isFetching} onClick={() => setTargetPage(value => value - 1)}>{t("previous")}</Button><Button size="sm" variant="outline" disabled={!targets.data?.nextPageToken || targets.isFetching} onClick={() => { const next = targets.data?.nextPageToken; if (next) { setTargetTokens(current => [...current.slice(0, targetPage + 1), next]); setTargetPage(value => value + 1) } }}>{t("next")}</Button></div>
          <p className="text-sm text-muted-foreground">{t("targetHint")}</p>
          <label className="flex items-center gap-2 text-sm"><Checkbox checked={allowCdn} onCheckedChange={setAllowCdn} />{t("allowCdn")}</label>
          <div className="flex flex-wrap gap-2"><Button disabled={!target || !selected.size || sync.isPending || ignore.isPending || run.status === "running"} onClick={() => sync.mutate()}>{sync.isPending ? t("syncing") : t("sync")}</Button><Button variant="outline" disabled={!selected.size || sync.isPending || ignore.isPending || run.status === "running"} onClick={() => ignore.mutate()}>{ignore.isPending ? t("handling") : t("markHandled")}</Button></div>
        </div>
      </CardContent>
    </Card>}
  </div>
}
