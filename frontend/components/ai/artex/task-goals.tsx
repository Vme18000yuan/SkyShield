// Adapted from ARTEX, AGPL-3.0-only; see third_party/artex/SKYSHIELD-NOTICE.md.
"use client"

import * as React from "react"
import { CheckIcon, ListChecksIcon, PencilIcon, PlusIcon, Trash2Icon, XIcon } from "lucide-react"
import { useTranslations } from "next-intl"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api-client"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"

type Goal = { id: string; text: string; vulnclass?: string; state: "open" | "met" | "abandoned"; origin?: string; ts: string }
export function TaskGoals({ taskId }: { taskId: string }) {
  const t = useTranslations("aiTasks.goals")
  const cache = useQueryClient(), key = ["aiTaskGoals", taskId]
  const base = `/aiTasks/${encodeURIComponent(taskId)}/goals`
  const query = useQuery({ queryKey: key, queryFn: async () => (await api.get<{ goals: Goal[] }>(base)).data.goals ?? [], refetchInterval: 5000 })
  const [text, setText] = React.useState(""), [vulnClass, setVulnClass] = React.useState("")
  const [editId, setEditId] = React.useState<string | null>(null), [editText, setEditText] = React.useState(""), [editVuln, setEditVuln] = React.useState("")
  const [error, setError] = React.useState("")
  const refresh = async () => { await Promise.all([cache.invalidateQueries({ queryKey: key }),cache.invalidateQueries({ queryKey: ["ai-task"] }),cache.invalidateQueries({ queryKey: ["ai-task-graph"] })]) }
  const save = useMutation({ mutationFn: (input: { id?: string; text: string; vulnclass: string }) => input.id ? api.put(`${base}/${encodeURIComponent(input.id)}`, { text: input.text.trim(), vulnclass: input.vulnclass.trim() }) : api.post(base, { text: input.text.trim(), vulnclass: input.vulnclass.trim() }), onSuccess: async () => { setText(""); setVulnClass(""); setEditId(null); setError(""); await refresh() }, onError: () => setError(t("saveFailed")) })
  const remove = useMutation({ mutationFn: (id: string) => api.delete(`${base}/${encodeURIComponent(id)}`), onSuccess: async () => { setError(""); await refresh() }, onError: () => setError(t("deleteFailed")) })
  const busy = save.isPending || remove.isPending
  return <Card className="mt-4">
    <CardHeader><CardTitle className="flex flex-wrap items-center gap-2 text-base"><ListChecksIcon className="size-4 text-primary" />{t("title")} <span className="text-xs font-normal text-muted-foreground">{t("description", { count: query.data?.length ?? 0 })}</span></CardTitle></CardHeader>
    <CardContent className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <Input className="min-w-56 flex-1" aria-label={t("text")} placeholder={t("placeholder")} value={text} onChange={event => setText(event.target.value)} disabled={busy} onKeyDown={event => { if (event.key === "Enter" && text.trim() && !busy) save.mutate({ text, vulnclass: vulnClass }) }} />
        <Input className="w-32" aria-label={t("vulnClass")} placeholder={t("vulnClass")} value={vulnClass} onChange={event => setVulnClass(event.target.value)} disabled={busy} onKeyDown={event => { if (event.key === "Enter" && text.trim() && !busy) save.mutate({ text, vulnclass: vulnClass }) }} />
        <Button size="sm" variant="outline" disabled={busy || !text.trim()} onClick={() => save.mutate({ text, vulnclass: vulnClass })}><PlusIcon className="size-3.5" />{t("add")}</Button>
      </div>
      {error || query.isError ? <p role="alert" className="text-xs text-destructive">{error || t("loadFailed")}</p> : null}
      {query.data?.length ? <div className="flex flex-col gap-1.5">{query.data.map(goal => editId === goal.id
        ? <div key={goal.id} className="flex flex-wrap items-center gap-2 rounded-md border px-2.5 py-1.5"><Input className="min-w-56 flex-1" aria-label={t("editText")} value={editText} onChange={event => setEditText(event.target.value)} disabled={busy} autoFocus onKeyDown={event => { if (event.key === "Enter" && editText.trim() && !busy) save.mutate({ id: goal.id, text: editText, vulnclass: editVuln }); if (event.key === "Escape") setEditId(null) }} /><Input className="w-32" aria-label={t("editVulnClass")} placeholder={t("vulnClass")} value={editVuln} onChange={event => setEditVuln(event.target.value)} disabled={busy} /><Button aria-label={t("save")} size="sm" variant="ghost" disabled={busy || !editText.trim()} onClick={() => save.mutate({ id: goal.id, text: editText, vulnclass: editVuln })}><CheckIcon className="size-3.5 text-emerald-500" /></Button><Button aria-label={t("cancel")} size="sm" variant="ghost" disabled={busy} onClick={() => setEditId(null)}><XIcon className="size-3.5" /></Button></div>
        : <div key={goal.id} className="flex items-center gap-2 rounded-md border px-2.5 py-1.5 text-sm"><Badge variant={goal.state === "met" ? "success" : "outline"}>{t(`state.${goal.state}`)}</Badge><span className="min-w-0 flex-1 break-words">{goal.text}</span>{goal.vulnclass ? <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">{goal.vulnclass}</span> : null}<Button aria-label={t("edit")} size="sm" variant="ghost" disabled={busy} onClick={() => { setEditId(goal.id); setEditText(goal.text); setEditVuln(goal.vulnclass || "") }}><PencilIcon className="size-3.5" /></Button><Button aria-label={t("delete")} size="sm" variant="ghost" disabled={busy} onClick={() => remove.mutate(goal.id)}><Trash2Icon className="size-3.5 text-red-500" /></Button></div>)}</div>
        : !query.isLoading && !query.isError ? <p className="text-sm text-muted-foreground">{t("empty")}</p> : null}
    </CardContent>
  </Card>
}
