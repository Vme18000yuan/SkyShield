import { AITaskDetail } from "@/components/ai/ai-task-detail"
import { COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"

export default async function AITaskDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params
  return <div className={COMPACT_PAGE_SHELL_CLASS}><AITaskDetail id={id} /></div>
}
