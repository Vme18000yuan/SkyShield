"use client"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { ContentHandoff } from "@/components/shared/loading/content-handoff"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useAIToolSettings } from "@/hooks/use-ai-tool-settings"
import { normalizeError } from "@/lib/errors/normalize-error"
import type { AIAgentKey } from "@/types/ai-agent-setting.types"
import type { AIPlatformToolKey } from "@/types/ai-tool-setting.types"

const agents: AIAgentKey[] = ["assistant", "planner", "worker"]
const tools: AIPlatformToolKey[] = ["search_platform_assets", "insert_assets"]
export function AIToolSettings() {
  const t = useTranslations("aiToolSettings")
  const { list, save } = useAIToolSettings()
  if (list.error) return <AppErrorState error={normalizeError(list.error, { notFoundKind: "unexpected-error" })} onRetry={list.refetch} />
  return <ContentHandoff owner="ai-tool-settings-content" layer="workspace" isLoading={list.isPending}
    skeleton={<div className="flex flex-col gap-2" aria-label={t("loading")}>{[0, 1, 2].map(row => <Skeleton key={row} className="h-10 w-full" />)}</div>}>
    <div className="flex flex-col gap-3"><p className="text-sm text-muted-foreground">{t("scopeHint")}</p>
      <Table><TableHeader><TableRow><TableHead>{t("tool")}</TableHead>{agents.map(agent => <TableHead key={agent}>{t(agent)}</TableHead>)}</TableRow></TableHeader>
        <TableBody>{tools.map(tool => <TableRow key={tool}><TableCell><div>{t(tool)}</div><div className="text-sm text-muted-foreground">{t(`${tool}Description`)}</div></TableCell>
          {agents.map(agent => { const item = list.data?.find(entry => entry.agentKey === agent && entry.toolKey === tool)
            return <TableCell key={agent}>{item && <Switch checked={item.enabled} disabled={save.isPending} aria-label={`${t(agent)} - ${t(tool)}`} onCheckedChange={async enabled => {
              try { await save.mutateAsync({ ...item, enabled }); toast.success(t("saved")) } catch { toast.error(t("saveError")) }
            }} />}</TableCell> })}
        </TableRow>)}</TableBody>
      </Table>
    </div>
  </ContentHandoff>
}
