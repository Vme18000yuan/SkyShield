import Link from "next/link"
import { getTranslations } from "next-intl/server"
import { PageHeader } from "@/components/common/page-header"
import { AssetEntityList } from "@/components/assets/asset-entity-list"
import { AssetOrganizationDirectory, OrganizationAssets } from "@/components/assets/organization-assets"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"
import { Button } from "@/components/ui/button"
import { inventoryFilter } from "@/lib/asset-inventory-navigation"

type SearchParams = Record<string, string | string[] | undefined>

export default async function AssetsPage({ searchParams }: { searchParams: Promise<SearchParams> }) {
  const t = await getTranslations("assetInventory")
  const { organizationId, q, view, type, showCdn } = await searchParams
  const organization = typeof organizationId === "string" && /^[1-9]\d*$/.test(organizationId) ? Number(organizationId) : 0
  const query = typeof q === "string" && Buffer.byteLength(q, "utf8") <= 200 ? q : ""
  const details = view === "inventory" || !!query
  let content = <AssetOrganizationDirectory />
  if (organization > 0 && organization <= 2147483647) {
    content = <OrganizationAssets key={organization} id={organization} showCdn={showCdn === "1"} details={details} type={inventoryFilter(type)} initialSearch={query} />
  } else if (organizationId === undefined && details) {
    content = <div className="space-y-4">
      <Button size="sm" variant="outline" render={<Link href="/assets/" />}>{t("chooseOrganization")}</Button>
      <AssetEntityList key={`${inventoryFilter(type)}:${query}:${showCdn === "1"}`} initialSearch={query} initialType={inventoryFilter(type)} initialShowCdn={showCdn === "1"} />
    </div>
  }
  return <div className={COMPACT_PAGE_SHELL_CLASS}>
    <PageHeader code="AST-01" title={t("title")} description={t("description")} />
    <div className={COMPACT_CONTENT_GUTTER_CLASS}>{content}</div>
  </div>
}
