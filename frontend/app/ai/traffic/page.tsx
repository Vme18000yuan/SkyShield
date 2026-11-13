import { getTranslations } from "next-intl/server"
import { PageHeader } from "@/components/common/page-header"
import { GlobalTraffic } from "@/components/ai/artex/task-traffic"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"

export default async function AITrafficPage() {
  const t=await getTranslations("aiTasks.traffic")
  return <div className={COMPACT_PAGE_SHELL_CLASS}><PageHeader code="AI-06" title={t("globalTitle")} description={t("globalDescription")}/><div className={COMPACT_CONTENT_GUTTER_CLASS}><GlobalTraffic/></div></div>
}
