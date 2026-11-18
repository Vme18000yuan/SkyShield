"use client"

import * as React from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useTranslations } from "next-intl"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { AssetCoverageGraph } from "@/components/shared/asset-coverage/asset-coverage-graph"
import { useRootDomainOverview } from "@/hooks/use-asset-entities"
import { normalizeError } from "@/lib/errors/normalize-error"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"
import { assetInventoryHref } from "@/lib/asset-inventory-navigation"
import { CdnVisibilityToggle } from "./cdn-visibility-toggle"
import { AssetEntityList } from "./asset-entity-list"
import { AssetOrganizationFilter } from "./asset-organization-filter"

export function RootDomainOverview({ id }: { id: string }) {
  const t = useTranslations("assetInventory")
  const params = useSearchParams()
  const router = useRouter()
  const rawOrg = params.get("organizationId")
  const organizationId = rawOrg && /^[1-9]\d*$/.test(rawOrg) && Number.isSafeInteger(Number(rawOrg)) && Number(rawOrg) <= 2147483647 ? Number(rawOrg) : undefined
  const showCdn = params.get("showCdn") === "1"
  const fromOverview = params.get("from") === "overview" && !!organizationId
  const overview = useRootDomainOverview(id, organizationId, !showCdn)
  const [tab, setTab] = React.useState("assets")
  if (!/^[1-9]\d*$/.test(id)) return <div className={COMPACT_CONTENT_GUTTER_CLASS}>{t("rootNotFound")}</div>
  if (overview.error) return <div className={COMPACT_CONTENT_GUTTER_CLASS}><AppErrorState error={normalizeError(overview.error)} onRetry={overview.refetch} actionHref="/assets/" /></div>
  if (overview.isPending || !overview.data) return <div className={COMPACT_CONTENT_GUTTER_CLASS}><Skeleton className="h-32 w-full" /></div>
  const root = overview.data
  const metrics = [
    { label: t("subdomain"), value: root.counts.subdomains }, { label: t("ip"), value: root.counts.ips },
    { label: t("port"), value: root.counts.ports }, { label: t("app"), value: root.counts.apps }, { label: t("web"), value: root.counts.web }, { label: t("endpoint"), value: root.counts.urls },
  ]
  const setScope = (org: number | undefined, show: boolean) => router.replace(assetInventoryHref({ path: `/assets/root-domains/${encodeURIComponent(id)}/`, organizationId: org, showCdn: show, from: fromOverview && org ? "overview" : undefined }), { scroll: false })
  const changeOrganization = (org?: number) => setScope(org, showCdn)

  return <div className={COMPACT_PAGE_SHELL_CLASS}><div className={`${COMPACT_CONTENT_GUTTER_CLASS} space-y-5`}>
    <div className="flex flex-wrap items-center justify-between gap-3"><div><p className="text-sm text-muted-foreground">{t("rootOverview")}</p><h1 className="mt-1 break-all text-2xl font-semibold">{root.domain}</h1><p className="mt-1 text-sm text-muted-foreground">{root.organizations?.length ? root.organizations.map(item => item.name).join("、") : t("unassignedOrganization")}</p></div><Button variant="outline" render={<Link href={assetInventoryHref({ view: fromOverview ? undefined : "inventory", organizationId, showCdn })} />}>{t(fromOverview ? "organizationOverview" : "backToAssets")}</Button></div>
    <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">{metrics.map(metric => <Card key={metric.label}><CardContent className="space-y-2 p-4"><p className="text-sm text-muted-foreground">{metric.label}</p><p className="text-2xl font-semibold tabular-nums">{metric.value.toLocaleString()}</p></CardContent></Card>)}</div>
    <CdnVisibilityToggle shown={showCdn} onChange={value => setScope(organizationId, value)} />
    <Tabs value={tab} onValueChange={setTab}>
      <TabsList variant="content"><TabsTrigger value="assets">{t("relatedAssets")}</TabsTrigger><TabsTrigger value="coverage">{t("coverageGraph")}</TabsTrigger></TabsList>
      <TabsContent value="assets"><AssetEntityList key={`${id}:${organizationId}:${showCdn}`} showCdn={showCdn} rootDomainId={id} organizationId={organizationId} onOrganizationChange={changeOrganization} /></TabsContent>
      <TabsContent value="coverage">{tab === "coverage" && <section className="space-y-3"><AssetOrganizationFilter value={organizationId} onChange={changeOrganization} /><AssetCoverageGraph showCdn={showCdn} scope={{ kind: "root", id, organizationId }} /></section>}</TabsContent>
    </Tabs>
  </div></div>
}
