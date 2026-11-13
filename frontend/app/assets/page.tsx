import { getTranslations } from "next-intl/server"
import { PageHeader } from "@/components/common/page-header"
import { AssetEntityList } from "@/components/assets/asset-entity-list"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"

export default async function AssetsPage({ searchParams }: { searchParams: Promise<{ organizationId?: string | string[]; q?: string | string[] }> }) {
  const t = await getTranslations("assetInventory")
  const { organizationId, q } = await searchParams
  const initialOrganizationId = typeof organizationId === "string" && /^[1-9]\d*$/.test(organizationId) && Number.isSafeInteger(Number(organizationId)) && Number(organizationId) <= 2147483647 ? Number(organizationId) : undefined
  const initialSearch = typeof q === "string" && Buffer.byteLength(q, "utf8") <= 200 ? q : ""
  return (
    <div className={COMPACT_PAGE_SHELL_CLASS}>
      <PageHeader code="AST-01" title={t("title")} description={t("description")} />
      <div className={COMPACT_CONTENT_GUTTER_CLASS}>
        <AssetEntityList initialOrganizationId={initialOrganizationId} initialSearch={initialSearch} />
      </div>
    </div>
  )
}
