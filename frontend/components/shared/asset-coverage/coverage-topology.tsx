// Adapted from ARTEX's task coverage graph, AGPL-3.0-only.
"use client"

import * as React from "react"
import { BaseEdge, Controls, Handle, Position, ReactFlow, useInternalNode, useNodesState, type Edge, type EdgeProps, type Node, type NodeProps, type ReactFlowInstance } from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { FoldVertical, List, Minus, MoreHorizontal, Plus, RotateCcw, UnfoldVertical } from "lucide-react"
import { useTranslations } from "next-intl"
import { useVirtualizer } from "@tanstack/react-virtual"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import type { CoverageGraph, CoverageKind, CoverageNode } from "@/types/asset-coverage.types"
import { COVERAGE_FOLD_LIMIT, COVERAGE_FOLD_STEP, coverageNodeLabel, coverageVisible, type CoverageFold } from "./coverage-graph-layout"
import { seedCoveragePoints, TOPOLOGY_CENTER, TOPOLOGY_NODE_HEIGHT, TOPOLOGY_NODE_WIDTH, type CoverageForceInput, type CoveragePoint, type ForceFrame, type ForceRequest } from "./coverage-force-layout"
import { coverageVisuals } from "./coverage-visuals"

const radii: Record<CoverageKind, number> = { company: 26, root_domain: 23, subdomain: 20, ip: 20, service: 18, app: 18, endpoint: 15 }
type AssetVisual = Node<{ asset: CoverageNode; radius: number; childCount: number; collapsed: boolean; inspect: (asset: CoverageNode) => void; toggle: (id: string) => void }, "asset">
type FoldVisual = Node<{ fold: CoverageFold; radius: number; expand: (fold: CoverageFold) => void; browse: (id: string) => void }, "fold">
type VisualNode = AssetVisual | FoldVisual

function CenterHandles() {
  return <><Handle type="target" position={Position.Left} style={{ left: "50%", top: TOPOLOGY_CENTER.y, opacity: 0, pointerEvents: "none" }} /><Handle type="source" position={Position.Right} style={{ left: "50%", right: "auto", top: TOPOLOGY_CENTER.y, opacity: 0, pointerEvents: "none" }} /></>
}

const AssetNode = React.memo(function AssetNode({ data, selected }: NodeProps<AssetVisual>) {
  const t = useTranslations("aiTasks.coverageGraph")
  const item = data.asset, config = coverageVisuals[item.kind], Icon = config.icon
  const borderColor = item.tested ? "#10b981" : item.in_scope ? "#7b8ca6" : "#aab8cc"
  return <div className="group relative" style={{ width: TOPOLOGY_NODE_WIDTH, height: TOPOLOGY_NODE_HEIGHT }}>
    <CenterHandles />
    <button type="button" onClick={() => data.inspect(item)} className="flex h-full w-full cursor-grab flex-col items-center rounded-lg outline-none active:cursor-grabbing focus-visible:ring-2 focus-visible:ring-ring" aria-label={`${t(`kinds.${item.kind}`)} ${item.label}`} title={`${item.label}\n${item.tested ? t("topologyTestedHelp") : item.in_scope ? t("topologyUntested") : t("topologyOutside")}`}>
      <span className="flex h-14 items-center justify-center">
        <span className={`relative flex shrink-0 items-center justify-center rounded-full border-2 shadow-sm transition-shadow ${!item.in_scope ? "border-dashed" : ""} ${selected ? "ring-4 ring-primary/20" : "group-hover:ring-4 group-hover:ring-slate-400/15"}`}
          style={{ width: data.radius * 2, height: data.radius * 2, borderColor, backgroundColor: item.in_scope ? (item.tested ? config.color : "#94a3b8") : "transparent", color: item.in_scope ? "white" : "#94a3b8" }}>
          <Icon style={{ width: data.radius, height: data.radius }} strokeWidth={1.8} />
          {item.tested && <span className="absolute -bottom-0.5 -right-0.5 size-2.5 rounded-full border-2 border-white bg-emerald-500 dark:border-slate-900" />}
        </span>
      </span>
      <span className={`mt-1 max-w-full truncate rounded px-1.5 py-0.5 text-[11px] leading-4 ${selected ? "bg-primary/10 font-medium text-foreground" : "bg-white/85 text-slate-600 dark:bg-slate-900/85 dark:text-slate-300"}`}>{coverageNodeLabel(item)}</span>
    </button>
    {data.childCount > 0 && <button type="button" className="nodrag nopan absolute top-0 flex size-5 items-center justify-center rounded-full border bg-background text-muted-foreground shadow-sm hover:bg-accent hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
      style={{ left: TOPOLOGY_CENTER.x + data.radius - 1 }}
      onClick={event => { event.stopPropagation(); data.toggle(item.key) }} aria-expanded={!data.collapsed}
      aria-label={t(data.collapsed ? "treeExpandAsset" : "treeCollapseAsset", { name: item.label })}
      title={t(data.collapsed ? "topologyExpandBranch" : "topologyCollapseBranch", { count: data.childCount })}>
      {data.collapsed ? <Plus className="size-3" /> : <Minus className="size-3" />}
    </button>}
  </div>
})

