"use client"

import * as React from "react"
import Link from "next/link"
import { useLocale, useTranslations } from "next-intl"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { ContentHandoff } from "@/components/shared/loading/content-handoff"
import { getLoadingStructureSlotAttributes } from "@/components/shared/loading/loading-owner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useFofa } from "@/hooks/use-fofa"
import { normalizeError } from "@/lib/errors/normalize-error"
import { getErrorMessage, getErrorResponseData } from "@/lib/response-parser"
import type { FofaImportResult, FofaSearchInput, FofaSearchPage } from "@/types/fofa.types"
import { FofaImportDialog } from "./fofa-import-dialog"
import { FofaResultsTable } from "./fofa-results-table"
import { FofaSettingsDialog } from "./fofa-settings-dialog"
import { FofaQueryForm } from "./fofa-query-form"

export function FofaSearchWorkspace() {
  const t = useTranslations("spaceSearch")
  const locale = useLocale()
  const { userId, settings, search } = useFofa()
  const [query, setQuery] = React.useState("")
  const [pageSize, setPageSize] = React.useState("50")
  const [full, setFull] = React.useState(false)
  const [current, setCurrent] = React.useState<FofaSearchPage | null>(null)
  const [submitted, setSubmitted] = React.useState<FofaSearchInput | null>(null)
  const pages = React.useRef(new Map<number, FofaSearchPage>())
  const generation = React.useRef(0)
  const [selected, setSelected] = React.useState(new Set<number>())
  const [error, setError] = React.useState("")
  const [importOpen, setImportOpen] = React.useState(false)
  const [imported, setImported] = React.useState<FofaImportResult | null>(null)
  React.useEffect(() => {
    generation.current += 1
    pages.current.clear(); setQuery(""); setCurrent(null); setSubmitted(null); setSelected(new Set()); setImported(null); setImportOpen(false); setError("")
    return () => { generation.current += 1 }
  }, [userId])
  const reset = () => { generation.current += 1; pages.current.clear(); setCurrent(null); setSubmitted(null); setSelected(new Set()); setImported(null); setImportOpen(false); setError(""); search.reset() }
  const execute = async (input: FofaSearchInput) => {
    const requestGeneration = generation.current
    setSelected(new Set()); setImported(null); setError("")
    try {
      const result = await search.mutateAsync(input)
      if (requestGeneration !== generation.current) return
      pages.current.set(input.page, result); setSelected(new Set()); setCurrent(result)
    } catch (err) { if (requestGeneration === generation.current) setError(getErrorMessage(getErrorResponseData(err)) || t("error")) }
  }
  const begin = async () => {
    const input = { query: query.trim(), page: 1, pageSize: Number(pageSize), full }
    if (!input.query) return
    generation.current += 1; pages.current.clear(); setCurrent(null); setSubmitted(input)
    await execute(input)
  }
  const navigate = async (page: number) => {
    if (!submitted || search.isPending) return
    setSelected(new Set()); setImported(null); setError("")
    const cached = pages.current.get(page)
    if (cached) { setCurrent(cached); return }
    await execute({ ...submitted, page })
  }
  if (settings.error) return <AppErrorState error={normalizeError(settings.error, { notFoundKind: "unexpected-error" })} onRetry={settings.refetch} />
  const status = settings.data
  const warnings = [...new Set(imported?.results.flatMap(item => item.warnings) ?? [])]
  return <ContentHandoff owner="fofa-search-workspace" layer="workspace" isLoading={settings.isPending}
    skeleton={<div className="flex min-h-120 flex-col gap-4" aria-label={t("loading")} aria-busy="true">
      <div className="flex min-h-9 items-center justify-between gap-2"><Badge variant="outline">{t("loading")}</Badge><Button variant="outline" disabled>{t("settings")}</Button></div>
      <p className="min-h-10 text-sm text-muted-foreground">{t("loading")}</p>
      <div {...getLoadingStructureSlotAttributes("fofa-query-panel")}><FofaQueryForm query={query} pageSize={pageSize} full={full} disabled onQuery={() => undefined} onPageSize={() => undefined} onFull={() => undefined} onSubmit={() => undefined} /></div>
      <p className="py-8 text-center text-sm text-muted-foreground">{t("initial")}</p>
    </div>}>
    {status && <div className="flex min-h-120 min-w-0 flex-col gap-4">
      <div className="flex min-h-9 flex-wrap items-center justify-between gap-2"><Badge variant={status.configured ? "success" : "outline"}>{status.configured ? t("configured") : t("unconfigured")}</Badge>{status.canManage && <FofaSettingsDialog settings={status} onSaved={reset} />}</div>
      <p className="min-h-10 text-sm text-muted-foreground">{!status.canSearch ? t("permissionHint") : !status.configured ? status.canManage ? t("setupHint") : t("askAdmin") : t("readyHint")}</p>
      <div {...getLoadingStructureSlotAttributes("fofa-query-panel")}><FofaQueryForm query={query} pageSize={pageSize} full={full} disabled={search.isPending} pending={search.isPending} ready={status.configured && status.canSearch} onQuery={setQuery} onPageSize={setPageSize} onFull={setFull} onSubmit={() => void begin()} /></div>
      {error && <p role="alert" className="rounded-md border border-destructive p-3 text-sm text-destructive">{error}</p>}
      {search.isPending && !current && <Skeleton className="h-64 w-full" />}
      {!current && !search.isPending && <p className="py-8 text-center text-sm text-muted-foreground">{t("initial")}</p>}
      {current && <>
        <div className="flex flex-wrap items-center justify-between gap-2"><div className="min-w-0 text-sm"><p className="break-all font-medium">{current.query}</p><p className="text-muted-foreground">{t("resultSummary", { total: current.total, page: current.page, count: current.results.length })}</p></div>
          <div className="flex flex-wrap gap-2"><Button variant="outline" size="sm" onClick={() => setSelected(new Set(current.results.filter(row => row.importable && !selected.has(row.rowId)).map(row => row.rowId)))} disabled={search.isPending}>{t("invertSelection")}</Button><Button variant="outline" size="sm" onClick={() => setSelected(new Set())} disabled={!selected.size || search.isPending}>{t("clearSelection")}</Button><Button size="sm" disabled={!selected.size || search.isPending} onClick={() => setImportOpen(true)}>{t("syncSelected", { count: selected.size })}</Button></div>
        </div>
        <FofaResultsTable rows={current.results} selected={selected} onSelect={setSelected} disabled={search.isPending} />
        <div className="flex flex-wrap items-center justify-between gap-2"><p className="text-xs text-muted-foreground">{t("expiresAt", { time: new Date(current.expiresAt).toLocaleString(locale) })}</p><div className="flex items-center gap-2"><Button variant="outline" size="sm" disabled={current.page <= 1 || search.isPending} onClick={() => void navigate(current.page - 1)}>{t("previous")}</Button><Button variant="outline" size="sm" disabled={!current.hasNext || search.isPending} onClick={() => void navigate(current.page + 1)}>{t("next")}</Button></div></div>
        <FofaImportDialog searchId={current.id} rows={[...selected].sort((a, b) => a - b)} open={importOpen} onOpenChange={setImportOpen} onImported={result => { setImported(result); setSelected(new Set()) }} />
      </>}
      {imported && <div className="flex flex-col gap-2 rounded-md border bg-card p-3 text-sm" role="status">
        <p>{t("importSummary", { rows: imported.importedRows, assets: imported.assetCount, failed: imported.failedRows })}</p>
        {warnings.map(warning => <p key={warning} className="text-muted-foreground">{warning}</p>)}
        {imported.errors.map(item => <p key={item.rowId} className="text-destructive">{t("rowError", { number: item.rowId + 1, message: item.message })}</p>)}
        {imported.importedRows > 0 && <Button variant="link" size="sm" className="self-start" render={<Link href="/assets/" />}>{t("viewAssets")}</Button>}
      </div>}
    </div>}
  </ContentHandoff>
}
