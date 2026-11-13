"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { useAISchedules } from "@/hooks/use-ai-schedules"
import { useAITasks } from "@/hooks/use-ai-tasks"
import type { AISchedule, AIScheduleInput } from "@/types/ai-schedule.types"

const empty: AIScheduleInput = { displayName: "", remark: "", enabled: true, timezoneMode: "beijing", scheduleType: "weekly", runDate: "", endDate: "", weekdays: [1, 2, 3, 4, 5], startTime: "09:00", endTime: "18:00", startImmediately: false, taskIds: [] }

export function AIScheduleDialog({ open, onOpenChange, schedule }: { open: boolean; onOpenChange: (open: boolean) => void; schedule: AISchedule | null }) {
  const t = useTranslations("aiSchedules")
  const { save } = useAISchedules()
  const [input, setInput] = React.useState<AIScheduleInput>(empty)
  const [cursors, setCursors] = React.useState<string[]>([""])
  const [page, setPage] = React.useState(0)
  const tasks = useAITasks(cursors[page], open)
  React.useEffect(() => {
    if (!open) return
    setInput(schedule ? { displayName: schedule.displayName, remark: schedule.remark, enabled: schedule.enabled, timezoneMode: schedule.timezoneMode,
      scheduleType: schedule.scheduleType, runDate: schedule.runDate, endDate: schedule.endDate, weekdays: schedule.weekdays,
      startTime: schedule.startTime.slice(0, 5), endTime: schedule.endTime.slice(0, 5), startImmediately: schedule.startImmediately, taskIds: schedule.taskIds } : { ...empty })
    setCursors([""]); setPage(0)
  }, [open, schedule])
  const update = <K extends keyof AIScheduleInput>(key: K, value: AIScheduleInput[K]) => setInput(current => ({ ...current, [key]: value }))
  const submit = async () => {
    if (input.scheduleType === "weekly" && !input.weekdays.length) { toast.error(t("weekdaysRequired")); return }
    try { await save.mutateAsync({ id: schedule?.id ?? null, input }); onOpenChange(false); toast.success(t("saved")) }
    catch { toast.error(t("saveError")) }
  }
  const field = (id: string, label: string, control: React.ReactNode) => <div className="flex flex-col gap-1"><Label htmlFor={id}>{label}</Label>{control}</div>
  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent>
    <DialogHeader><DialogTitle>{schedule ? t("edit") : t("create")}</DialogTitle><DialogDescription>{t("editDescription")}</DialogDescription></DialogHeader>
    <form className="flex max-h-150 flex-col gap-3 overflow-y-auto" onSubmit={event => { event.preventDefault(); void submit() }}>
      {field("schedule-name", t("name"), <Input id="schedule-name" required maxLength={200} value={input.displayName} onChange={event => update("displayName", event.target.value)} />)}
      {field("schedule-remark", t("remark"), <Textarea id="schedule-remark" value={input.remark} onChange={event => update("remark", event.target.value)} />)}
      <div className="grid grid-cols-2 gap-2">
        {field("schedule-type", t("rule"), <Select value={input.scheduleType} onValueChange={(value: "weekly" | "once") => update("scheduleType", value)}><SelectTrigger id="schedule-type"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="weekly">{t("weekly")}</SelectItem><SelectItem value="once">{t("once")}</SelectItem></SelectContent></Select>)}
        {field("schedule-zone", t("timezone"), <Select value={input.timezoneMode} onValueChange={(value: "beijing" | "system") => update("timezoneMode", value)}><SelectTrigger id="schedule-zone"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="beijing">{t("beijing")}</SelectItem><SelectItem value="system">{t("system")}</SelectItem></SelectContent></Select>)}
      </div>
      {input.scheduleType === "weekly" ? <fieldset className="flex flex-wrap gap-3"><legend className="mb-1 text-sm font-medium">{t("weekdays")}</legend>
        {[1, 2, 3, 4, 5, 6, 7].map(day => <label key={day} className="flex items-center gap-1 text-sm"><Checkbox checked={input.weekdays.includes(day)} onCheckedChange={checked => update("weekdays", checked ? [...input.weekdays, day].sort((a, b) => a - b) : input.weekdays.filter(value => value !== day))} />{t(`day${day}`)}</label>)}
      </fieldset> : <div className="grid grid-cols-2 gap-2">
        {field("schedule-date", t("runDate"), <Input id="schedule-date" type="date" required value={input.runDate} onChange={event => update("runDate", event.target.value)} />)}
        {field("schedule-end-date", t("endDate"), <Input id="schedule-end-date" type="date" min={input.runDate || undefined} value={input.endDate} onChange={event => update("endDate", event.target.value)} />)}
      </div>}
      <div className="grid grid-cols-2 gap-2">
        {field("schedule-start", t("startTime"), <Input id="schedule-start" required pattern="([01][0-9]|2[0-3]):[0-5][0-9]" placeholder="09:00" value={input.startTime} onChange={event => update("startTime", event.target.value)} />)}
        {field("schedule-end", t("endTime"), <Input id="schedule-end" pattern="(([01][0-9]|2[0-3]):[0-5][0-9]|24:00)" placeholder="18:00" value={input.endTime} onChange={event => update("endTime", event.target.value)} />)}
      </div>
      <p className="text-sm text-muted-foreground">{t("windowHint")}</p>
      <fieldset className="rounded-md border p-2"><legend className="px-1 text-sm font-medium">{t("boundTasks", { count: input.taskIds.length })}</legend>
        <div className="flex max-h-40 flex-col gap-2 overflow-auto">{tasks.data?.tasks.map(task => <label key={task.id} className="flex items-center gap-2 text-sm"><Checkbox checked={input.taskIds.includes(task.id)} onCheckedChange={checked => update("taskIds", checked ? [...input.taskIds, task.id] : input.taskIds.filter(id => id !== task.id))} />{task.displayName} · #{task.id}</label>)}
          {tasks.error && <p className="text-destructive">{t("taskLoadError")}</p>}{tasks.data && !tasks.data.tasks.length && <p className="text-muted-foreground">{t("noTasks")}</p>}</div>
        <div className="mt-2 flex justify-end gap-2"><Button type="button" size="sm" variant="outline" disabled={page === 0} onClick={() => setPage(value => value - 1)}>{t("previous")}</Button>
          <Button type="button" size="sm" variant="outline" disabled={!tasks.data?.nextBefore} onClick={() => { const next = tasks.data?.nextBefore; if (next) { setCursors(current => [...current.slice(0, page + 1), next]); setPage(value => value + 1) } }}>{t("next")}</Button></div>
      </fieldset>
      <div className="flex items-center gap-2"><Switch id="schedule-enabled" checked={input.enabled} onCheckedChange={value => update("enabled", value)} /><Label htmlFor="schedule-enabled">{t("enabled")}</Label></div>
      <div className="flex items-center gap-2"><Switch id="schedule-immediate" checked={input.startImmediately} onCheckedChange={value => update("startImmediately", value)} /><Label htmlFor="schedule-immediate">{t("startImmediately")}</Label></div>
      <DialogFooter><Button type="submit" disabled={save.isPending}>{t("save")}</Button></DialogFooter>
    </form>
  </DialogContent></Dialog>
}
