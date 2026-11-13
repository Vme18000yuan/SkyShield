// Adapted from ARTEX, AGPL-3.0-only; see third_party/artex/SKYSHIELD-NOTICE.md.
"use client"

import * as React from "react"
import Link from "next/link"
import { RotateCcwIcon } from "lucide-react"
import { useTranslations } from "next-intl"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { FindingRetestDialog } from "@/components/ai/finding-retest-dialog"
import { Markdown } from "@/components/settings/ai/artex-markdown"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { aiFindingRetestService, type AIFindingRetest } from "@/services/ai-finding-retest.service"

const active = (item: AIFindingRetest) => item.status === "pending" || item.status === "running"

export function FindingRetestPanel({ findingId, findingName, readOnly = false, onCompleted }: {
  findingId: string
  findingName?: string
  readOnly?: boolean
  onCompleted?: () => void
}) {
  const t = useTranslations("vulnerabilities.detail")
  const cache = useQueryClient()
  const [open, setOpen] = React.useState(false)
  const previous = React.useRef<AIFindingRetest[]>([])
  const key = ["aiFindingRetests", findingId]
  const history = useQuery({
    queryKey: key,
    queryFn: () => aiFindingRetestService.history(findingId),
    refetchInterval: query => query.state.data?.some(active) ? 3000 : false,
  })
  React.useEffect(() => {
    if (!history.data) return
    const completed = previous.current.some(item => active(item) && history.data?.some(row => row.id === item.id && !active(row)))
    previous.current = history.data
    if (completed) onCompleted?.()
  }, [history.data, onCompleted])
  const running = history.data?.find(active)
  return <Card>
    <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
      <div className="flex flex-col gap-1.5"><CardTitle>{t("retestTitle")}</CardTitle><CardDescription>{t("retestPanelDescription")}</CardDescription></div>
      {running?.conversation_id ? <Button render={<Link href={`/ai/chat/?c=${encodeURIComponent(running.conversation_id)}`} />} variant="outline" size="sm"><Spinner data-icon="inline-start" aria-hidden="true" />{t("retestStatus.running")}</Button> : null}
      {!running && !readOnly ? <Button size="sm" onClick={() => setOpen(true)} disabled={history.isLoading || history.isError}><RotateCcwIcon data-icon="inline-start" />{t("retestLaunch")}</Button> : null}
    </CardHeader>
    <CardContent className="flex flex-col gap-3">
      {history.isError ? <Alert variant="destructive"><AlertDescription>{t("retestLoadFailed")} <Button variant="outline" size="sm" onClick={() => void history.refetch()}>{t("retestRetry")}</Button></AlertDescription></Alert> : null}
      {history.isLoading ? <Skeleton className="h-16 w-full" /> : null}
      {!history.isLoading && !history.isError && !history.data?.length ? <Empty><EmptyHeader><EmptyTitle>{t("retestEmpty")}</EmptyTitle><EmptyDescription>{t("retestEmptyHint")}</EmptyDescription></EmptyHeader></Empty> : null}
      {history.data?.map(item => <div key={item.id} className="flex min-w-0 flex-col gap-2 rounded-lg border p-3">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant={item.status === "completed" && item.verdict === "reproduced" ? "destructive" : "secondary"}>{item.status === "completed" && item.verdict ? t(`retestVerdict.${item.verdict}`) : t(`retestStatus.${item.status}`)}</Badge>
          <span className="text-xs text-muted-foreground">#{item.id} · {new Date(item.created_at).toLocaleString()}</span>
          {item.conversation_id ? <Button render={<Link href={`/ai/chat/?c=${encodeURIComponent(item.conversation_id)}`} />} variant="ghost" size="sm" className="ml-auto">{t("retestConversation")}</Button> : <span className="text-xs text-muted-foreground">{t("retestConversationDeleted")}</span>}
        </div>
        {item.status === "completed" && item.summary ? <p className="whitespace-pre-wrap break-words text-sm">{item.summary}</p> : null}
        {item.error ? <p className="whitespace-pre-wrap break-words text-sm text-destructive">{item.error}</p> : null}
        {item.notes ? <p className="whitespace-pre-wrap break-words text-xs text-muted-foreground">{t("retestNotesPrefix", { notes: item.notes })}</p> : null}
        {item.status === "completed" && item.evidence ? <details className="min-w-0"><summary className="cursor-pointer text-sm">{t("retestEvidence")}</summary><div className="mt-3 overflow-x-auto"><Markdown text={item.evidence} /></div></details> : null}
      </div>)}
    </CardContent>
    {open ? <FindingRetestDialog findingId={findingId} findingName={findingName} onClose={() => setOpen(false)} onStarted={item => {cache.setQueryData<AIFindingRetest[]>(key, old => [item, ...(old ?? []).filter(row => row.id !== item.id)]); void cache.invalidateQueries({ queryKey: key })}} /> : null}
  </Card>
}
