"use client"

import * as React from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useLocale, useTranslations } from "next-intl"
import { ArrowLeft, ArrowRight, Building2, Globe, Network, Server } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { useOrganizations } from "@/hooks/use-organizations"
import { useAssetEntities, useOrganizationAssetOverview } from "@/hooks/use-asset-entities"
import { assetInventoryHref, type InventoryFilter } from "@/lib/asset-inventory-navigation"
import { normalizeError } from "@/lib/errors/normalize-error"
import { AssetEntityList } from "./asset-entity-list"
import { CdnVisibilityToggle } from "./cdn-visibility-toggle"

export function AssetOrganizationDirectory() {
  const t = useTranslations("assetInventory")
  const [search, setSearch] = React.useState("")
  const [query, setQuery] = React.useState("")
  const [pages, setPages] = React.useState([""])
  React.useEffect(() => {
    const timer = window.setTimeout(() => { setQuery(search.trim()); setPages([""]) }, 250)
    return () => window.clearTimeout(timer)
  }, [search])
  const list = useOrganizations({ pageSize: 24, pageToken: pages.at(-1) || undefined, filter: query ? `displayName=${JSON.stringify(query)}` : undefined })
  const next = list.data?.nextPageToken
  return <section className="space-y-4">
    <div className="flex flex-wrap items-center gap-3">
      <h2 className="mr-auto font-semibold">{t("chooseOrganization")}</h2>
      <Input type="search" className="w-64 max-w-full" value={search} onChange={event => setSearch(event.target.value)} aria-label={t("searchOrganizations")} placeholder={t("searchOrganizations")} />
      <Button size="sm" variant="outline" render={<Link href={assetInventoryHref({ view: "inventory" })} />}>{t("allAssets")}</Button>
    </div>
    {list.error ? <AppErrorState error={normalizeError(list.error)} onRetry={list.refetch} /> : list.isPending ? <Skeleton className="h-28 w-full" /> : <>
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3" aria-busy={list.isFetching}>
        {list.data.organizations.map(org => <Link key={org.id} href={assetInventoryHref({ organizationId: org.id })} className="group flex min-w-0 items-center gap-3 rounded-xl border bg-card p-4 transition-colors hover:border-primary/50 focus-visible:outline-2 focus-visible:outline-ring">
          <Building2 aria-hidden className="size-5 shrink-0 text-primary" /><div className="min-w-0 flex-1"><span className="block truncate font-medium">{org.name}</span>{org.description && <span className="block truncate text-sm text-muted-foreground">{org.description}</span>}</div><ArrowRight aria-hidden className="size-4 text-muted-foreground group-hover:text-primary" />
        </Link>)}
        {!list.data.organizations.length && <p className="col-span-full rounded-lg border py-8 text-center text-sm text-muted-foreground">{t("noOrganizations")}</p>}
      </div>
      <div className="flex items-center gap-2"><span className="mr-auto text-sm text-muted-foreground">{t("organizationTotal", { count: list.data.total })}</span>
        <Button size="sm" variant="outline" disabled={pages.length < 2 || list.isFetching} onClick={() => setPages(old => old.slice(0, -1))}>{t("previous")}</Button>
        <Button size="sm" variant="outline" disabled={!next || list.isFetching} onClick={() => { if (next) setPages(old => [...old, next]) }}>{t("next")}</Button>
      </div>
    </>}
  </section>
}

const metrics = [
  { key: "rootDomains", type: "root_domain", icon: Globe },
  { key: "subdomains", type: "subdomain", icon: Network },
  { key: "ips", type: "ip", icon: Server },
] as const

