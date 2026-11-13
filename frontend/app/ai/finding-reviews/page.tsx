import { getTranslations } from "next-intl/server"
import { PageHeader } from "@/components/common/page-header"
import { GlobalFindingReview } from "@/components/ai/artex/global-finding-review"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"

export default async function FindingReviewsPage() {
  const t = await getTranslations("aiGlobalFindingReview")
  return <div className={COMPACT_PAGE_SHELL_CLASS}><PageHeader code="AI-07" title={t("title")} description={t("description")} /><div className={COMPACT_CONTENT_GUTTER_CLASS}><GlobalFindingReview /></div></div>
}
