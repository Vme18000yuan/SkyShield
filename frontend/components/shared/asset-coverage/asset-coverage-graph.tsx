// Adapted from ARTEX's task coverage graph, AGPL-3.0-only; see third_party/artex/SKYSHIELD-NOTICE.md.
"use client"

import * as React from "react"
import { ListTree, Network } from "lucide-react"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { useAuth } from "@/hooks/use-auth"
import { useCoverageGraph, useCoverageRefs } from "@/hooks/use-asset-coverage"
import type { CoverageNode, CoverageScope } from "@/types/asset-coverage.types"
import { CoverageAssetDetailBody } from "./coverage-asset-detail"
import { CoverageTopology } from "./coverage-topology"
import { CoverageTree } from "./coverage-tree"
import { buildCoverageTree } from "./coverage-tree-model"

type CoverageGraphProps = { scope: CoverageScope; coverageEnabled?: boolean; sourceNames?: Record<string, string> }

export function AssetCoverageGraph(props: CoverageGraphProps) {
  const user = useAuth().data?.user?.id
  if (!user) return null
  return <AccountCoverageGraph key={`${user}:${JSON.stringify(props.scope)}`} {...props} />
}

function AccountCoverageGraph({ scope, coverageEnabled = true, sourceNames = {} }: CoverageGraphProps) {
  const t = useTranslations("aiTasks.coverageGraph")
  const [mode, setMode] = React.useState<"tree" | "topology">("tree")
  const [selectedKey, setSelectedKey] = React.useState<string | null>(null)
  const [topologyDetailOpen, setTopologyDetailOpen] = React.useState(false)
  const graph = useCoverageGraph(scope)
  const data = React.useMemo(() => {
    if (!graph.data) return null
    return coverageEnabled ? graph.data : { ...graph.data, nodes: graph.data.nodes.map(node => ({ ...node, tested: false })) }
  }, [graph.data, coverageEnabled])
  const index = React.useMemo(() => data ? buildCoverageTree(data) : null, [data])
  const selected = !graph.isError && selectedKey ? index?.nodes.get(selectedKey) ?? null : null
  const refs = useCoverageRefs(scope, selected?.asset_id, !graph.isError)

  React.useEffect(() => {
    if (graph.isError) { setSelectedKey(null); setTopologyDetailOpen(false); return }
    if (index) setSelectedKey(current => current && index.nodes.has(current) ? current : index.roots[0]?.key ?? null)
  }, [graph.isError, index])

  const otherParents = selected && index ? (index.parents.get(selected.key) ?? []).filter(node => node.key !== index.primaryParent.get(selected.key)) : []
  const relatedChildren = selected && index ? [...(index.children.get(selected.key)?.values() ?? [])].flat() : []
  const selectTree = (node: CoverageNode) => setSelectedKey(node.key)
  const selectTopology = (node: CoverageNode) => { setSelectedKey(node.key); setTopologyDetailOpen(true) }
  const detail = selected ? <CoverageAssetDetailBody key={selected.key} selected={selected} scopeKind={scope.kind} coverageEnabled={coverageEnabled} refs={refs} sourceNames={sourceNames} otherParents={otherParents} relatedChildren={relatedChildren} onSelect={mode === "tree" ? selectTree : selectTopology} /> : null

  return <div className="space-y-3">
    <div className="flex flex-wrap items-center gap-3 text-sm">
      <div className="flex items-center gap-1 rounded-md bg-muted p-1" role="group" aria-label={t("viewMode")}>
        <Button size="sm" variant={mode === "tree" ? "surface" : "ghost"} aria-pressed={mode === "tree"} onClick={() => { setMode("tree"); setTopologyDetailOpen(false) }}><ListTree className="size-4" />{t("treeView")}</Button>
        <Button size="sm" variant={mode === "topology" ? "surface" : "ghost"} aria-pressed={mode === "topology"} onClick={() => setMode("topology")}><Network className="size-4" />{t("topologyView")}</Button>
      </div>
      {mode === "topology" && <span className="text-muted-foreground">{t("foldHint")}</span>}
      <Button size="sm" variant="outline" onClick={() => { void graph.refetch(); if (selected?.asset_id) void refs.refetch() }} disabled={graph.isFetching}>{t("refresh")}</Button>
      {!graph.isError && graph.data?.truncated && <span className="text-amber-600">{t("truncated")}{mode === "tree" ? ` ${t("treeSearchLimit")}` : ""}</span>}
    </div>
    {graph.isError ? <p role="alert" className="text-sm text-destructive">{t("loadError")}</p> : graph.isLoading ? <div className="min-h-96 rounded-lg border p-4 text-sm text-muted-foreground">{t("loading")}</div> : !data?.nodes.length || !index ? <div className="min-h-96 rounded-lg border p-4 text-sm text-muted-foreground">{t("empty")}</div> : <>
      <div hidden={mode !== "tree"} className="overflow-hidden rounded-lg border bg-card" data-slot="coverage-tree-layout">
        <div className="flex min-h-[32rem] flex-col lg:h-[68vh] lg:flex-row">
          <CoverageTree index={index} selectedKey={selectedKey} coverageEnabled={coverageEnabled} onSelect={selectTree} />
          <aside className="flex max-h-[40vh] min-h-48 flex-col border-t bg-muted/20 lg:max-h-none lg:w-[22rem] lg:shrink-0 lg:border-l lg:border-t-0" aria-label={t("treeDetail")}>
            {mode === "tree" && (selected ? <><div className="border-b p-4"><h3 className="text-sm font-semibold">{t(`kinds.${selected.kind}`)}</h3><p className="mt-1 break-all text-sm text-muted-foreground">{selected.label}</p></div>{detail}</> : <p className="p-4 text-sm text-muted-foreground">{t("treeSelectAsset")}</p>)}
          </aside>
        </div>
      </div>
      {mode === "topology" && <CoverageTopology graph={data} onSelect={selectTopology} />}
    </>}
    <Sheet open={mode === "topology" && topologyDetailOpen && !!selected && !graph.isError} onOpenChange={setTopologyDetailOpen}><SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-md">
      <SheetHeader className="border-b p-4"><SheetTitle>{selected ? t(`kinds.${selected.kind}`) : t("asset")}</SheetTitle><SheetDescription className="break-all">{selected?.label}</SheetDescription></SheetHeader>{mode === "topology" ? detail : null}
    </SheetContent></Sheet>
  </div>
}
