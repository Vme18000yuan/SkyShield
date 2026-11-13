"use client"

import * as React from "react"
import { toast } from "sonner"
import { TaskLLMProfileChain } from "@/components/ai/artex/task-llm-profile-chain"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { getErrorMessage } from "@/lib/api-client"
import { api as artexAPI } from "@/services/artex-ai.service"
import { AITaskService } from "@/services/ai-task.service"
import type { AITask } from "@/types/ai-task.types"
import type { LLMProfile } from "@/types/artex-ai.types"

export function AITaskModelChainDialog({task,onSaved}:{task:AITask;onSaved:()=>Promise<void>}){
  const [open,setOpen]=React.useState(false)
  const [loading,setLoading]=React.useState(false)
  const [saving,setSaving]=React.useState(false)
  const [loadError,setLoadError]=React.useState("")
  const [profiles,setProfiles]=React.useState<LLMProfile[]>([])
  const [ids,setIds]=React.useState<string[]>([])
  const [active,setActive]=React.useState("")
  const dialogRef=React.useRef<HTMLDivElement|null>(null)

  const changeOpen=(next:boolean)=>{
    setOpen(next)
    if(!next)return
    setIds(task.llmProfileIds||[]);setActive(task.activeLlmProfileId||"");setLoadError("");setLoading(true)
    void artexAPI.chatLLMProfiles().then(setProfiles).catch(error=>setLoadError(getErrorMessage(error))).finally(()=>setLoading(false))
  }
  const save=async()=>{
    setSaving(true)
    try{await AITaskService.setModels(task.id,ids,active||null);await onSaved();toast.success("任务模型链已保存");setOpen(false)}
    catch(error){toast.error("保存失败",{description:getErrorMessage(error)})}
    finally{setSaving(false)}
  }
  return <Dialog open={open} onOpenChange={changeOpen}>
    <Button variant="outline" size="sm" onClick={()=>changeOpen(true)}>模型配置链</Button>
    <DialogContent ref={dialogRef}>
      <DialogHeader><DialogTitle>任务模型配置链</DialogTitle><DialogDescription>设置当前模型及备用顺序。额度耗尽后，任务会自动尝试后续配置。</DialogDescription></DialogHeader>
      <div className="max-h-[60vh] overflow-y-auto">
        {task.llmFailoverState==="chain_exhausted"&&<p className="mb-3 text-sm text-destructive">当前模型链额度已耗尽，请选择可用配置后保存。</p>}
        {loadError?<p role="alert" className="text-sm text-destructive">模型列表加载失败：{loadError}</p>:<TaskLLMProfileChain profiles={profiles} value={ids} onValueChange={setIds} activeProfileId={active} onActiveProfileChange={setActive} disabled={loading||saving} portalContainer={dialogRef} />}
        <p className="mt-3 text-muted-foreground text-xs">Agent 单独绑定的模型优先于任务模型链；保存后下一次模型调用生效。</p>
      </div>
      <DialogFooter><Button type="button" variant="outline" onClick={()=>setOpen(false)}>取消</Button><Button type="button" disabled={loading||saving||!!loadError} onClick={()=>void save()}>保存配置</Button></DialogFooter>
    </DialogContent>
  </Dialog>
}
