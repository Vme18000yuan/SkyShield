import { getTranslations } from "next-intl/server"
import { PageHeader } from "@/components/common/page-header"
import { OrganizationMonitorLauncher } from "@/components/asset-monitor/organization-launcher"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"

export default async function AssetMonitorPage() {
  const t = await getTranslations("assetMonitor")
  return <div className={COMPACT_PAGE_SHELL_CLASS}>
    <PageHeader code="MON-01" title={t("title")} description={t("description")} />
    <div className={COMPACT_CONTENT_GUTTER_CLASS}><OrganizationMonitorLauncher /></div>
  </div>
}
