import { getTranslations } from "next-intl/server"
import { PageHeader } from "@/components/common/page-header"
import { AITaskList } from "@/components/ai/ai-task-list"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"

export default async function AITasksPage() {
  const t = await getTranslations("aiTasks")
  return <div className={COMPACT_PAGE_SHELL_CLASS}>
    <PageHeader code="AI-03" title={t("title")} description={t("description")} />
    <div className={COMPACT_CONTENT_GUTTER_CLASS}><AITaskList /></div>
  </div>
}
