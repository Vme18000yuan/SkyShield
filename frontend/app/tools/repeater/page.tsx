import { getTranslations } from "next-intl/server"
import { PageHeader } from "@/components/common/page-header"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"
import { RepeaterWorkspace } from "@/components/tools/repeater/repeater-workspace"

export default async function RepeaterPage() {
  const t = await getTranslations("repeater")
  return <div className={COMPACT_PAGE_SHELL_CLASS}><PageHeader code="TOOLS-05" title={t("title")} description={t("description")} /><div className={COMPACT_CONTENT_GUTTER_CLASS}><RepeaterWorkspace /></div></div>
}
