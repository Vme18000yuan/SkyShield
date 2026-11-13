"use client"

import * as React from "react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { useRegisterAssets } from "@/hooks/use-asset-entities"
import { useTargets } from "@/hooks/use-targets"
import type { AssetRegistrationType, AssetRegistrationInput } from "@/types/asset-entity.types"
import type { Target } from "@/types/target.types"

const kinds: AssetRegistrationType[] = ["root_domain", "subdomain", "ip", "app", "web", "service", "endpoint"]
const lines = (value: string) => value.split(/\r?\n/).map(item => item.trim()).filter(Boolean)

export function AssetRegistrationDialog({ initialType, organizationId }: { initialType?: AssetRegistrationType; organizationId?: number }) {
  const t = useTranslations("assetInventory")
  const [open, setOpen] = React.useState(false)
  const [type, setType] = React.useState<AssetRegistrationType>(initialType ?? "web")
  const [value, setValue] = React.useState("")
  const [method, setMethod] = React.useState("")
  const [recordType, setRecordType] = React.useState("")
  const [recordValues, setRecordValues] = React.useState("")
  const [bundle, setBundle] = React.useState("")
  const [serviceName, setServiceName] = React.useState("")
  const [serviceDomain, setServiceDomain] = React.useState("")
  const [serviceIP, setServiceIP] = React.useState("")
  const [port, setPort] = React.useState("")
  const [protocol, setProtocol] = React.useState("unknown")
  const [title, setTitle] = React.useState("")
  const [tech, setTech] = React.useState("")
  const [boundDomains, setBoundDomains] = React.useState("")
  const [target, setTarget] = React.useState<Target | null>(null)
  const [targetDraft, setTargetDraft] = React.useState("")
  const [targetSearch, setTargetSearch] = React.useState("")
  const [targetTokens, setTargetTokens] = React.useState<string[]>([""])
  const [targetPage, setTargetPage] = React.useState(0)
  const lastRequest = React.useRef<{ body: string; id: string } | null>(null)
  const register = useRegisterAssets()
  const targets = useTargets({ pageSize: 20, pageToken: targetTokens[targetPage] || undefined,
    filter: targetSearch ? `displayName=${JSON.stringify(targetSearch)}` : undefined }, { enabled: open && type !== "app" })

  React.useEffect(() => {
    const timer = window.setTimeout(() => { setTargetSearch(targetDraft.trim()); setTargetTokens([""]); setTargetPage(0) }, 300)
    return () => window.clearTimeout(timer)
  }, [targetDraft])

  const targetOptions = [...(target && !targets.data?.targets.some(item => item.id === target.id) ? [target] : []), ...(targets.data?.targets ?? [])]
  const valueLabel = type === "root_domain" || type === "subdomain" ? t("domain") : type === "ip" ? t("address") : type === "app" ? t("appName") : t("url")
  const web = type === "endpoint" || type === "web"

  const submit = async () => {
    const asset: AssetRegistrationInput = { type: type === "web" ? "service" : type, ...(target && type !== "app" ? { targetId: target.id } : {}), ...(organizationId ? { organizationId } : {}) }
    if (type === "root_domain" || type === "subdomain") asset.domain = value
    if (type === "subdomain") { asset.recordType = recordType || undefined; asset.recordValue = lines(recordValues) }
    if (type === "ip") { asset.ip = value; asset.boundDomains = lines(boundDomains) }
    if (type === "app") { asset.appName = value; asset.bundleId = bundle || undefined; asset.boundDomains = lines(boundDomains) }
    if (web) { asset.url = value; asset.pageTitle = title || undefined; asset.technologies = tech.split(",").map(item => item.trim()).filter(Boolean) }
    if (type === "endpoint") asset.method = method || undefined
    if (type === "service") {
      asset.domain = serviceDomain || undefined; asset.ip = serviceIP || undefined; asset.port = Number(port)
      asset.proto = protocol; asset.serviceName = serviceName
    }
    const body = JSON.stringify(asset)
    if (lastRequest.current?.body !== body) lastRequest.current = { body, id: crypto.randomUUID() }
    try {
      const result = await register.mutateAsync({ assets: [asset], requestId: lastRequest.current!.id })
      if (result.errors.length) { toast.error(result.errors[0].error); return }
      setOpen(false); setValue(""); lastRequest.current = null
      toast.success(t("saved"))
    } catch { toast.error(t("saveError")) }
  }

  const field = (id: string, label: string, contents: React.ReactNode) => <div className="flex flex-col gap-1"><Label htmlFor={id}>{label}</Label>{contents}</div>

  return <Dialog open={open} onOpenChange={setOpen}>
    <Button onClick={() => { setType(initialType ?? "web"); setOpen(true) }}>{t("add")}</Button>
    <DialogContent>
      <DialogHeader><DialogTitle>{t("add")}</DialogTitle><DialogDescription>{t("description")}</DialogDescription></DialogHeader>
      <form className="flex max-h-150 flex-col gap-3 overflow-auto" onSubmit={event => { event.preventDefault(); void submit() }}>
        {field("asset-type", t("type"), <Select value={type} onValueChange={(next: AssetRegistrationType) => { setType(next); if (next === "app") setTarget(null) }}>
          <SelectTrigger id="asset-type"><SelectValue /></SelectTrigger><SelectContent>{kinds.map(kind => <SelectItem key={kind} value={kind}>{t(kind)}</SelectItem>)}</SelectContent>
        </Select>)}
        {type !== "service" && field("asset-value", valueLabel, <Input id="asset-value" required value={value} onChange={event => setValue(event.target.value)} />)}
        {type === "endpoint" && field("asset-method", t("method"), <Input id="asset-method" value={method} onChange={event => setMethod(event.target.value)} placeholder={t("unknown")} />)}
        {type === "subdomain" && <>
          {field("asset-record-type", t("recordType"), <Input id="asset-record-type" value={recordType} onChange={event => setRecordType(event.target.value)} />)}
          {field("asset-record-values", t("recordValue"), <Textarea id="asset-record-values" value={recordValues} onChange={event => setRecordValues(event.target.value)} />)}
        </>}
        {type === "app" && field("asset-bundle", t("bundleId"), <Input id="asset-bundle" value={bundle} onChange={event => setBundle(event.target.value)} />)}
        {(type === "ip" || type === "app") && field("asset-bound-domains", t("boundDomains"), <Textarea id="asset-bound-domains" value={boundDomains} onChange={event => setBoundDomains(event.target.value)} />)}
        {type === "service" && <>
          {field("asset-service-domain", t("domain"), <Input id="asset-service-domain" value={serviceDomain} onChange={event => setServiceDomain(event.target.value)} />)}
          {field("asset-service-ip", t("address"), <Input id="asset-service-ip" value={serviceIP} onChange={event => setServiceIP(event.target.value)} />)}
          {field("asset-service-name", t("serviceName"), <Input id="asset-service-name" required value={serviceName} onChange={event => setServiceName(event.target.value)} />)}
          {field("asset-service-port", t("port"), <Input id="asset-service-port" type="number" min={1} max={65535} required value={port} onChange={event => setPort(event.target.value)} />)}
          {field("asset-service-proto", t("protocol"), <Select value={protocol} onValueChange={setProtocol}><SelectTrigger id="asset-service-proto"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="unknown">{t("unknown")}</SelectItem><SelectItem value="tcp">TCP</SelectItem><SelectItem value="udp">UDP</SelectItem></SelectContent></Select>)}
        </>}
        {web && <>
          {field("asset-title", t("pageTitle"), <Input id="asset-title" value={title} onChange={event => setTitle(event.target.value)} />)}
          {field("asset-tech", t("tech"), <Input id="asset-tech" value={tech} onChange={event => setTech(event.target.value)} />)}
        </>}
        {type !== "app" && <div className="flex flex-col gap-2">
          <Label htmlFor="asset-target">{t("target")}</Label>
          <Input value={targetDraft} onChange={event => setTargetDraft(event.target.value)} placeholder={t("targetSearch")} aria-label={t("targetSearch")} />
          <Select value={target ? String(target.id) : "none"} onValueChange={next => setTarget(next === "none" ? null : targetOptions.find(item => String(item.id) === next) ?? null)}>
            <SelectTrigger id="asset-target"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="none">{t("independent")}</SelectItem>{targetOptions.map(item => <SelectItem key={item.id} value={String(item.id)}>{item.name}</SelectItem>)}</SelectContent>
          </Select>
          <div className="flex justify-end gap-2"><Button type="button" variant="outline" size="sm" disabled={targetPage === 0 || targets.isFetching} onClick={() => setTargetPage(value => value - 1)}>{t("previous")}</Button><Button type="button" variant="outline" size="sm" disabled={!targets.data?.nextPageToken || targets.isFetching} onClick={() => {
            const next = targets.data?.nextPageToken
            if (next) { setTargetTokens(existing => [...existing.slice(0, targetPage + 1), next]); setTargetPage(value => value + 1) }
          }}>{t("next")}</Button></div>
        </div>}
        <DialogFooter><Button type="submit" disabled={register.isPending}>{t("save")}</Button></DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
}
