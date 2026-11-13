"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { semanticIcons } from "@/components/icons"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"
import { Skeleton } from "@/components/ui/skeleton"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { useOrganizations } from "@/hooks/use-organizations"
import { useAssetMonitorTheme } from "@/hooks/use-asset-monitor-theme"
import { normalizeError } from "@/lib/errors/normalize-error"
import { MAX_MONITOR_ORGANIZATIONS, monitorScreenHref } from "@/lib/asset-monitor-selection"
import { MONITOR_THEMES, MONITOR_THEME_PREVIEWS } from "@/lib/asset-monitor-theme"
import type { Organization } from "@/types/organization.types"

export function OrganizationMonitorLauncher() {
  const t = useTranslations("assetMonitor")
  const [search, setSearch] = React.useState("")
  const [query, setQuery] = React.useState("")
  const [cursors, setCursors] = React.useState<string[]>([""])
  const [page, setPage] = React.useState(0)
  const [selected, setSelected] = React.useState<Map<number, Organization>>(new Map())
  const [theme, changeTheme] = useAssetMonitorTheme()
  React.useEffect(() => {
    const timer = window.setTimeout(() => { setQuery(search.trim()); setPage(0); setCursors([""]) }, 250)
    return () => window.clearTimeout(timer)
  }, [search])
  const organizations = useOrganizations({ pageSize: 20, pageToken: cursors[page] || undefined, filter: query ? `displayName=${JSON.stringify(query)}` : undefined })
  const toggle = (organization: Organization) => setSelected(current => {
    const next = new Map(current)
    if (next.has(organization.id)) next.delete(organization.id)
    else if (next.size < MAX_MONITOR_ORGANIZATIONS) next.set(organization.id, organization)
    return next
  })
  const OrganizationIcon = semanticIcons.concept.organization
  return <div className="mx-auto max-w-5xl">
    <section className="flex min-w-0 flex-col rounded-xl border bg-card p-5">
      <div className="mb-4 flex items-start justify-between gap-3"><div><h2 className="text-base font-semibold">{t("selectOrganizations")}</h2><p className="mt-1 text-sm text-muted-foreground">{t("selectionHint", { max: MAX_MONITOR_ORGANIZATIONS })}</p></div><span className="shrink-0 rounded-full bg-primary/10 px-3 py-1 text-sm font-medium tabular-nums">{selected.size} / {MAX_MONITOR_ORGANIZATIONS}</span></div>
      <Input aria-label={t("searchOrganizations")} placeholder={t("searchOrganizations")} value={search} onChange={event => setSearch(event.target.value)} />
      <div className="my-3 min-h-72" aria-busy={organizations.isFetching}>
        {organizations.error ? <AppErrorState error={normalizeError(organizations.error)} onRetry={organizations.refetch} /> : organizations.isPending ? <div className="grid gap-2">{[0, 1, 2, 3].map(i => <Skeleton key={i} className="h-16 w-full" />)}</div> : <div className="grid gap-2 sm:grid-cols-2">
          {organizations.data?.organizations.map(organization => <label key={organization.id} className={`flex cursor-pointer items-center gap-3 rounded-lg border p-3 transition-colors ${selected.has(organization.id) ? "border-primary/60 bg-primary/5" : "hover:bg-muted/50"}`}>
            <Checkbox aria-label={organization.name} checked={selected.has(organization.id)} disabled={!selected.has(organization.id) && selected.size >= MAX_MONITOR_ORGANIZATIONS} onCheckedChange={() => toggle(organization)} />
            <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted"><OrganizationIcon className="size-4 text-muted-foreground" /></span>
            <span className="min-w-0"><span className="block truncate text-sm font-medium">{organization.name}</span><span className="block truncate text-xs text-muted-foreground">{organization.description || (organization.targetCount !== undefined ? t("organizationTargets", { count: organization.targetCount }) : t("organizationAssetScope"))}</span></span>
          </label>)}
          {!organizations.data?.organizations.length && <p className="col-span-full py-12 text-center text-sm text-muted-foreground">{t("noOrganizations")}</p>}
        </div>}
      </div>
      <div className="mt-auto flex items-center justify-between border-t pt-3"><span className="text-xs text-muted-foreground">{t("organizationTotal", { count: organizations.data?.total ?? 0 })}</span><div className="flex gap-2">
        <Button variant="outline" size="sm" disabled={page === 0 || organizations.isFetching} onClick={() => setPage(value => value - 1)}>{t("previous")}</Button>
        <Button variant="outline" size="sm" disabled={!organizations.data?.nextPageToken || organizations.isFetching} onClick={() => { const cursor = organizations.data?.nextPageToken; if (cursor) { setCursors(current => [...current.slice(0, page + 1), cursor]); setPage(value => value + 1) } }}>{t("next")}</Button>
      </div></div>
      <div className="mt-5 border-t pt-4"><div className="mb-2 flex items-center justify-between"><p className="text-sm font-medium">{t("selected", { count: selected.size })}</p>{selected.size > 0 && <Button size="sm" variant="ghost" onClick={() => setSelected(new Map())}>{t("clear")}</Button>}</div>
        <div className="flex max-h-36 min-h-10 flex-wrap gap-2 overflow-auto">{[...selected.values()].map(organization => <Button key={organization.id} size="sm" variant="outline" onClick={() => toggle(organization)} aria-label={t("removeOrganization", { name: organization.name })}><span className="max-w-44 truncate">{organization.name}</span><span aria-hidden="true">×</span></Button>)}{selected.size === 0 && <span className="text-sm text-muted-foreground">{t("selectToStart")}</span>}</div>
      </div>
      <div className="mt-4 border-t pt-4"><p className="mb-3 text-sm font-medium">{t("selectTheme")}</p><div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5" role="group" aria-label={t("selectTheme")}>
        {MONITOR_THEMES.map(option => <button key={option} type="button" aria-pressed={theme === option} onClick={() => changeTheme(option)} className={`flex min-w-0 items-center gap-2 rounded-lg border p-2 text-left text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${theme === option ? "border-primary bg-primary/5" : "hover:bg-muted/50"}`}>
          <span aria-hidden="true" className="flex h-8 w-12 shrink-0 flex-col justify-center gap-1 rounded border border-white/15 px-1.5" style={{ background: MONITOR_THEME_PREVIEWS[option].background }}><span className="h-1 w-6 rounded" style={{ background: MONITOR_THEME_PREVIEWS[option].accent }} /><span className="h-1 w-4 rounded opacity-50" style={{ background: MONITOR_THEME_PREVIEWS[option].accent }} /></span>
          <span className="truncate">{t(`theme.${option}`)}</span>
        </button>)}
      </div></div>
      <div className="mt-4 flex justify-end"><Button disabled={selected.size === 0} render={selected.size > 0 ? <a href={monitorScreenHref([...selected.keys()], theme)} target="_blank" rel="noopener noreferrer" /> : undefined}>{t("openScreen")}</Button></div>
    </section>
  </div>
}
