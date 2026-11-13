"use client"

import * as React from "react"
import { useRouter } from "next/navigation"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { useQueryClient } from "@tanstack/react-query"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { useAITaskMutations } from "@/hooks/use-ai-tasks"
import { TaskLLMProfileChain } from "@/components/ai/artex/task-llm-profile-chain"
import { AssetInterceptRulesEditor } from "@/components/ai/artex/asset-intercept-rules-editor"
import { CategoryPicker } from "@/components/ai/artex/category-picker"
import { TaskTemplateControls } from "@/components/ai/artex/task-template-controls"
import { TaskSourcePicker } from "@/components/ai/artex/task-source-picker"
import { api as artexAPI } from "@/services/artex-ai.service"
import { getErrorMessage } from "@/lib/api-client"
import type { AssetInterceptRuleInput, LLMProfile, TaskCategory } from "@/types/artex-ai.types"
import { useTargets } from "@/hooks/use-targets"
import type { AssetEntity } from "@/types/asset-entity.types"
import type { Target } from "@/types/target.types"
import type { AITask } from "@/types/ai-task.types"

export function AITaskCreateDialog({ asset, open: controlledOpen, onOpenChange, trigger = true }: {
  asset?: AssetEntity; open?: boolean; onOpenChange?: (open: boolean) => void; trigger?: boolean
}) {
  const t = useTranslations("aiTasks")
  const router = useRouter()
  const { create } = useAITaskMutations()
  const queryClient = useQueryClient()
  const [localOpen, setLocalOpen] = React.useState(false)
  const dialogRef = React.useRef<HTMLDivElement | null>(null)
  const open = controlledOpen ?? localOpen
  const setOpen = (value: boolean) => { setLocalOpen(value); onOpenChange?.(value) }
  const [name, setName] = React.useState("")
  const [goal, setGoal] = React.useState("")
  const [description, setDescription] = React.useState("")
  const [remark, setRemark] = React.useState("")
  const [categoryID, setCategoryID] = React.useState<string | undefined>()
  const [categories, setCategories] = React.useState<TaskCategory[]>([])
  const [templateID, setTemplateID] = React.useState<string | null>(null)
  const [sourceTasks, setSourceTasks] = React.useState<AITask[]>([])
  const [workerCount, setWorkerCount] = React.useState("0")
  const [maxTurns, setMaxTurns] = React.useState("0")
  const [timeout, setTimeout] = React.useState("0")
  const [heartbeatMinutes,setHeartbeatMinutes] = React.useState("10")
  const [start, setStart] = React.useState(true)
  const [seedFirstIntent, setSeedFirstIntent] = React.useState(false)
  const [coverageEnabled, setCoverageEnabled] = React.useState(true)
  const [profiles,setProfiles] = React.useState<LLMProfile[]>([])
  const [profileIds,setProfileIds] = React.useState<string[]>([])
  const [interceptRules, setInterceptRules] = React.useState<AssetInterceptRuleInput[]>([])
  const [target, setTarget] = React.useState<Target | null>(null)
  const [targetDraft, setTargetDraft] = React.useState("")
  const [targetSearch, setTargetSearch] = React.useState("")
  const [tokens, setTokens] = React.useState<string[]>([""])
  const [page, setPage] = React.useState(0)
  const targets = useTargets({ pageSize: 20, pageToken: tokens[page] || undefined,
    filter: targetSearch ? `displayName=${JSON.stringify(targetSearch)}` : undefined }, { enabled: open })
  React.useEffect(()=>{if(open)void artexAPI.chatLLMProfiles().then(setProfiles).catch(()=>setProfiles([]))},[open])
  const loadCategories = React.useCallback(() => { void artexAPI.taskCategories().then(setCategories).catch(() => setCategories([])); void queryClient.invalidateQueries({ queryKey:["ai-task-categories"] }) }, [queryClient])
  React.useEffect(() => { if (open) loadCategories() }, [open, loadCategories])
  React.useEffect(() => {
    const timer = window.setTimeout(() => { setTargetSearch(targetDraft.trim()); setTokens([""]); setPage(0) }, 300)
    return () => window.clearTimeout(timer)
  }, [targetDraft])
  const options = [...(target && !targets.data?.targets.some(item => item.id === target.id) ? [target] : []), ...(targets.data?.targets ?? [])]
  const submit = async () => {
    try {
      const task = await create.mutateAsync({ displayName: name, goal, description, remark, categoryId: categoryID, targetId: target?.id,
        assetIds: asset ? [asset.id] : [], sourceTaskIds: sourceTasks.map(item => item.id), llmProfileIds:profileIds, workerCount: Number(workerCount), maxTurns: Number(maxTurns), timeoutSeconds: Number(timeout),planHeartbeatSeconds:Number(heartbeatMinutes)*60, startImmediately: start, seedFirstIntent, coverageEnabled, interceptRules })
      setOpen(false)
      toast.success(t("createdToast"))
      router.push(`/ai/tasks/${task.id}/`)
    } catch (error) { toast.error(t("createError"),{description:getErrorMessage(error)}) }
  }
  const field = (id: string, label: string, control: React.ReactNode) => <div className="flex flex-col gap-1"><Label htmlFor={id}>{label}</Label>{control}</div>
  return <Dialog open={open} onOpenChange={setOpen}>
    {trigger && <Button variant={asset ? "outline" : "default"} size={asset ? "sm" : "default"} onClick={() => setOpen(true)}>{asset ? t("createFromAsset") : t("create")}</Button>}
    <DialogContent ref={dialogRef} className="max-h-[90vh] sm:max-w-[760px]">
      <DialogHeader><DialogTitle>{t("create")}</DialogTitle><DialogDescription>{t("createDescription")}</DialogDescription></DialogHeader>
      <form className="flex max-h-150 flex-col gap-3 overflow-y-auto" onSubmit={event => { event.preventDefault(); void submit() }}>
        <TaskTemplateControls description={description} goal={goal} remark={remark} categoryID={categoryID} interceptRules={interceptRules} workerCount={workerCount} selectedTemplateID={templateID} onSelectedTemplateIDChange={setTemplateID} portalContainer={dialogRef} onApply={template => { setDescription(template.description); setGoal(template.goal); setRemark(template.remark ?? ""); setCategoryID(template.category_id ?? undefined); setInterceptRules(template.intercept_rules ?? []); setWorkerCount(String(template.worker_count ?? 0)) }} />
        {field("task-name", t("name"), <Input id="task-name" value={name} maxLength={200} onChange={event => setName(event.target.value)} />)}
        {field("task-remark", "备注（可选）", <Textarea id="task-remark" maxLength={2000} value={remark} onChange={event => setRemark(event.target.value)} />)}
        <div className="grid gap-1"><Label htmlFor="task-category">任务分类</Label><CategoryPicker categories={categories} value={categoryID} onValueChange={setCategoryID} onCategoryCreated={loadCategories} portalContainer={dialogRef} /></div>
        <TaskSourcePicker value={sourceTasks} onChange={setSourceTasks} enabled={open} />
        {field("task-goal", t("goal"), <Textarea id="task-goal" required value={goal} onChange={event => setGoal(event.target.value)} />)}
        {field("task-description", t("details"), <Textarea id="task-description" value={description} onChange={event => setDescription(event.target.value)} />)}
        {asset && <div className="rounded-md border p-2 text-sm"><span className="text-muted-foreground">{t("selectedAsset")}: </span><span className="break-all">{asset.label}</span></div>}
        <div className="flex flex-col gap-2">
          <Label htmlFor="task-target">{t("target")}</Label>
          <Input value={targetDraft} onChange={event => setTargetDraft(event.target.value)} placeholder={t("targetSearch")} aria-label={t("targetSearch")} />
          <Select value={target ? String(target.id) : "none"} onValueChange={next => setTarget(next === "none" ? null : options.find(item => String(item.id) === next) ?? null)}>
            <SelectTrigger id="task-target"><SelectValue>{target?.name ?? t("allAssets")}</SelectValue></SelectTrigger><SelectContent><SelectItem value="none">{t("allAssets")}</SelectItem>{options.map(item => <SelectItem key={item.id} value={String(item.id)}>{item.name}</SelectItem>)}</SelectContent>
          </Select>
          {targets.error && <p className="text-sm text-destructive">{t("targetError")}</p>}
          {asset && <p className="text-sm text-muted-foreground">{t("selectedAssetHint")}</p>}
          <div className="flex justify-end gap-2"><Button type="button" variant="outline" size="sm" disabled={page === 0 || targets.isFetching} onClick={() => setPage(value => value - 1)}>{t("previous")}</Button>
            <Button type="button" variant="outline" size="sm" disabled={!targets.data?.nextPageToken || targets.isFetching} onClick={() => { const next = targets.data?.nextPageToken; if (next) { setTokens(current => [...current.slice(0, page + 1), next]); setPage(value => value + 1) } }}>{t("next")}</Button></div>
        </div>
        <div className="grid gap-2"><Label htmlFor="task-model-chain">任务模型配置链</Label>
          <TaskLLMProfileChain profiles={profiles} value={profileIds} onValueChange={setProfileIds} inputId="task-model-chain" portalContainer={dialogRef} />
          <p className="text-muted-foreground text-xs">Agent 单独绑定的模型优先于任务模型链。</p>
        </div>
        <div className="grid grid-cols-3 gap-2">
          {field("task-workers", t("workers"), <Input id="task-workers" type="number" min={0} max={50} required value={workerCount} onChange={event => setWorkerCount(event.target.value)} />)}
          {field("task-turns", t("turns"), <Input id="task-turns" type="number" min={0} max={1000} required value={maxTurns} onChange={event => setMaxTurns(event.target.value)} />)}
          {field("task-timeout", t("seconds"), <Input id="task-timeout" type="number" min={0} max={2147483647} required value={timeout} onChange={event => setTimeout(event.target.value)} />)}
        </div>
        <p className="text-muted-foreground text-xs">Worker 数填 0 使用全局配置；最大循环次数和任务超时填 0 表示不限。</p>
        <div className="grid gap-2"><Label>任务级资产拦截 / 允许</Label><AssetInterceptRulesEditor value={interceptRules} onChange={setInterceptRules} /><p className="text-muted-foreground text-xs">规则会随任务一起保存，任务启动时立即生效。</p></div>
        {field("task-heartbeat","规划心跳（分钟）",<Input id="task-heartbeat" type="number" min={10} max={35791394} required value={heartbeatMinutes} onChange={event=>setHeartbeatMinutes(event.target.value)} />)}
        <div className="flex items-center gap-2"><Switch id="task-seed-intent" checked={seedFirstIntent} onCheckedChange={setSeedFirstIntent} /><Label htmlFor="task-seed-intent">目标拆解后优先执行种子意图</Label></div>
        <div className="flex items-center gap-2"><Switch id="task-coverage" checked={coverageEnabled} onCheckedChange={setCoverageEnabled} /><Label htmlFor="task-coverage">启用资产覆盖度</Label></div>
        <div className="flex items-center gap-2"><Switch id="task-start" checked={start} onCheckedChange={setStart} /><Label htmlFor="task-start">{t("startImmediately")}</Label></div>
        <DialogFooter><Button type="submit" disabled={create.isPending}>{t("create")}</Button></DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
}
