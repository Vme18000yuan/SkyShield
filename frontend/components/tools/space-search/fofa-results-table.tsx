"use client"

import { useTranslations } from "next-intl"
import { Checkbox } from "@/components/ui/checkbox"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { FofaRecord } from "@/types/fofa.types"

export function FofaResultsTable({ rows, selected, onSelect, disabled = false }: { rows: FofaRecord[]; selected: Set<number>; onSelect: (selection: Set<number>) => void; disabled?: boolean }) {
  const t = useTranslations("spaceSearch")
  const selectable = rows.filter(row => row.importable)
  const all = Boolean(selectable.length && selectable.every(row => selected.has(row.rowId)))
  const some = selectable.some(row => selected.has(row.rowId))
  return <div className="overflow-x-auto rounded-md border">
    <Table className="min-w-240"><TableHeader><TableRow>
      <TableHead><Checkbox aria-label={t("selectPage")} checked={all} indeterminate={some && !all} disabled={disabled || !selectable.length} onCheckedChange={checked => onSelect(checked ? new Set(selectable.map(row => row.rowId)) : new Set())} /></TableHead>
      <TableHead>{t("host")}</TableHead><TableHead>IP</TableHead><TableHead>{t("port")}</TableHead><TableHead>{t("protocol")}</TableHead><TableHead>{t("websiteTitle")}</TableHead><TableHead>{t("server")}</TableHead><TableHead>{t("location")}</TableHead>
    </TableRow></TableHeader><TableBody>
      {rows.map(row => <TableRow key={row.rowId}>
        <TableCell><Checkbox aria-label={t("selectRow", { number: row.rowId + 1 })} checked={selected.has(row.rowId)} disabled={disabled || !row.importable} onCheckedChange={checked => { const next = new Set(selected); if (checked) next.add(row.rowId); else next.delete(row.rowId); onSelect(next) }} /></TableCell>
        <TableCell className="max-w-md break-all"><div>{row.url || row.host || row.ip}</div>{!row.importable && <p className="mt-1 text-xs text-destructive">{row.importError}</p>}</TableCell>
        <TableCell className="whitespace-nowrap font-mono">{row.ip}</TableCell><TableCell>{row.port || "—"}</TableCell><TableCell>{row.protocol || "—"}</TableCell>
        <TableCell className="max-w-sm truncate" title={row.title}>{row.title || "—"}</TableCell><TableCell className="max-w-xs truncate" title={row.server}>{row.server || "—"}</TableCell><TableCell>{[row.countryName || row.country, row.region, row.city].filter(Boolean).join(" / ") || "—"}</TableCell>
      </TableRow>)}
      {!rows.length && <TableRow><TableCell colSpan={8} className="text-muted-foreground">{t("empty")}</TableCell></TableRow>}
    </TableBody></Table>
  </div>
}
