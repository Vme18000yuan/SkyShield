import ArtexLLMSettings from "@/components/settings/ai/artex-llm-settings"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"

export default function AIModelSettingsPage() {
  return (
    <div className={COMPACT_PAGE_SHELL_CLASS}>
      <div className={COMPACT_CONTENT_GUTTER_CLASS}><ArtexLLMSettings /></div>
    </div>
  )
}