const FoldNode = React.memo(function FoldNode({ data, selected }: NodeProps<FoldVisual>) {
  const t = useTranslations("aiTasks.coverageGraph")
  const label = t("foldCount", { count: data.fold.hidden.length, kind: t(`kinds.${data.fold.kind}`) })
  return <div className="group relative" style={{ width: TOPOLOGY_NODE_WIDTH, height: TOPOLOGY_NODE_HEIGHT }}>
    <CenterHandles />
    <button type="button" onClick={() => data.expand(data.fold)} className="flex h-full w-full cursor-pointer flex-col items-center rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={`${label} · ${t("showMoreCount", { count: COVERAGE_FOLD_STEP })}`} title={label}>
      <span className="flex h-14 items-center"><span className={`relative flex size-10 items-center justify-center rounded-full border-2 border-dashed border-slate-400 bg-slate-200/80 text-slate-500 dark:bg-slate-800 dark:text-slate-300 ${selected ? "ring-4 ring-primary/20" : "group-hover:ring-4 group-hover:ring-slate-400/15"}`}><MoreHorizontal className="size-5" /></span></span>
      <span className="mt-1 max-w-full truncate rounded bg-white/85 px-1.5 py-0.5 text-[11px] leading-4 text-slate-500 dark:bg-slate-900/85 dark:text-slate-400">{label}</span>
    </button>
    <button type="button" className="nodrag nopan absolute right-10 top-0 flex size-5 items-center justify-center rounded-full border bg-background text-muted-foreground shadow-sm hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
      onClick={event => { event.stopPropagation(); data.browse(data.fold.key) }} aria-label={t("topologyBrowseFold")} title={t("topologyBrowseFold")}><List className="size-3" /></button>
  </div>
})

// The attachment point follows the circle, rather than the label's bounding box.
function RelationEdge({ id, source, target }: EdgeProps) {
  const a = useInternalNode(source), b = useInternalNode(target)
  if (!a || !b) return null
  const x1 = a.internals.positionAbsolute.x + TOPOLOGY_CENTER.x, y1 = a.internals.positionAbsolute.y + TOPOLOGY_CENTER.y
  const x2 = b.internals.positionAbsolute.x + TOPOLOGY_CENTER.x, y2 = b.internals.positionAbsolute.y + TOPOLOGY_CENTER.y
  const dx = x2 - x1, dy = y2 - y1, length = Math.hypot(dx, dy)
  if (length < 1) return null
  const from = Number(a.data.radius ?? 20), to = Number(b.data.radius ?? 20)
  if (length <= from + to) return null
  const path = `M ${x1 + dx * from / length},${y1 + dy * from / length} L ${x2 - dx * to / length},${y2 - dy * to / length}`
  return <BaseEdge id={id} path={path} className="!stroke-slate-300 dark:!stroke-slate-600" style={{ strokeWidth: 1.3 }} interactionWidth={8} />
}
const nodeTypes = { asset: AssetNode, fold: FoldNode }
const edgeTypes = { relation: RelationEdge }

