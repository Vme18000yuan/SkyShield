"use client"

import * as React from "react"
import Link from "next/link"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { useFofa } from "@/hooks/use-fofa"
import { useOrganizations } from "@/hooks/use-organizations"
import { useTargets } from "@/hooks/use-targets"
import { getErrorMessage, getErrorResponseData } from "@/lib/response-parser"
import type { Organization } from "@/types/organization.types"
import type { Target } from "@/types/target.types"
import type { FofaImportResult } from "@/types/fofa.types"

export function FofaImportDialog({ searchId, rows, open, onOpenChange, onImported }: {
  searchId: string; rows: number[]; open: boolean; onOpenChange: (open: boolean) => void; onImported: (result: FofaImportResult) => void
}) {
  const t = useTranslations("spaceSearch")
  const { sync } = useFofa()
  const [organization, setOrganization] = React.useState<Organization | null>(null)
  const [organizationDraft, setOrganizationDraft] = React.useState("")
  const [organizationSearch, setOrganizationSearch] = React.useState("")
  const [organizationTokens, setOrganizationTokens] = React.useState([""])
  const [organizationPage, setOrganizationPage] = React.useState(0)
  const [target, setTarget] = React.useState<Target | null>(null)
  const [targetDraft, setTargetDraft] = React.useState("")
  const [targetSearch, setTargetSearch] = React.useState("")
  const [targetTokens, setTargetTokens] = React.useState([""])
  const [targetPage, setTargetPage] = React.useState(0)
  const organizations = useOrganizations({ pageSize: 20, pageToken: organizationTokens[organizationPage] || undefined, filter: organizationSearch ? `displayName=${JSON.stringify(organizationSearch)}` : undefined }, { enabled: open })
  const targets = useTargets({ pageSize: 20, pageToken: targetTokens[targetPage] || undefined, filter: targetSearch ? `displayName=${JSON.stringify(targetSearch)}` : undefined }, { enabled: open })
  React.useEffect(() => { const timer = window.setTimeout(() => { setOrganizationSearch(organizationDraft.trim()); setOrganizationTokens([""]); setOrganizationPage(0) }, 300); return () => window.clearTimeout(timer) }, [organizationDraft])
  React.useEffect(() => { const timer = window.setTimeout(() => { setTargetSearch(targetDraft.trim()); setTargetTokens([""]); setTargetPage(0) }, 300); return () => window.clearTimeout(timer) }, [targetDraft])
  const organizationOptions = [...(organization && !organizations.data?.organizations.some(item => item.id === organization.id) ? [organization] : []), ...(organizations.data?.organizations ?? [])]
  const targetOptions = [...(target && !targets.data?.targets.some(item => item.id === target.id) ? [target] : []), ...(targets.data?.targets ?? [])]
  const submit = async () => {
    if (!organization || !rows.length) return
    try {
      const result = await sync.mutateAsync({ searchId, rows, organizationId: organization.id, targetId: target?.id })
      onImported(result); onOpenChange(false)
      if (result.importedRows) toast.success(t("importSuccess", { rows: result.importedRows, assets: result.assetCount }))
      else toast.error(t("importFailed"))
    } catch (error) { toast.error(getErrorMessage(getErrorResponseData(error)) || t("error")) }
  }
  const pagination = (page: number, previous: () => void, next: () => void, hasNext: boolean, pending: boolean) => <div className="flex justify-end gap-2"><Button type="button" variant="outline" size="sm" disabled={!page || pending} onClick={previous}>{t("previous")}</Button><Button type="button" variant="outline" size="sm" disabled={!hasNext || pending} onClick={next}>{t("next")}</Button></div>
  return <Dialog open={open} onOpenChange={value => { if (!sync.isPending) onOpenChange(value) }}><DialogContent>
    <DialogHeader><DialogTitle>{t("syncTitle")}</DialogTitle><DialogDescription>{t("syncDescription", { count: rows.length })}</DialogDescription></DialogHeader>
    <form className="flex max-h-150 flex-col gap-3 overflow-y-auto" onSubmit={event => { event.preventDefault(); void submit() }}>
      <Label htmlFor="fofa-organization">{t("organization")}</Label>
      <Input aria-label={t("searchOrganization")} placeholder={t("searchOrganization")} value={organizationDraft} onChange={event => setOrganizationDraft(event.target.value)} />
      <Select value={organization ? String(organization.id) : "none"} onValueChange={value => setOrganization(organizationOptions.find(item => String(item.id) === value) ?? null)}><SelectTrigger id="fofa-organization"><SelectValue>{organization?.name ?? t("selectOrganization")}</SelectValue></SelectTrigger><SelectContent><SelectItem value="none">{t("selectOrganization")}</SelectItem>{organizationOptions.map(item => <SelectItem key={item.id} value={String(item.id)}>{item.name}</SelectItem>)}</SelectContent></Select>
      {organizations.error && <p className="text-sm text-destructive">{t("organizationError")}</p>}
      {pagination(organizationPage, () => setOrganizationPage(value => value - 1), () => { const next = organizations.data?.nextPageToken; if (next) { setOrganizationTokens(current => [...current.slice(0, organizationPage + 1), next]); setOrganizationPage(value => value + 1) } }, Boolean(organizations.data?.nextPageToken), organizations.isFetching)}
      <Button variant="link" size="sm" render={<Link href="/organizations/" />}>{t("manageOrganizations")}</Button>
      <Label htmlFor="fofa-target">{t("target")}</Label>
      <Input aria-label={t("searchTarget")} placeholder={t("searchTarget")} value={targetDraft} onChange={event => setTargetDraft(event.target.value)} />
      <Select value={target ? String(target.id) : "auto"} onValueChange={value => setTarget(targetOptions.find(item => String(item.id) === value) ?? null)}><SelectTrigger id="fofa-target"><SelectValue>{target?.name ?? t("automaticTarget")}</SelectValue></SelectTrigger><SelectContent><SelectItem value="auto">{t("automaticTarget")}</SelectItem>{targetOptions.map(item => <SelectItem key={item.id} value={String(item.id)}>{item.name}</SelectItem>)}</SelectContent></Select>
      <p className="text-sm text-muted-foreground">{target ? t("existingTargetHint") : t("automaticTargetHint")}</p>
      {targets.error && <p className="text-sm text-destructive">{t("targetError")}</p>}
      {pagination(targetPage, () => setTargetPage(value => value - 1), () => { const next = targets.data?.nextPageToken; if (next) { setTargetTokens(current => [...current.slice(0, targetPage + 1), next]); setTargetPage(value => value + 1) } }, Boolean(targets.data?.nextPageToken), targets.isFetching)}
      <DialogFooter><Button type="submit" disabled={!organization || !rows.length || sync.isPending}>{sync.isPending ? t("syncing") : t("confirmSync")}</Button></DialogFooter>
    </form>
  </DialogContent></Dialog>
}
