"use client"

import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"

export function FofaQueryForm({ query, pageSize, full, disabled = false, pending = false, ready = false, onQuery, onPageSize, onFull, onSubmit }: {
  query: string; pageSize: string; full: boolean; disabled?: boolean; pending?: boolean; ready?: boolean
  onQuery: (query: string) => void; onPageSize: (size: string) => void; onFull: (full: boolean) => void; onSubmit: () => void
}) {
  const t = useTranslations("spaceSearch")
  return <form className="flex flex-col gap-3 rounded-md border bg-card p-4" onSubmit={event => { event.preventDefault(); onSubmit() }}>
    <Label htmlFor="fofa-query">{t("query")}</Label>
    <Textarea id="fofa-query" required maxLength={8192} value={query} onChange={event => onQuery(event.target.value)} placeholder={t("queryPlaceholder")} disabled={disabled} />
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div className="flex flex-wrap items-center gap-3"><Label htmlFor="fofa-page-size">{t("pageSize")}</Label><Select value={pageSize} onValueChange={onPageSize} disabled={disabled}><SelectTrigger id="fofa-page-size" className="w-24"><SelectValue /></SelectTrigger><SelectContent>{[20, 50, 100].map(size => <SelectItem key={size} value={String(size)}>{size}</SelectItem>)}</SelectContent></Select>
        <div className="flex items-center gap-2"><Checkbox id="fofa-full" checked={full} onCheckedChange={onFull} disabled={disabled} /><Label htmlFor="fofa-full">{t("fullHistory")}</Label></div>
      </div>
      <Button type="submit" disabled={!ready || !query.trim() || disabled}>{pending ? t("searching") : t("search")}</Button>
    </div>
    <p className="text-xs text-muted-foreground">{t("quotaHint")} <a className="underline" href="https://fofa.info/api" target="_blank" rel="noreferrer">{t("apiDocs")}</a></p>
  </form>
}
