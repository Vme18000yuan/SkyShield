// Adapted from ARTEX, AGPL-3.0-only; see third_party/artex/SKYSHIELD-NOTICE.md.
"use client"

import * as React from "react"
import { CheckIcon, PencilIcon, PlusIcon, ShieldAlertIcon, Trash2Icon, XIcon } from "lucide-react"
import { useTranslations } from "next-intl"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api-client"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"

type Constraint = { id: string; kind: "allow" | "deny"; text: string; origin: string; ts: string }
export function TaskConstraints({ taskId }: { taskId: string }) {
  const t = useTranslations("aiTasks.constraints")
  const cache = useQueryClient()
  const key = ["aiTaskConstraints", taskId]
  const base = `/aiTasks/${encodeURIComponent(taskId)}/constraints`
  const query = useQuery({ queryKey: key, queryFn: async () => (await api.get<{ constraints: Constraint[] }>(base)).data.constraints ?? [] })
  const [kind, setKind] = React.useState<Constraint["kind"]>("deny")
  const [text, setText] = React.useState("")
  const [editId, setEditId] = React.useState<string | null>(null)
  const [editKind, setEditKind] = React.useState<Constraint["kind"]>("deny")
  const [editText, setEditText] = React.useState("")
  const [error, setError] = React.useState("")
  const save = useMutation({
    mutationFn: async (input: { id?: string; kind: Constraint["kind"]; text: string }) => input.id
      ? api.put(`${base}/${encodeURIComponent(input.id)}`, { kind: input.kind, text: input.text.trim() })
      : api.post(base, { kind: input.kind, text: input.text.trim() }),
    onSuccess: async () => { setText(""); setEditId(null); setError(""); await cache.invalidateQueries({ queryKey: key }) },
    onError: () => setError(t("saveFailed")),
  })
  const remove = useMutation({
    mutationFn: (id: string) => api.delete(`${base}/${encodeURIComponent(id)}`),
    onSuccess: async () => { setError(""); await cache.invalidateQueries({ queryKey: key }) },
    onError: () => setError(t("deleteFailed")),
  })
  const busy = save.isPending || remove.isPending
  const selector = (value: Constraint["kind"], onChange: (value: Constraint["kind"]) => void, label: string) => <select aria-label={label} className="h-8 rounded-md border bg-background px-2 text-sm" value={value} disabled={busy} onChange={event => onChange(event.target.value as Constraint["kind"])}><option value="deny">{t("deny")}</option><option value="allow">{t("allow")}</option></select>
  return <Card className="mt-4">
    <CardHeader><CardTitle className="flex flex-wrap items-center gap-2 text-base"><ShieldAlertIcon className="size-4 text-amber-500" />{t("title")} <span className="text-xs font-normal text-muted-foreground">{t("description", { count: query.data?.length ?? 0 })}</span></CardTitle></CardHeader>
    <CardContent className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        {selector(kind, setKind, t("kind"))}
        <Input className="min-w-56 flex-1" aria-label={t("text")} placeholder={t("placeholder")} value={text} onChange={event => setText(event.target.value)} onKeyDown={event => { if (event.key === "Enter" && text.trim() && !busy) save.mutate({ kind, text }) }} disabled={busy} />
        <Button size="sm" variant="outline" disabled={busy || !text.trim()} onClick={() => save.mutate({ kind, text })}><PlusIcon className="size-3.5" />{t("add")}</Button>
      </div>
      {error || query.isError ? <p role="alert" className="text-xs text-destructive">{error || t("loadFailed")}</p> : null}
      {query.data?.length ? <div className="flex flex-col gap-1.5">{query.data.map(item => editId === item.id
        ? <div key={item.id} className="flex flex-wrap items-center gap-2 rounded-md border px-2.5 py-1.5">
            {selector(editKind, setEditKind, t("editKind"))}<Input className="min-w-56 flex-1" aria-label={t("editText")} value={editText} onChange={event => setEditText(event.target.value)} disabled={busy} autoFocus onKeyDown={event => { if (event.key === "Enter" && editText.trim() && !busy) save.mutate({ id: item.id, kind: editKind, text: editText }); if (event.key === "Escape") setEditId(null) }} />
            <Button aria-label={t("save")} size="sm" variant="ghost" disabled={busy || !editText.trim()} onClick={() => save.mutate({ id: item.id, kind: editKind, text: editText })}><CheckIcon className="size-3.5 text-emerald-500" /></Button><Button aria-label={t("cancel")} size="sm" variant="ghost" disabled={busy} onClick={() => setEditId(null)}><XIcon className="size-3.5" /></Button>
          </div>
        : <div key={item.id} className="flex items-center gap-2 rounded-md border px-2.5 py-1.5 text-sm"><span className={item.kind === "allow" ? "rounded bg-emerald-500/15 px-1.5 py-0.5 text-xs text-emerald-600" : "rounded bg-red-500/15 px-1.5 py-0.5 text-xs text-red-600"}>{item.kind === "allow" ? t("allow") : t("deny")}</span><span className="min-w-0 flex-1 break-words">{item.text}</span><Button aria-label={t("edit")} size="sm" variant="ghost" disabled={busy} onClick={() => { setEditId(item.id); setEditKind(item.kind); setEditText(item.text) }}><PencilIcon className="size-3.5" /></Button><Button aria-label={t("delete")} size="sm" variant="ghost" disabled={busy} onClick={() => remove.mutate(item.id)}><Trash2Icon className="size-3.5" /></Button></div>)}</div>
        : !query.isLoading && !query.isError ? <p className="text-sm text-muted-foreground">{t("empty")}</p> : null}
    </CardContent>
  </Card>
}
