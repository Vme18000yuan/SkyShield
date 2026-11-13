"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { useOrganization, useOrganizations } from "@/hooks/use-organizations"

export function AssetOrganizationFilter({ value, onChange }: { value?: number; onChange: (id?: number) => void }) {
  const t = useTranslations("assetInventory")
  const [open, setOpen] = React.useState(false)
  const [draft, setDraft] = React.useState("")
  const [search, setSearch] = React.useState("")
  const [tokens, setTokens] = React.useState<string[]>([""])
  const [page, setPage] = React.useState(0)
  React.useEffect(() => { const timer = window.setTimeout(() => { setSearch(draft.trim()); setTokens([""]); setPage(0) }, 250); return () => window.clearTimeout(timer) }, [draft])
  const organizations = useOrganizations({ pageSize: 20, pageToken: tokens[page] || undefined, filter: search ? `displayName=${JSON.stringify(search)}` : undefined }, { enabled: open })
  const current = useOrganization(value ?? 0)
  return <Popover open={open} onOpenChange={setOpen}>
    <PopoverTrigger render={<Button variant="outline" size="sm" aria-label={t("filterOrganization")} />}>{value ? current.data?.name ?? t("organizationId", { id: value }) : t("allOrganizations")}</PopoverTrigger>
    <PopoverContent align="start" className="w-80 max-w-[calc(100vw-2rem)] space-y-3">
      <Input value={draft} onChange={event => setDraft(event.target.value)} placeholder={t("searchOrganizations")} aria-label={t("searchOrganizations")} />
      <div className="max-h-60 space-y-1 overflow-y-auto">
        <button type="button" className="w-full rounded px-2 py-1.5 text-left text-sm hover:bg-muted" onClick={() => { onChange(undefined); setOpen(false) }}>{t("allOrganizations")}</button>
        {organizations.data?.organizations.map(org => <button type="button" key={org.id} className={`w-full rounded px-2 py-1.5 text-left text-sm hover:bg-muted ${value === org.id ? "bg-muted font-medium" : ""}`} onClick={() => { onChange(org.id); setOpen(false) }}>{org.name}</button>)}
        {organizations.error && <p className="px-2 text-sm text-destructive">{t("organizationLoadError")}</p>}
        {!organizations.isPending && !organizations.error && !organizations.data?.organizations.length && <p className="px-2 text-sm text-muted-foreground">{t("noOrganizations")}</p>}
      </div>
      <div className="flex items-center justify-between gap-2"><span className="text-xs text-muted-foreground">{t("organizationTotal", { count: organizations.data?.total ?? 0 })}</span><div className="flex gap-1">
        <Button type="button" variant="outline" size="sm" disabled={page === 0 || organizations.isFetching} onClick={() => setPage(value => value-1)}>{t("previous")}</Button>
        <Button type="button" variant="outline" size="sm" disabled={!organizations.data?.nextPageToken || organizations.isFetching} onClick={() => { const next = organizations.data?.nextPageToken; if (next) { setTokens(current => [...current.slice(0,page+1),next]); setPage(value => value+1) } }}>{t("next")}</Button>
      </div></div>
    </PopoverContent>
  </Popover>
}
