"use client"

import * as React from "react"
import Link from "next/link"
import { useLocale, useTranslations } from "next-intl"
import { toast } from "sonner"
import { PageHeader } from "@/components/common/page-header"
import { AppErrorState } from "@/components/shared/feedback/app-error-state"
import { COMPACT_CONTENT_GUTTER_CLASS } from "@/components/shared/layout/page-shell-density"
import { ContentHandoff } from "@/components/shared/loading/content-handoff"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { FindingRetestPanel } from "@/components/ai/finding-retest-panel"
import { ApprovalRecords } from "@/components/ai/artex/approval-records"
import { TaskApprovalExecution } from "@/components/ai/artex/task-approval-execution"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { MentionTextarea } from "@/components/ai/artex/mention-textarea"
import { TaskConstraints } from "@/components/ai/artex/task-constraints"
import { TaskGoals } from "@/components/ai/artex/task-goals"
import { TaskInterceptRulesCard } from "@/components/ai/artex/task-intercept-rules"
import { TaskScopeCard } from "@/components/ai/artex/task-scope"
import { FindingTrafficPanel, TaskTraffic } from "@/components/ai/artex/task-traffic"
import { TaskMainSessions } from "@/components/ai/artex/task-main-sessions"
import { TaskIntelligence } from "@/components/ai/artex/task-intelligence"
import { TaskLLMUsage } from "@/components/ai/artex/task-llm-usage"
import { OASTPanel } from "@/components/ai/oast-panel"
import { TaskFindings } from "@/components/ai/artex/task-findings"
import { TaskCoverageGraph } from "@/components/ai/artex/task-coverage-graph"
import { useAITask, useAITaskActivities, useAITaskAssets, useAITaskMutations } from "@/hooks/use-ai-tasks"
import { AITaskModelChainDialog } from "@/components/ai/ai-task-model-chain-dialog"
import { normalizeError } from "@/lib/errors/normalize-error"
import { AITaskService } from "@/services/ai-task.service"
import type { AITaskAction, AITaskGraph } from "@/types/ai-task.types"
import { AITaskStatusBadge } from "./ai-task-list"

function nodeTitle(payload: Record<string, unknown>): string {
  for (const key of ["summary", "text", "name", "goal", "detail"]) {
    if (typeof payload?.[key] === "string" && payload[key]) return payload[key] as string
  }
  return "—"
}

