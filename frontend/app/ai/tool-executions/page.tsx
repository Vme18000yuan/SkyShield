import { getTranslations } from "next-intl/server"
import { PageHeader } from "@/components/common/page-header"
import { AIToolExecutionList } from "@/components/ai/ai-tool-execution-list"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"

export default async function AIToolExecutionsPage() {
  const t = await getTranslations("aiTools")
  return <div className={COMPACT_PAGE_SHELL_CLASS}>
    <PageHeader code="AI-02" title={t("title")} description={t("description")} />
    <div className={COMPACT_CONTENT_GUTTER_CLASS}><AIToolExecutionList /></div>
  </div>
}