function OrganizationRootDomains({ id, showCdn }: { id: number; showCdn: boolean }) {
  const t = useTranslations("assetInventory")
  const [pages, setPages] = React.useState([""])
  const roots = useAssetEntities({ organizationId: id, assetType: "root_domain", hideCdn: !showCdn, pageSize: 12, pageToken: pages.at(-1) || undefined })
  const next = roots.data?.nextPageToken
  if (roots.error) return <AppErrorState error={normalizeError(roots.error)} onRetry={roots.refetch} />
  if (roots.isPending) return <Skeleton className="h-24 w-full" />
  return <section className="rounded-xl border bg-card p-4">
    <h3 className="mb-2 text-sm font-semibold">{t("root_domain")}</h3>
    <ul className="grid gap-x-6 sm:grid-cols-2">
      {roots.data.assetEntities.map(root => <li key={root.id}>
        <Link href={assetInventoryHref({ path: `/assets/root-domains/${encodeURIComponent(root.id)}/`, organizationId: id, showCdn, from: "overview" })}
          className="flex items-center justify-between gap-2 border-t py-2 text-sm text-primary hover:underline focus-visible:outline-2 focus-visible:outline-ring">
          <span className="min-w-0 break-all">{root.label}</span><ArrowRight aria-hidden className="size-3.5 shrink-0" />
        </Link>
      </li>)}
    </ul>
    {!roots.data.assetEntities.length && <p className="text-sm text-muted-foreground">{t("noRootDomains")}</p>}
    {(pages.length > 1 || next) && <div className="mt-3 flex justify-end gap-2">
      <Button size="sm" variant="outline" disabled={pages.length < 2 || roots.isFetching} onClick={() => setPages(old => old.slice(0, -1))}>{t("previous")}</Button>
      <Button size="sm" variant="outline" disabled={!next || roots.isFetching} onClick={() => { if (next) setPages(old => [...old, next]) }}>{t("next")}</Button>
    </div>}
  </section>
}

export function OrganizationAssets({ id, showCdn, details, initialSearch, type }: { id: number; showCdn: boolean; details: boolean; initialSearch: string; type: InventoryFilter }) {
  const t = useTranslations("assetInventory")
  const locale = useLocale()
  const router = useRouter()
  const overview = useOrganizationAssetOverview(id, !showCdn)
  const href = (filter: InventoryFilter) => assetInventoryHref({ organizationId: id, view: "inventory", type: filter, showCdn })
  const toggleCdn = (value: boolean) => router.replace(assetInventoryHref({ organizationId: id, view: details ? "inventory" : undefined, type, showCdn: value, q: initialSearch }), { scroll: false })
  if (overview.error) return <AppErrorState error={normalizeError(overview.error)} onRetry={overview.refetch} actionHref="/assets/" />
  if (overview.isPending) return <Skeleton className="h-32 w-full" />
  return <section className="space-y-4">
    <div className="flex flex-wrap items-center gap-3"><h2 className="mr-auto text-xl font-semibold">{overview.data.name}</h2>
      <CdnVisibilityToggle shown={showCdn} onChange={toggleCdn} />
      <Button size="sm" variant="outline" render={<Link href={details ? assetInventoryHref({ organizationId: id, showCdn }) : "/assets/"} />}><ArrowLeft />{t(details ? "organizationOverview" : "chooseOrganization")}</Button>
    </div>
    {details ? <AssetEntityList key={id} organizationId={id} fixedOrganization initialType={type} showCdn={showCdn} initialSearch={initialSearch} onTypeChange={next => router.replace(assetInventoryHref({ organizationId: id, view: "inventory", type: next, showCdn, q: initialSearch }), { scroll: false })} /> : <>
      <div className="grid gap-3 sm:grid-cols-3">{metrics.map(({ key, type: assetType, icon: Icon }) => <Link key={key} href={href(assetType)} aria-label={`${t(assetType)} ${overview.data[key].toLocaleString(locale)}`} className="group rounded-xl border bg-card p-4 transition-colors hover:border-primary/50 focus-visible:outline-2 focus-visible:outline-ring">
        <span className="flex items-center gap-2 text-sm text-muted-foreground"><Icon className="size-4" />{t(assetType)}<ArrowRight className="ml-auto size-4" /></span>
        <span className="mt-2 block text-2xl font-semibold tabular-nums">{overview.data[key].toLocaleString(locale)}</span>
      </Link>)}</div>
      <OrganizationRootDomains key={`${id}:${showCdn}`} id={id} showCdn={showCdn} />
      <Button render={<Link href={href("all")} />}>{t("viewInventory")}<ArrowRight /></Button>
    </>}
  </section>
}
