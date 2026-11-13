import ArtexAgentSettings from "@/components/settings/ai/artex-agent-settings"
import { COMPACT_CONTENT_GUTTER_CLASS, COMPACT_PAGE_SHELL_CLASS } from "@/components/shared/layout/page-shell-density"

export default function AIAgentSettingsPage() {
  return <div className={COMPACT_PAGE_SHELL_CLASS}>
    <div className={COMPACT_CONTENT_GUTTER_CLASS}><ArtexAgentSettings /></div>
  </div>
}
