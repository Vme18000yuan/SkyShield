"use client"

import * as React from "react"
import Link from "next/link"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import type { useCoverageRefs } from "@/hooks/use-asset-coverage"
import type { CoverageNode, CoverageScope } from "@/types/asset-coverage.types"

type Props = {
  selected: CoverageNode
  scopeKind: CoverageScope["kind"]
  coverageEnabled: boolean
  refs: ReturnType<typeof useCoverageRefs>
  sourceNames: Record<string, string>
  otherParents: CoverageNode[]
  relatedChildren: CoverageNode[]
  onSelect: (node: CoverageNode) => void
}

export function CoverageAssetDetailBody({ selected, scopeKind, coverageEnabled, refs, sourceNames, otherParents, relatedChildren, onSelect }: Props) {
  const t = useTranslations("aiTasks.coverageGraph")
  const [parentLimit, setParentLimit] = React.useState(8)
  const [relatedLimit, setRelatedLimit] = React.useState(8)
  const sections = refs.data ? ([[t("intents"), refs.data.intents], [t("facts"), refs.data.facts], [t("findings"), refs.data.findings]] as const) : []
  return <ScrollArea className="min-h-0 flex-1"><div className="space-y-4 p-4 text-sm">
    <section><h4 className="mb-2 text-xs font-medium text-muted-foreground">{t("attributes")}</h4><dl className="grid grid-cols-[6rem_1fr] gap-2"><dt className="text-muted-foreground">{t("scope")}</dt><dd>{selected.in_scope ? t(scopeKind === "task" ? "inScope" : "treeRootScope") : t("connector")}</dd>{selected.in_scope && coverageEnabled && <><dt className="text-muted-foreground">{t("factStatus")}</dt><dd>{selected.tested ? t("hasFact") : t("noFact")}</dd></>}{([[t("domain"), selected.domain], [t("rootDomain"), selected.root_domain], [t("ip"), selected.ip], [t("port"), selected.port], [t("url"), selected.url], [t("pageTitle"), selected.page_title], [t("statusCode"), selected.status_code], [t("serviceType"), selected.service_type], [t("app"), selected.app_name], [t("assetId"), selected.asset_id]] as const).map(([label, value]) => value != null && value !== "" ? <React.Fragment key={label}><dt className="text-muted-foreground">{label}</dt><dd className="break-all">{value}</dd></React.Fragment> : null)}</dl></section>
    {otherParents.length > 0 && <section className="border-t pt-3"><h4 className="mb-2 text-xs font-medium text-muted-foreground">{t("treeOtherParents", { count: otherParents.length })}</h4><div className="flex flex-col items-start gap-1">{otherParents.slice(0, parentLimit).map(node => <Button key={node.key} variant="link" size="content" className="h-auto max-w-full justify-start whitespace-normal break-all p-0 text-left" onClick={() => onSelect(node)}>{node.label}</Button>)}{otherParents.length > parentLimit && <Button variant="ghost" size="xs" onClick={() => setParentLimit(limit => limit + 40)}>{t("treeShowMore", { count: Math.min(40, otherParents.length - parentLimit) })}</Button>}</div></section>}
    {relatedChildren.length > 0 && <section className="border-t pt-3"><h4 className="mb-2 text-xs font-medium text-muted-foreground">{t("treeRelatedAssets", { count: relatedChildren.length })}</h4><div className="flex flex-col items-start gap-2">{relatedChildren.slice(0, relatedLimit).map(node => <Button key={node.key} variant="link" size="content" className="h-auto max-w-full justify-start whitespace-normal break-all p-0 text-left" onClick={() => onSelect(node)}>{node.label} <span className="text-xs text-muted-foreground">{t(`kinds.${node.kind}`)}</span></Button>)}{relatedChildren.length > relatedLimit && <Button variant="ghost" size="xs" onClick={() => setRelatedLimit(limit => limit + 40)}>{t("treeShowMore", { count: Math.min(40, relatedChildren.length - relatedLimit) })}</Button>}</div></section>}
    {!selected.asset_id && <p className="text-xs text-muted-foreground">{t("treeNoAssetId")}</p>}
    {selected.asset_id && refs.isFetching && <p className="text-xs text-muted-foreground">{t("refsLoading")}</p>}
    {refs.isError && <p role="alert" className="text-destructive">{t("refsError")}</p>}
    {selected.asset_id && !refs.isError && !refs.isFetching && refs.data && sections.every(([, items]) => !items.length) && <p className="text-xs text-muted-foreground">{t("refsEmpty")}</p>}
    {!refs.isError && !refs.isFetching && sections.map(([label, items]) => items.length ? <section className="border-t pt-3" key={label}><strong>{label}（{items.length}）</strong><ul className="mt-1 space-y-1">{items.map(item => <li key={`${item.kind}-${item.id}`} className="break-words rounded bg-muted/50 px-2 py-1.5">{item.summary || item.kind} <span className="text-xs text-muted-foreground">{item.state}{item.task_name && item.source_task_id ? <Link className="ml-2 underline" href={`/ai/tasks/${encodeURIComponent(item.source_task_id)}/`}>{item.task_name}</Link> : null}{item.inherited ? ` · ${t("inherited", { name: sourceNames[item.source_task_id ?? ""] ?? t("sourceTask") })}` : ""}</span></li>)}</ul></section> : null)}
    {refs.data?.truncated && <p className="text-xs text-muted-foreground">{t("refsTruncated")}</p>}
    <details className="border-t pt-3"><summary className="cursor-pointer text-xs font-medium text-muted-foreground">{t("originalData")}</summary><pre className="mt-2 max-w-full whitespace-pre-wrap break-all rounded-md border bg-muted/50 p-3 text-xs">{JSON.stringify(selected, null, 2)}</pre></details>
  </div></ScrollArea>
}
