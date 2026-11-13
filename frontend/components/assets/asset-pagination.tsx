"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

export function AssetPagination({ page, total, pageSize, onPageChange }: { page: number; total: number; pageSize: number; onPageChange: (page: number) => void }) {
  const t = useTranslations("assetInventory")
  const pages = Math.max(1, Math.ceil(total / pageSize))
  const [draft, setDraft] = React.useState(String(page))
  React.useEffect(() => setDraft(String(page)), [page])
  return <div className="flex flex-wrap items-center justify-between gap-3">
    <span className="text-sm text-muted-foreground">{t("paginationSummary", { total, page, pages })}</span>
    <div className="flex flex-wrap items-center gap-2">
      <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => onPageChange(page-1)}>{t("previous")}</Button>
      <Button variant="outline" size="sm" disabled={page >= pages} onClick={() => onPageChange(page+1)}>{t("next")}</Button>
      <form className="flex items-center gap-1" onSubmit={event => { event.preventDefault(); const next = Number(draft); if (Number.isInteger(next) && next >= 1 && next <= pages) onPageChange(next) }}>
        <Input type="number" min={1} max={pages} required value={draft} onChange={event => setDraft(event.target.value)} aria-label={t("jumpPage")} className="h-8 w-20" />
        <Button type="submit" variant="outline" size="sm">{t("jump")}</Button>
      </form>
    </div>
  </div>
}