export function CoverageTopology({ graph, onSelect, onShowTree }: { graph: CoverageGraph; onSelect: (node: CoverageNode) => void; onShowTree: () => void }) {
  const t = useTranslations("aiTasks.coverageGraph")
  const [expanded, setExpanded] = React.useState<Map<string, number>>(() => new Map())
  const [collapsed, setCollapsed] = React.useState<Set<string>>(() => new Set())
  const [selectedFoldId, setSelectedFoldId] = React.useState<string | null>(null)
  const [reset, setReset] = React.useState(0)
  const [reducedMotion, setReducedMotion] = React.useState(false)
  const [layoutError, setLayoutError] = React.useState(false)
  const points = React.useRef(new Map<string, CoveragePoint>())
  const generation = React.useRef(0)
  const send = React.useRef<((message: ForceRequest) => void) | null>(null)
  const dragging = React.useRef<string | null>(null)
  const suppressClickUntil = React.useRef(0)
  const shouldFit = React.useRef(true)
  const settled = React.useRef(false)
  const flow = React.useRef<ReactFlowInstance<VisualNode, Edge> | null>(null)
  const select = React.useRef(onSelect)
  React.useEffect(() => { select.current = onSelect }, [onSelect])
  React.useEffect(() => {
    const media = window.matchMedia("(prefers-reduced-motion: reduce)")
    const update = () => setReducedMotion(media.matches)
    update(); media.addEventListener("change", update)
    return () => media.removeEventListener("change", update)
  }, [])

  const view = React.useMemo(() => coverageVisible(graph, expanded, collapsed), [graph, expanded, collapsed])
  const childCounts = React.useMemo(() => {
    const valid = new Set(graph.nodes.map(node => node.key)), children = new Map<string, Set<string>>()
    for (const edge of graph.edges) if (edge.src !== edge.dst && valid.has(edge.src) && valid.has(edge.dst)) {
      const set = children.get(edge.dst) ?? new Set<string>(); set.add(edge.src); children.set(edge.dst, set)
    }
    return new Map([...children].map(([key, children]) => [key, children.size]))
  }, [graph])
  React.useEffect(() => {
    const valid = new Set(graph.nodes.map(node => node.key))
    for (const key of points.current.keys()) {
      if (valid.has(key)) continue
      if (key.startsWith("fold:")) {
        try { if (valid.has(JSON.parse(key.slice(5))[0])) continue } catch { /* Discard stale synthetic coordinates. */ }
      }
      points.current.delete(key)
    }
  }, [graph])
  const [nodes, setNodes, onNodesChange] = useNodesState<VisualNode>([])
  const inspect = React.useCallback((asset: CoverageNode) => {
    if (performance.now() < suppressClickUntil.current) return
    setSelectedFoldId(null)
    setNodes(previous => previous.map(node => ({ ...node, selected: node.id === asset.key })))
    select.current(asset)
  }, [setNodes])
  const toggle = React.useCallback((id: string) => setCollapsed(previous => {
    const next = new Set(previous); if (next.has(id)) next.delete(id); else next.add(id); return next
  }), [])
  const showMore = React.useCallback((fold: CoverageFold) => {
    if (performance.now() < suppressClickUntil.current) return
    if (!view.canExpand) { setSelectedFoldId(fold.key); return }
    setExpanded(previous => {
      const next = new Map(previous)
      next.set(fold.groupId, collapsed.has(fold.parentKey) ? COVERAGE_FOLD_LIMIT : (next.get(fold.groupId) ?? COVERAGE_FOLD_LIMIT) + COVERAGE_FOLD_STEP)
      return next
    })
    setCollapsed(previous => { if (!previous.has(fold.parentKey)) return previous; const next = new Set(previous); next.delete(fold.parentKey); return next })
  }, [collapsed, view.canExpand])
  const edges = React.useMemo<Edge[]>(() => view.edges.map((edge, index) => ({ id: `${edge.src}-${edge.dst}-${index}`, source: edge.dst, target: edge.src, type: "relation" })), [view.edges])
  const fit = React.useCallback(() => {
    if (!flow.current || !shouldFit.current) return
    shouldFit.current = false
    void flow.current.fitView({ padding: .26, maxZoom: 1.15, duration: reducedMotion ? 0 : 250 })
  }, [reducedMotion])

  React.useEffect(() => {
    let disposed = false, worker: Worker | null = null
    let fallback: ReturnType<typeof import("./coverage-force-engine").createCoverageForceEngine> | null = null
    let fallbackStarted = false
    const version = ++generation.current
    settled.current = false
    setLayoutError(false)
    const parents = new Map<string, string>()
    for (const edge of view.edges) if (!parents.has(edge.src)) parents.set(edge.src, edge.dst)
    const inputs: CoverageForceInput[] = [...view.nodes.map(asset => ({ id: asset.key, radius: radii[asset.kind], parent: parents.get(asset.key) })), ...view.folds.map(fold => ({ id: fold.key, radius: 20, parent: fold.parentKey }))]
    const seeded = seedCoveragePoints(inputs, points.current)
    const start: ForceRequest = { type: "start", generation: version, nodes: inputs, edges: view.edges, points: seeded }
    const byId = new Map(seeded.map(point => [point.id, point]))
    const position = (id: string) => ({ x: byId.get(id)!.x - TOPOLOGY_CENTER.x, y: byId.get(id)!.y - TOPOLOGY_CENTER.y })
    setNodes(previous => {
      const selected = new Set(previous.filter(node => node.selected).map(node => node.id))
      return [
        ...view.nodes.map((asset): AssetVisual => ({ id: asset.key, type: "asset", position: position(asset.key), width: TOPOLOGY_NODE_WIDTH, height: TOPOLOGY_NODE_HEIGHT, selected: selected.has(asset.key), ariaLabel: asset.label, data: { asset, radius: radii[asset.kind], childCount: childCounts.get(asset.key) ?? 0, collapsed: collapsed.has(asset.key), inspect, toggle } })),
        ...view.folds.map((fold): FoldVisual => ({ id: fold.key, type: "fold", position: position(fold.key), width: TOPOLOGY_NODE_WIDTH, height: TOPOLOGY_NODE_HEIGHT, data: { fold, radius: 20, expand: showMore, browse: setSelectedFoldId } })),
      ]
    })
    const accept = (frame: ForceFrame) => {
      if (disposed || frame.generation !== generation.current) return
      // A worker frame already in flight must not undo the most recent drag.
      const coordinates = frame.points.map(point => {
        const retained = points.current.get(point.id)
        return retained?.pinned ? retained : point
      })
      for (const point of coordinates) if (point.id !== dragging.current) points.current.set(point.id, point)
      if (!reducedMotion || frame.settled) {
        const next = new Map(coordinates.map(point => [point.id, point]))
        setNodes(previous => previous.map(node => {
          const point = next.get(node.id)
          return point && node.id !== dragging.current ? { ...node, position: { x: point.x - TOPOLOGY_CENTER.x, y: point.y - TOPOLOGY_CENTER.y } } : node
        }))
      }
      if (frame.settled) { settled.current = true; requestAnimationFrame(() => { if (!disposed) fit() }) }
    }
    const useFallback = async () => {
      if (disposed || fallbackStarted) return
      fallbackStarted = true
      worker?.terminate(); worker = null
      try {
        const { createCoverageForceEngine } = await import("./coverage-force-engine")
        if (disposed) return
        fallback = createCoverageForceEngine(accept)
        send.current = message => fallback?.receive(message)
        fallback.receive({ ...start, points: seedCoveragePoints(inputs, points.current) })
      } catch { if (!disposed) setLayoutError(true) }
    }
    try {
      worker = new Worker(new URL("./coverage-force.worker.ts", import.meta.url), { type: "module" })
      worker.onmessage = (event: MessageEvent<ForceFrame>) => accept(event.data)
      worker.onerror = event => { event.preventDefault(); void useFallback() }
      send.current = message => worker?.postMessage(message)
      worker.postMessage(start)
    } catch { void useFallback() }
    return () => { disposed = true; worker?.terminate(); fallback?.dispose(); send.current = null }
  }, [view, childCounts, collapsed, inspect, toggle, showMore, reducedMotion, reset, fit, setNodes])

  const pin = (node: VisualNode) => {
    const point = { id: node.id, x: node.position.x + TOPOLOGY_CENTER.x, y: node.position.y + TOPOLOGY_CENTER.y, pinned: true }
    points.current.set(node.id, point)
    send.current?.({ type: "pin", generation: generation.current, point })
  }
  const selectedFold = view.folds.find(fold => fold.key === selectedFoldId) ?? null
  const foldList = React.useRef<HTMLDivElement>(null)
  const virtual = useVirtualizer({ count: selectedFold?.hidden.length ?? 0, getScrollElement: () => foldList.current, estimateSize: () => 36, overscan: 8 })
  React.useEffect(() => { if (selectedFoldId && !selectedFold) setSelectedFoldId(null) }, [selectedFoldId, selectedFold])

  return <>
    {view.limited && <div role="status" className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
      {t("topologyLimited", { shown: view.nodes.length, total: graph.nodes.length })}
      <Button size="sm" variant="link" onClick={onShowTree}>{t("treeView")}</Button>
    </div>}
    <div className="relative h-[68vh] min-h-96 overflow-hidden rounded-xl border bg-[#eef2f8] dark:bg-[#141c2b]" data-slot="coverage-topology">
      <ReactFlow<VisualNode, Edge> nodes={nodes} edges={edges} nodeTypes={nodeTypes} edgeTypes={edgeTypes} onNodesChange={onNodesChange}
        onInit={instance => { flow.current = instance; if (settled.current) fit() }}
        onMoveStart={event => { if (event) shouldFit.current = false }}
        onNodeDragStart={(_, node) => { dragging.current = node.id; suppressClickUntil.current = Infinity; shouldFit.current = false; pin(node) }}
        onNodeDrag={(_, node) => pin(node)} onNodeDragStop={(_, node) => { pin(node); dragging.current = null; suppressClickUntil.current = performance.now() + 180 }}
        fitView minZoom={.05} maxZoom={2.5} nodesConnectable={false} nodesFocusable={false} edgesFocusable={false} edgesReconnectable={false} onlyRenderVisibleElements deleteKeyCode={null}>
        <Controls position="bottom-right" showInteractive={false} className="!overflow-hidden !rounded-lg !border !border-border !bg-background !shadow-sm [&_button]:!border-border [&_button]:!bg-background [&_button]:!fill-foreground [&_button:hover]:!bg-accent" />
      </ReactFlow>
      <div className="absolute left-3 top-3 z-10 max-w-[calc(100%-6rem)] rounded-lg border bg-background/95 p-3 shadow-sm backdrop-blur-sm" aria-label={t("topologyLegend")}>
        <div className="grid grid-cols-4 gap-x-3 gap-y-2">
          {(Object.entries(coverageVisuals) as [CoverageKind, (typeof coverageVisuals)[CoverageKind]][]).map(([kind, config]) => <span key={kind} className="flex items-center gap-1.5 text-[10px] text-muted-foreground"><span className="flex size-4 items-center justify-center rounded text-white" style={{ backgroundColor: config.color }}><config.icon className="size-2.5" /></span>{t(`kinds.${kind}`)}</span>)}
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-3 border-t pt-2 text-[10px] text-muted-foreground">
          <span className="flex items-center gap-1.5" title={t("topologyTestedHelp")}><i className="size-2 rounded-full bg-emerald-500" />{t("topologyTested")}</span>
          <span className="flex items-center gap-1.5"><i className="size-2 rounded-full bg-slate-400" />{t("topologyUntested")}</span>
          <span className="flex items-center gap-1.5"><i className="size-2 rounded-full border border-dashed border-slate-400" />{t("topologyOutside")}</span>
        </div>
      </div>
      <div className="absolute right-3 top-3 z-10 flex flex-col gap-1 rounded-lg border bg-background/95 p-1 shadow-sm">
        <Button size="icon-sm" variant="ghost" title={t("topologyRelayout")} aria-label={t("topologyRelayout")} onClick={() => { generation.current++; points.current.clear(); shouldFit.current = true; setReset(value => value + 1) }}><RotateCcw className="size-4" /></Button>
        <Button size="icon-sm" variant="ghost" title={t("topologyCollapseAll")} aria-label={t("topologyCollapseAll")} onClick={() => { shouldFit.current = true; setCollapsed(new Set(childCounts.keys())) }}><FoldVertical className="size-4" /></Button>
        <Button size="icon-sm" variant="ghost" title={t("topologyRestore")} aria-label={t("topologyRestore")} onClick={() => { shouldFit.current = true; setCollapsed(new Set()); setExpanded(new Map()) }}><UnfoldVertical className="size-4" /></Button>
      </div>
      {layoutError && <p role="alert" className="absolute bottom-3 left-3 rounded-md border bg-background/95 px-3 py-2 text-xs text-destructive">{t("topologyLayoutError")}</p>}
    </div>
    <Sheet open={!!selectedFold} onOpenChange={open => { if (!open) setSelectedFoldId(null) }}><SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-md">
      {selectedFold && <><SheetHeader className="border-b p-4"><SheetTitle>{t("hiddenAssets", { kind: t(`kinds.${selectedFold.kind}`), count: selectedFold.hidden.length })}</SheetTitle><SheetDescription>{t("foldDescription")}</SheetDescription></SheetHeader>
        <div ref={foldList} className="min-h-0 flex-1 overflow-auto p-3"><div style={{ height: virtual.getTotalSize(), position: "relative" }}>
          {virtual.getVirtualItems().map(row => { const asset = selectedFold.hidden[row.index]; return <button key={asset.key} type="button" onClick={() => { setSelectedFoldId(null); inspect(asset) }} className="absolute left-0 top-0 flex w-full items-center gap-2 rounded-md px-2 text-left text-sm hover:bg-accent focus-visible:outline-2 focus-visible:outline-ring" style={{ height: row.size, transform: `translateY(${row.start}px)` }}><span className={`size-2 shrink-0 rounded-full ${asset.tested ? "bg-emerald-500" : "bg-slate-400"}`} /><span className="min-w-0 flex-1 truncate font-mono text-xs" title={asset.label}>{asset.label}</span></button> })}
        </div></div>
        <div className="border-t p-3"><Button className="w-full" variant="outline" onClick={() => view.canExpand ? showMore(selectedFold) : onShowTree()}>{view.canExpand ? t("showMoreCount", { count: COVERAGE_FOLD_STEP }) : t("treeView")}</Button></div></>}
    </SheetContent></Sheet>
  </>
}