function TaskGraph({ graph, taskId, taskRunning, sourceNames, refresh }: { graph: AITaskGraph; taskId: string; taskRunning: boolean; sourceNames: Record<string,string>; refresh: () => Promise<unknown> }) {
  const t = useTranslations("aiTasks")
  const [limit, setLimit] = React.useState(100)
  const [rerunning, setRerunning] = React.useState(false)
  const [messageIntent, setMessageIntent] = React.useState<{ id: string; title: string; requestId: string } | null>(null)
	const [deletingIntent, setDeletingIntent] = React.useState<{ id: string; title: string } | null>(null)
	const [deleteReason, setDeleteReason] = React.useState("")
	const [deleteMode, setDeleteMode] = React.useState<"soft" | "hard">("soft")
  const [workerMessage, setWorkerMessage] = React.useState("")
  const [retest, setRetest] = React.useState<{ id: string; title: string } | null>(null)
  const [evidenceFinding, setEvidenceFinding] = React.useState<{ id: string; title: string } | null>(null)
  const tRetest = useTranslations("vulnerabilities.detail")
  const rerun = async (intentId?: string) => {
    setRerunning(true)
    try {
      const count = await AITaskService.rerunIntent(taskId, intentId)
      await refresh()
      toast.success(t("rerunSuccess", { count }))
    } catch {
      toast.error(t("rerunError"))
    } finally { setRerunning(false) }
  }
  const controlIntent = async (intentId: string, action: "pause" | "resume") => {
    setRerunning(true)
    try {
      const state = await AITaskService.controlIntent(taskId, intentId, action)
      await refresh()
      if (state === "pending") toast.info(t("intentPausePending"))
      else toast.success(action === "pause" ? t("intentPaused") : t("intentResumed"))
    } catch {
      toast.error(t("intentControlError"))
    } finally { setRerunning(false) }
  }
  const sendWorkerMessage = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!messageIntent || !workerMessage.trim()) return
    setRerunning(true)
    try {
      await AITaskService.sendWorkerMessage(taskId, messageIntent.id, messageIntent.requestId, workerMessage.trim())
      await refresh()
      setMessageIntent(null)
      setWorkerMessage("")
      toast.success(t("workerMessageSent"))
    } catch {
      toast.error(t("workerMessageError"))
    } finally { setRerunning(false) }
  }
	const cancelIntent = async (event: React.FormEvent<HTMLFormElement>) => {
		event.preventDefault()
		if (!deletingIntent || !deleteReason.trim()) return
		setRerunning(true)
		try {
			const state = await AITaskService.cancelIntent(taskId, deletingIntent.id, deleteReason.trim(), deleteMode)
			await refresh()
			setDeletingIntent(null)
			setDeleteReason("")
			if (state === "pending") toast.info(t("intentDeletePending"))
			else toast.success(t("intentDeleted"))
		} catch { toast.error(t("intentControlError")) }
		finally { setRerunning(false) }
	}
  const byNode = React.useMemo(() => {
    const result = new Map<string, AITaskGraph["assets"]>()
    for (const asset of graph.assets) result.set(asset.nodeId, [...(result.get(asset.nodeId) ?? []), asset])
    return result
  }, [graph.assets])
  const edges = React.useMemo(() => {
    const result = new Map<string, AITaskGraph["edges"]>()
    for (const edge of graph.edges) result.set(edge.source, [...(result.get(edge.source) ?? []), edge])
    return result
  }, [graph.edges])
  return <div className="flex flex-col gap-2">
    {graph.nodes.some(node => !node.inherited && node.kind === "intent" && node.state === "blocked") && <div><Button variant="outline" size="sm" disabled={rerunning} onClick={() => void rerun()}>{t("rerunBlocked")}</Button></div>}
    {graph.truncated && <p className="text-sm text-muted-foreground">{t("graphTruncated")}</p>}
    {graph.nodes.slice(0, limit).map(node => <article key={node.id} className="rounded-md border bg-card p-3">
      <div className="flex flex-wrap items-center gap-2"><Badge variant="outline">{node.kind}</Badge><Badge variant="secondary">{node.state}</Badge>{node.inherited && <Badge variant="outline">继承 · {sourceNames[node.sourceTaskId ?? ""] ?? "来源任务"}</Badge>}<span className="text-xs text-muted-foreground">#{node.id}</span><span className="break-all text-sm font-medium">{nodeTitle(node.payload)}</span></div>
      {(byNode.get(node.id) ?? []).map(asset => <p key={asset.assetId} className="mt-1 break-all text-sm text-muted-foreground">{t("asset")}: {asset.label} ({asset.assetType} · #{asset.assetId})</p>)}
      {(edges.get(node.id) ?? []).map(edge => <p key={`${edge.relation}-${edge.target}`} className="mt-1 text-xs text-muted-foreground">{edge.relation} → #{edge.target}</p>)}
			{node.deleteReason && <p className="mt-1 text-sm text-muted-foreground">{t("intentDeleteReason")}: {node.deleteReason}</p>}
			{node.deletionState === "pending" && <p className="mt-1 text-sm text-muted-foreground">{t("intentDeletePending")}</p>}
			{node.deletionState === "conflict" && <p role="alert" className="mt-1 text-sm text-destructive">{t("intentDeleteConflict")}</p>}
      {node.findingId && !node.inherited ? <Button className="mt-2" variant="outline" size="sm" onClick={() => setRetest({ id: node.findingId!, title: nodeTitle(node.payload) })}>{tRetest("retestTab")}</Button> : null}
      {node.findingId && !node.inherited ? <Button className="mt-2 ml-2" variant="outline" size="sm" onClick={() => setEvidenceFinding({ id: node.findingId!, title: nodeTitle(node.payload) })}>{t("traffic.evidence")}</Button> : null}
      {!node.inherited && node.kind === "intent" && ["blocked", "exhausted", "stopped"].includes(node.state) ? <Button className="mt-2" variant="outline" size="sm" disabled={rerunning} onClick={() => void rerun(node.id)}>{t("rerunIntent")}</Button> : null}
      {!node.inherited && node.kind === "intent" && node.state === "running" && taskRunning ? <Button className="mt-2" variant="outline" size="sm" disabled={rerunning || node.deletionState === "pending"} onClick={() => void controlIntent(node.id, "pause")}>{t("pauseIntent")}</Button> : null}
      {!node.inherited && node.kind === "intent" && node.state === "paused" ? <Button className="mt-2" variant="outline" size="sm" disabled={rerunning || node.deletionState === "pending"} onClick={() => void controlIntent(node.id, "resume")}>{t("resumeIntent")}</Button> : null}
      {!node.inherited && node.kind === "intent" && node.state === "paused" && taskRunning ? <Button className="mt-2 ml-2" variant="outline" size="sm" disabled={rerunning || node.deletionState === "pending"} onClick={() => { setWorkerMessage(""); setMessageIntent({ id: node.id, title: nodeTitle(node.payload), requestId: globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}` }) }}>{t("workerMessage")}</Button> : null}
			{!node.inherited && node.kind === "intent" && ["open", "running", "paused"].includes(node.state) ? <Button className="mt-2 ml-2" variant="outline" size="sm" disabled={rerunning || node.deletionState === "pending"} onClick={() => { setDeleteReason(""); setDeleteMode("soft"); setDeletingIntent({ id: node.id, title: nodeTitle(node.payload) }) }}>{t("deleteIntent")}</Button> : null}
      <details className="mt-1 text-xs"><summary className="cursor-pointer text-muted-foreground">{t("viewData")}</summary><pre className="mt-1 overflow-auto whitespace-pre-wrap break-all">{JSON.stringify(node.payload, null, 2)}</pre></details>
    </article>)}
    {!graph.nodes.length && <p className="text-sm text-muted-foreground">{t("noGraph")}</p>}
    {graph.nodes.length > limit && <Button variant="outline" onClick={() => setLimit(value => value + 100)}>{t("more")}</Button>}
    <Dialog open={!!retest} onOpenChange={open => { if (!open) setRetest(null) }}>
      <DialogContent className="max-h-[85vh] overflow-auto">
        <DialogHeader><DialogTitle>{tRetest("retestTab")}</DialogTitle><DialogDescription>{retest?.title}</DialogDescription></DialogHeader>
        {retest ? <FindingRetestPanel findingId={retest.id} findingName={retest.title} /> : null}
      </DialogContent>
    </Dialog>
    <Dialog open={!!evidenceFinding} onOpenChange={open => { if (!open) setEvidenceFinding(null) }}><DialogContent className="max-h-[85vh] overflow-auto sm:max-w-3xl"><DialogHeader><DialogTitle>{t("traffic.evidence")}</DialogTitle><DialogDescription>{evidenceFinding?.title}</DialogDescription></DialogHeader>{evidenceFinding && <FindingTrafficPanel taskId={taskId} findingId={evidenceFinding.id} />}</DialogContent></Dialog>
    <Dialog open={!!messageIntent} onOpenChange={open => { if (!open && !rerunning) setMessageIntent(null) }}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader><DialogTitle>{t("workerMessage")}</DialogTitle><DialogDescription>{messageIntent?.title}</DialogDescription></DialogHeader>
        <form className="flex flex-col items-end gap-3" onSubmit={event => void sendWorkerMessage(event)}>
          <MentionTextarea aria-label={t("workerMessageLabel")} value={workerMessage} maxLength={4000} onValueChange={setWorkerMessage} placeholder={t("workerMessagePlaceholder")} disabled={rerunning} />
          <Button type="submit" disabled={rerunning || !workerMessage.trim()}>{rerunning ? t("sending") : t("sendMessage")}</Button>
        </form>
      </DialogContent>
    </Dialog>
		<Dialog open={!!deletingIntent} onOpenChange={open => { if (!open && !rerunning) setDeletingIntent(null) }}>
			<DialogContent>
				<DialogHeader><DialogTitle>{t("deleteIntent")}</DialogTitle><DialogDescription>{deletingIntent?.title} · {t("intentDeleteDescription")}</DialogDescription></DialogHeader>
				<form className="flex flex-col items-end gap-3" onSubmit={event => void cancelIntent(event)}>
					<fieldset className="w-full space-y-2 text-sm"><legend className="mb-1 font-medium">{t("intentDeleteMode")}</legend>
						<label className="flex items-start gap-2"><input type="radio" name="delete-mode" value="soft" checked={deleteMode === "soft"} onChange={() => setDeleteMode("soft")} disabled={rerunning} /><span><strong>{t("intentDeleteSoft")}</strong> · {t("intentDeleteSoftDescription")}</span></label>
						<label className="flex items-start gap-2"><input type="radio" name="delete-mode" value="hard" checked={deleteMode === "hard"} onChange={() => setDeleteMode("hard")} disabled={rerunning} /><span><strong>{t("intentDeleteHard")}</strong> · {t("intentDeleteHardDescription")}</span></label>
					</fieldset>
					<Textarea aria-label={t("intentDeleteReason")} placeholder={t("intentDeleteReasonPlaceholder")} value={deleteReason} onChange={event => setDeleteReason(event.target.value)} maxLength={4000} disabled={rerunning} />
					<Button type="submit" disabled={rerunning || !deleteReason.trim()}>{t("confirmDeleteIntent")}</Button>
				</form>
			</DialogContent>
		</Dialog>
  </div>
}

export function AITaskDetail({ id }: { id: string }) {
  const t = useTranslations("aiTasks")
  const locale = useLocale()
  const { task, graph } = useAITask(id)
  const activities = useAITaskActivities(id)
  const { control } = useAITaskMutations()
  const [activeTab, setActiveTab] = React.useState("graph")
  React.useEffect(() => { const params = new URLSearchParams(window.location.search); if (params.get("tab") === "approvals") setActiveTab("approvals"); else if (params.get("tab") === "activity" && params.has("approval")) setActiveTab("activity"); else if (params.get("tab") === "findings" || params.has("finding")) setActiveTab("findings") }, [])
  const [assetCursors, setAssetCursors] = React.useState([""])
  const [assetPage, setAssetPage] = React.useState(0)
  const [sourceNames, setSourceNames] = React.useState<Record<string,string>>({})
  const sourceKey = (task.data?.sourceTaskIds ?? []).join(",")
  React.useEffect(() => {
    let active = true
    const ids = sourceKey ? sourceKey.split(",") : []
    void Promise.allSettled(ids.map(source => AITaskService.get(source))).then(results => {
      if (!active) return
      const names: Record<string,string> = {}
      results.forEach((result,index) => { if (result.status === "fulfilled") names[ids[index]] = result.value.displayName })
      setSourceNames(names)
    })
    return () => { active = false }
  }, [sourceKey])
  const assetCursor = assetCursors[assetPage] ?? ""
  const assets = useAITaskAssets(id, assetCursor, activeTab === "assets")
  React.useEffect(() => {
    setAssetCursors([""])
    setAssetPage(0)
  }, [id])
  if (task.error) return <div className={COMPACT_CONTENT_GUTTER_CLASS}><AppErrorState error={normalizeError(task.error)} onRetry={task.refetch} /></div>
  const item = task.data
  const doAction = async (action: AITaskAction) => {
    try { await control.mutateAsync({ id, action }); toast.success(t("actionSuccess")) }
    catch { toast.error(t("actionError")) }
  }
  return <>
    <PageHeader code="AI-03" title={item?.displayName || t("title")} description={item?.goal}
      action={<Button variant="outline" render={<Link href="/ai/tasks/" />}>{t("back")}</Button>} />
    <div className={COMPACT_CONTENT_GUTTER_CLASS}>
      <ContentHandoff owner="ai-task-detail-content" layer="workspace" isLoading={task.isPending}
        skeleton={<div className="flex flex-col gap-2" aria-label={t("loading")}><Skeleton className="h-16 w-full" /><Skeleton className="h-48 w-full" /></div>}>
        {item && <div className="flex min-w-0 flex-col gap-4">
          <div className="flex flex-wrap items-center gap-2"><AITaskStatusBadge task={item} />
			<AITaskModelChainDialog task={item} onSaved={async()=>{await task.refetch()}} />
            {item.status === "created" && !item.queued && <Button size="sm" disabled={control.isPending} onClick={() => void doAction("start")}>{t("start")}</Button>}
            {item.status === "paused" && <Button size="sm" disabled={control.isPending} onClick={() => void doAction("resume")}>{t("resume")}</Button>}
            {(item.status === "running" || item.queued) && <Button variant="outline" size="sm" disabled={control.isPending} onClick={() => void doAction("pause")}>{t("pause")}</Button>}
            {(item.status === "running" || item.status === "paused" || item.queued || item.status === "created") && <Button variant="outline" size="sm" disabled={control.isPending} onClick={() => void doAction("stop")}>{t("stop")}</Button>}
          </div>
          <div className="grid gap-2 rounded-md border bg-card p-3 text-sm md:grid-cols-3">
            <div><span className="text-muted-foreground">{t("target")}: </span>{item.targetId ?? t("allAssets")}</div>
            <div><span className="text-muted-foreground">{t("workers")}: </span>{item.workerCount || "全局默认"}</div>
            <div><span className="text-muted-foreground">{t("seconds")}: </span>{item.timeoutSeconds || "不限时"}</div>
            {item.description && <div className="break-words md:col-span-3">{item.description}</div>}
            {item.errorMessage && <div className="break-words text-destructive md:col-span-3">{item.errorMessage}</div>}
            {!!item.sourceTaskIds?.length && <div className="flex flex-wrap gap-2 md:col-span-3"><span className="text-muted-foreground">关联来源任务:</span>{item.sourceTaskIds.map(source => <Link className="text-primary hover:underline" key={source} href={`/ai/tasks/${source}/`}>{sourceNames[source] ?? "来源任务"}</Link>)}</div>}
          </div>
          <Tabs value={activeTab} onValueChange={setActiveTab}><div className="max-w-full overflow-x-auto"><TabsList variant="content" className="w-max"><TabsTrigger value="graph">{t("graph")}</TabsTrigger><TabsTrigger value="assets">{t("assets")}</TabsTrigger><TabsTrigger value="findings">{t("findings")}</TabsTrigger><TabsTrigger value="coverageGraph">{t("coverageGraph.title")}</TabsTrigger><TabsTrigger value="intelligence">{t("intelligence.title")}</TabsTrigger><TabsTrigger value="traffic">{t("traffic.title")}</TabsTrigger><TabsTrigger value="oast">{t("oast")}</TabsTrigger><TabsTrigger value="mainagent">{t("mainAgent")}</TabsTrigger><TabsTrigger value="activity">{t("activity")}</TabsTrigger><TabsTrigger value="approvals">{t("approvals")}</TabsTrigger></TabsList></div>
            <TabsContent value="graph">{graph.error ? <AppErrorState error={normalizeError(graph.error)} onRetry={graph.refetch} /> : graph.isPending ? <Skeleton className="h-48 w-full" /> : graph.data && <TaskGraph graph={graph.data} taskId={id} taskRunning={item.status === "running"} sourceNames={sourceNames} refresh={() => Promise.all([graph.refetch(), task.refetch()])} />}<TaskLLMUsage taskId={id} running={item.status === "running"} /><TaskScopeCard taskId={id} sourceNames={sourceNames} running={item.status === "running"} /><TaskGoals taskId={id} /><TaskConstraints taskId={id} /><TaskInterceptRulesCard taskId={id} /></TabsContent>
            <TabsContent value="assets">
              <div className="flex flex-col gap-3">
                <p className="text-sm text-muted-foreground">{t("assetsDescription")}</p>
                {assets.error && <AppErrorState error={normalizeError(assets.error)} onRetry={assets.refetch} />}
                {assets.isPending ? <Skeleton className="h-48 w-full" /> : !assets.error && <>
                  <div className="overflow-x-auto rounded-md border">
                    <Table><TableHeader><TableRow><TableHead>{t("asset")}</TableHead><TableHead>{t("assetType")}</TableHead><TableHead>{t("assetSource")}</TableHead><TableHead className="text-right">{t("factCount")}</TableHead></TableRow></TableHeader>
                      <TableBody>{assets.data?.assets.map(asset => <TableRow key={asset.id}>
                        <TableCell className="max-w-xl break-all font-medium">{asset.label}</TableCell>
                        <TableCell><Badge variant="outline">{asset.assetType}</Badge></TableCell>
                        <TableCell>{asset.inherited ? `继承 · ${sourceNames[asset.sourceTaskId ?? ""] ?? "来源任务"}` : asset.source === "selected" ? t("sourceSelected") : asset.source === "discovered" ? t("sourceDiscovered") : asset.source === "anchored" ? t("sourceAnchored") : asset.source === "scope" ? "任务范围" : asset.source}</TableCell>
                        <TableCell className="text-right tabular-nums">{asset.factCount}</TableCell>
                      </TableRow>)}{!assets.data?.assets.length && <TableRow><TableCell colSpan={4} className="text-muted-foreground">{t("noAssets")}</TableCell></TableRow>}</TableBody>
                    </Table>
                  </div>
                  <div className="flex items-center justify-end gap-2"><span className="text-sm text-muted-foreground">{t("page", { number: assetPage + 1 })}</span>
                    <Button variant="outline" size="sm" disabled={assetPage === 0 || assets.isFetching} onClick={() => setAssetPage(value => value - 1)}>{t("previous")}</Button>
                    <Button variant="outline" size="sm" disabled={!assets.data?.nextAfter || assets.isFetching} onClick={() => { const next = assets.data?.nextAfter; if (next) { setAssetCursors(current => [...current.slice(0, assetPage + 1), next]); setAssetPage(value => value + 1) } }}>{t("next")}</Button>
                  </div>
                </>}
              </div>
            </TabsContent>
            <TabsContent value="coverageGraph">{activeTab === "coverageGraph" && <TaskCoverageGraph key={id} taskId={id} coverageEnabled={item.coverageEnabled} sourceNames={sourceNames} />}</TabsContent>
            <TabsContent value="findings">{activeTab === "findings" && <TaskFindings key={id} taskId={id} />}</TabsContent>
            <TabsContent value="intelligence">{activeTab === "intelligence" && <TaskIntelligence key={id} taskId={id} sourceNames={sourceNames} running={item.status === "running"} />}</TabsContent>
            <TabsContent value="traffic">{activeTab === "traffic" && <TaskTraffic key={id} taskId={id} findings={(graph.data?.nodes ?? []).filter(node=>node.findingId && !node.inherited).map(node=>({id:node.findingId!,title:nodeTitle(node.payload)}))} />}</TabsContent>
            <TabsContent value="oast">{activeTab === "oast" && <OASTPanel key={id} scope={{ kind: "task", id }} />}</TabsContent>
            <TabsContent value="mainagent">{activeTab === "mainagent" && <TaskMainSessions key={id} taskId={id} status={item.status} />}</TabsContent>
            <TabsContent value="approvals">{activeTab === "approvals" ? <ApprovalRecords taskId={id} /> : null}</TabsContent>
            <TabsContent value="activity"><div className="flex flex-col gap-2">
              {activeTab === "activity" && <TaskApprovalExecution taskId={id} />}
              {Boolean(activities.error) && <p className="text-sm text-destructive">{t("activityError")}</p>}
              {activities.truncated && <p className="text-sm text-muted-foreground">{t("activityTruncated")}</p>}
              {activities.items.map(activity => <article key={activity.id} className="rounded-md border bg-card p-3 text-sm">
                <div className="flex flex-wrap items-center gap-2"><Badge variant={activity.isError ? "error" : "outline"}>{activity.tool || activity.kind}</Badge><span>{activity.worker}</span><span className="text-xs text-muted-foreground">{new Date(activity.createdAt).toLocaleString(locale)}</span></div>
                {activity.summary && <p className="mt-1 whitespace-pre-wrap break-words">{activity.summary}</p>}
                {activity.detail && <details className="mt-1"><summary className="cursor-pointer text-muted-foreground">{t("viewData")}</summary><pre className="mt-1 overflow-auto whitespace-pre-wrap break-all">{activity.detail}</pre></details>}
              </article>)}
              {!activities.items.length && !activities.error && <p className="text-sm text-muted-foreground">{t("noActivity")}</p>}
            </div></TabsContent>
          </Tabs>
        </div>}
      </ContentHandoff>
    </div>
  </>
}
