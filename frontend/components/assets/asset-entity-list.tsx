"use client"

import * as React from "react"
import Link from "next/link"
import { useLocale, useTranslations } from "next-intl"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { ContentHandoff } from "@/components/shared/loading/content-handoff"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useAssetEntities, useAssetInventory, useAssetPorts } from "@/hooks/use-asset-entities"
import { normalizeError } from "@/lib/errors/normalize-error"
import { assetDisplayType } from "@/lib/asset-display-type"
import type { AssetEntity, AssetEntityType, AssetInventoryItem, AssetOrganization } from "@/types/asset-entity.types"
import { AssetIntelligenceDialog } from "@/components/ai/artex/asset-intelligence-dialog"
import { AITaskCreateDialog } from "@/components/ai/ai-task-create-dialog"
import { AssetRegistrationDialog } from "./asset-registration-dialog"
import { AssetOrganizationFilter } from "./asset-organization-filter"
import { AssetPagination } from "./asset-pagination"

const assetTypes = ["all", "root_domain", "subdomain", "ip", "app", "web", "port", "endpoint"] as const
type AssetFilter = typeof assetTypes[number]
const PAGE_SIZE = 50

function OrganizationNames({ organizations }: { organizations?: AssetOrganization[] }) {
  const t = useTranslations("assetInventory")
  return <span className="text-muted-foreground">{organizations?.length ? organizations.map(item => item.name).join("、") : t("unassignedOrganization")}</span>
}

