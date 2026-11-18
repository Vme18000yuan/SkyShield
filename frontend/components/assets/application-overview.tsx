"use client"

import * as React from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useLocale, useTranslations } from "next-intl"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useApplicationOverview } from "@/hooks/use-asset-entities"
import { normalizeError } from "@/lib/errors/normalize-error"
import { assetInventoryHref } from "@/lib/asset-inventory-navigation"
import { AssetOrganizationFilter } from "./asset-organization-filter"
import { AssetPagination } from "./asset-pagination"

export function ApplicationOverview({ id }: { id: string }) {
  const t = useTranslations("assetInventory"), locale = useLocale()
  const router = useRouter(), params = useSearchParams()
  const rawOrg = params.get("organizationId")
  const organizationId = rawOrg && /^[1-9]\d*$/.test(rawOrg) && Number.isSafeInteger(Number(rawOrg)) && Number(rawOrg) <= 2147483647 ? Number(rawOrg) : undefined
  const [page, setPage] = React.useState(1)
  const overview = useApplicationOverview(id, organizationId, page)
  if (!/^[1-9]\d*$/.test(id)) return <div className={COMPACT_CONTENT_GUTTER_CLASS}>{t("applicationNotFound")}</div>
  if (overview.error) return <div className={COMPACT_CONTENT_GUTTER_CLASS}><AppErrorState error={normalizeError(overview.error)} onRetry={overview.refetch} actionHref="/assets/" /></div>
  if (overview.isPending || !overview.data) return <div className={COMPACT_CONTENT_GUTTER_CLASS}><Skeleton className="h-32 w-full" /></div>
  const { application, websites, totalSize } = overview.data
  const changeOrganization = (org?: number) => { setPage(1); router.replace(`/assets/applications/${encodeURIComponent(id)}/${org ? `?organizationId=${org}` : ""}`) }
  return <div className={COMPACT_PAGE_SHELL_CLASS}><div className={`${COMPACT_CONTENT_GUTTER_CLASS} space-y-5`}>
    <div className="flex flex-wrap items-center justify-between gap-3"><div><p className="text-sm text-muted-foreground">{t("applicationOverview")}</p><h1 className="mt-1 break-all text-2xl font-semibold">{application.label}</h1><p className="mt-1 text-sm text-muted-foreground">{application.organizations?.length ? application.organizations.map(item => item.name).join("、") : t("unassignedOrganization")}</p></div><Button variant="outline" render={<Link href={assetInventoryHref({ organizationId, view: "inventory", type: "app", showCdn: params.get("showCdn") === "1" })} />}>{t("backToAssets")}</Button></div>
    <div className="grid gap-3 md:grid-cols-2"><Card><CardContent className="space-y-2 p-4"><p className="text-sm text-muted-foreground">{t("applicationWebsites")}</p><p className="text-2xl font-semibold tabular-nums">{totalSize.toLocaleString(locale)}</p></CardContent></Card><Card><CardContent className="space-y-2 p-4"><p className="text-sm text-muted-foreground">{t("applicationEvidence")}</p><p className="text-sm">{(application.applicationSources ?? 0)>0 ? t("applicationRecognizedDescription") : t("applicationManualDescription")}</p></CardContent></Card></div>
    <section className="space-y-3"><div className="flex flex-wrap items-center justify-between gap-2"><h2 className="text-base font-semibold">{t("applicationWebsites")}</h2><AssetOrganizationFilter value={organizationId} onChange={changeOrganization} /></div>
      <Table><TableHeader><TableRow><TableHead>{t("url")}</TableHead><TableHead>{t("pageTitle")}</TableHead><TableHead>{t("applicationEvidence")}</TableHead><TableHead>{t("firstSeen")}</TableHead></TableRow></TableHeader><TableBody>
        {websites.map(site => <TableRow key={site.id}><TableCell className="max-w-md break-all"><a className="text-primary underline decoration-primary/40 underline-offset-4" href={site.url} target="_blank" rel="noopener noreferrer">{site.url}</a></TableCell><TableCell>{site.title || "—"}</TableCell><TableCell><span>{t(`evidence.${site.evidenceKind}`)}</span>{site.evidenceValue && <span className="ml-1 text-xs text-muted-foreground">{site.evidenceValue}</span>}</TableCell><TableCell>{new Date(site.firstSeenAt).toLocaleString(locale)}</TableCell></TableRow>)}
        {!websites.length && <TableRow><TableCell colSpan={4}>{t("applicationWebsitesEmpty")}</TableCell></TableRow>}
      </TableBody></Table>
      <AssetPagination page={page} total={totalSize} pageSize={50} onPageChange={setPage} />
    </section>
  </div></div>
}
