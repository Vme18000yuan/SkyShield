import { getTranslations } from "next-intl/server"
import { PageHeader } from "@/components/common/page-header"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"
import { FofaSearchWorkspace } from "@/components/tools/space-search/fofa-search-workspace"

export default async function SpaceSearchPage() {
  const t = await getTranslations("spaceSearch")
  return <div className={COMPACT_PAGE_SHELL_CLASS}><PageHeader code="TOOLS-04" title={t("title")} description={t("description")} /><div className={COMPACT_CONTENT_GUTTER_CLASS}><FofaSearchWorkspace /></div></div>
}