export function AssetEntityList({ rootDomainId, organizationId, initialOrganizationId, initialSearch = "", onOrganizationChange }: { rootDomainId?: string; organizationId?: number; initialOrganizationId?: number; initialSearch?: string; onOrganizationChange?: (id?: number) => void } = {}) {
  const t = useTranslations("assetInventory")
  const locale = useLocale()
  const [filter, setFilter] = React.useState<AssetFilter>("all")
  const [search, setSearch] = React.useState(initialSearch)
  const [query, setQuery] = React.useState(initialSearch)
  const [page, setPage] = React.useState(1)
  const [localOrganization, setLocalOrganization] = React.useState<number | undefined>(initialOrganizationId)
  const [taskAsset, setTaskAsset] = React.useState<AssetEntity | null>(null)
  const [intelAsset, setIntelAsset] = React.useState<AssetEntity | null>(null)
  const selectedOrganization = onOrganizationChange ? organizationId : localOrganization

  React.useEffect(() => {
    const timeout = window.setTimeout(() => { setQuery(search.trim()); setPage(1) }, 300)
    return () => window.clearTimeout(timeout)
  }, [search])

  const shared = { q: query || undefined, pageSize: PAGE_SIZE, page, organizationId: selectedOrganization, rootDomainId }
  const result = useAssetEntities({ ...shared,
    assetType: filter === "all" || filter === "web" || filter === "port" ? undefined : filter as AssetEntityType,
    assetCategory: filter === "web" ? "web" : undefined,
  }, filter !== "port" && filter !== "all")
  const ports = useAssetPorts(shared, filter === "port")
  const inventory = useAssetInventory(shared, filter === "all")
  const active = filter === "port" ? ports : filter === "all" ? inventory : result
  const total = active.data?.totalSize ?? 0
  React.useEffect(() => { if (active.data && !active.isFetching && page > Math.max(1, Math.ceil(total / PAGE_SIZE))) setPage(Math.max(1, Math.ceil(total / PAGE_SIZE))) }, [active.data, active.isFetching, page, total])
  const items: AssetInventoryItem[] = filter === "all" ? inventory.data?.items ?? [] : filter === "port" ? (ports.data?.ports ?? []).map(port => ({ kind: "port", port })) : (result.data?.assetEntities ?? []).map(asset => ({ kind: "asset", asset }))
  const selectFilter = (value: string) => { if (assetTypes.includes(value as AssetFilter)) { setFilter(value as AssetFilter); setPage(1) } }
  const selectOrganization = (id?: number) => { if (onOrganizationChange) onOrganizationChange(id); else setLocalOrganization(id); setPage(1) }

  if (active.error) return <AppErrorState error={normalizeError(active.error, { notFoundKind: "unexpected-error" })} onRetry={active.refetch} />

  return <div className="flex min-w-0 flex-col gap-3">
    <Tabs value={filter} onValueChange={selectFilter}>
      <div className="max-w-full overflow-x-auto"><TabsList variant="filter" className="w-max max-w-none">
        {assetTypes.filter(type => !rootDomainId || type !== "root_domain").map(type => <TabsTrigger key={type} value={type} title={type === "web" ? t("webFilterHint") : type === "endpoint" ? t("endpointFilterHint") : undefined}>{t(type)}</TabsTrigger>)}
      </TabsList></div>
    </Tabs>
    <div className="flex flex-wrap items-center justify-between gap-2">
      <div className="flex min-w-0 flex-wrap items-center gap-2"><Input value={search} onChange={event => setSearch(event.target.value)} placeholder={t("search")} aria-label={t("search")} className="w-64 max-w-full" /><AssetOrganizationFilter value={selectedOrganization} onChange={selectOrganization} /></div>
      {!rootDomainId && <AssetRegistrationDialog initialType={filter === "all" ? undefined : filter === "port" ? "service" : filter} organizationId={selectedOrganization} />}
    </div>
    <ContentHandoff owner="asset-entity-list-content" layer="workspace" isLoading={active.isPending} skeleton={<div className="flex flex-col gap-2" aria-label={t("loading")}>{[0,1,2,3].map(row => <Skeleton key={row} className="h-10 w-full" />)}</div>}>
      <Table><TableHeader><TableRow><TableHead>{t("asset")}</TableHead><TableHead>{t("type")}</TableHead><TableHead>{t("organization")}</TableHead><TableHead>{t("firstSeen")}</TableHead><TableHead>{t("lastSeen")}</TableHead>{filter !== "port" && <TableHead />}</TableRow></TableHeader>
        <TableBody>
          {items.map(item => item.kind === "port" ? <TableRow key={`port:${item.port.label}`}>
            <TableCell className="max-w-md break-all">{item.port.label}</TableCell><TableCell><span title={t(item.port.serviceSource === "port" ? "servicePortHint" : item.port.serviceSource === "observed" ? "serviceObservedHint" : "serviceUnknownHint")}>{item.port.serviceSource === "unknown" ? t("unknown") : item.port.service}</span>{item.port.serviceSource === "port" && <span className="ml-1 text-xs text-muted-foreground">{t("portInferred")}</span>}</TableCell><TableCell><OrganizationNames organizations={item.port.organizations} /></TableCell><TableCell>{new Date(item.port.firstSeenAt).toLocaleString(locale)}</TableCell><TableCell>{new Date(item.port.lastSeenAt).toLocaleString(locale)}</TableCell>{filter !== "port" && <TableCell />}
          </TableRow> : <TableRow key={`asset:${item.asset.id}`}>
            <TableCell className="max-w-md break-all">{item.asset.assetType === "root_domain" || item.asset.assetType === "app" ? <Link className="font-medium text-primary underline decoration-primary/40 underline-offset-4 hover:decoration-primary" href={`/assets/${item.asset.assetType === "app" ? "applications" : "root-domains"}/${encodeURIComponent(item.asset.id)}/${selectedOrganization ? `?organizationId=${selectedOrganization}` : ""}`}>{item.asset.label}</Link> : item.asset.label}</TableCell>
            <TableCell>{t(assetDisplayType(item.asset))}{item.asset.assetType === "app" && (item.asset.applicationSources ?? 0) > 0 && <span className="ml-1.5 text-xs text-muted-foreground" title={t("applicationRecognitionHint")}>· {t("applicationRecognized")}</span>}{item.asset.assetType === "endpoint" && assetDisplayType(item.asset) === "web" && <span className="ml-1.5 text-xs text-muted-foreground">· URL</span>}</TableCell><TableCell><OrganizationNames organizations={item.asset.organizations} /></TableCell><TableCell>{new Date(item.asset.firstSeenAt).toLocaleString(locale)}</TableCell><TableCell>{new Date(item.asset.lastSeenAt).toLocaleString(locale)}</TableCell><TableCell><div className="flex flex-wrap gap-2"><Button variant="outline" size="sm" onClick={() => setTaskAsset(item.asset)}>{t("createTask")}</Button><Button variant="outline" size="sm" onClick={() => setIntelAsset(item.asset)}>{t("intelligence")}</Button></div></TableCell>
          </TableRow>)}
          {!items.length && <TableRow><TableCell colSpan={filter === "port" ? 5 : 6}>{t("empty")}</TableCell></TableRow>}
        </TableBody>
      </Table>
    </ContentHandoff>
    <AssetIntelligenceDialog asset={intelAsset} open={!!intelAsset} onOpenChange={open => { if (!open) setIntelAsset(null) }} />
    <AITaskCreateDialog asset={taskAsset ?? undefined} open={Boolean(taskAsset)} onOpenChange={open => { if (!open) setTaskAsset(null) }} trigger={false} />
    <AssetPagination page={page} total={total} pageSize={PAGE_SIZE} onPageChange={setPage} />
  </div>
}
