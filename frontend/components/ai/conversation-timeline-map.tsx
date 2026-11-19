"use client"

import * as React from "react"
import {
  Background, Controls, getViewportForBounds, Handle, MiniMap, Panel,
  Position, ReactFlow, useNodesState, useReactFlow, useStore,
} from "@xyflow/react"
import type { NodeProps } from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { ArrowLeft, ArrowRight, GripVerticalIcon, semanticIcons } from "@/components/icons"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { textRole } from "@/lib/typography"
import { cn } from "@/lib/utils"
import { nodeWidth, nodeHeight, relations, sectionWindowSize, stages, timelineDiagram, type MapNode, type TimelineMapProps } from "./conversation-timeline-graph"
import styles from "./conversation-timeline-map.module.css"

const icons = { goal: semanticIcons.concept.target, section: semanticIcons.concept.workflow, finding: semanticIcons.concept.vulnerability, end: semanticIcons.status.success }
const kinds = ["goal", "section", "finding", "end"] as const
const relationOrder = ["contains", "registration", "citation", "summary"] as const
const fitPadding = { x: "40px", top: "112px", bottom: "72px" } as const
const fitOptions = { padding: fitPadding, maxZoom: 1 }

function Stage({ data }: NodeProps<MapNode>) {
  const t = useTranslations("aiChat.exploration")
  const Icon = icons[data.kind]
  return <div className={styles.node} data-selected={data.selected || undefined} style={{ "--stage-color": stages[data.kind].color } as React.CSSProperties}>
    {data.kind !== "goal" && <Handle id="in" type="target" position={Position.Left} className={styles.port} />}
    {(data.kind === "goal" || data.kind === "section") && <Handle id="out" type="source" position={Position.Right} className={styles.port} />}
    {data.cited && <Handle id="citation" type="target" position={Position.Left} className={cn(styles.port, styles.citationPort)} />}
    <div className={cn("trace-drag-handle", styles.nodeHeader)} title={t("map.drag")}>
      <span className={styles.glyph}><Icon className="size-3.5" aria-hidden /></span>
      <span className={cn(textRole.compactPrimary, "flex-1")}>{t(stages[data.kind].label)}{data.ordinal ? ` ${String(data.ordinal).padStart(2, "0")}` : ""}</span>
      <GripVerticalIcon className="size-3.5 text-muted-foreground" aria-hidden />
    </div>
    <Button variant="ghost" size="content" className={cn("nodrag nopan", styles.nodeContent)} onClick={data.open} aria-haspopup="dialog" aria-label={`${t(stages[data.kind].label)} · ${data.title}`}>
      <span className={cn(textRole.bodyStrong, styles.headline)} title={data.title}>{data.title}</span>
      <span className={cn(textRole.compactCaption, styles.context)} title={data.context || data.label}>{data.context || data.label}</span>
      <span className={styles.nodeFooter}>
        {data.badge ? <span className={cn(textRole.compactCaption, styles.badge)} data-tone={data.badge.tone}>{data.badge.text}</span> : <span className={textRole.compactCaption}>{t("map.inspect")}</span>}
        <span className={cn(textRole.compactCaption, "tabular-nums")}>{data.meta}</span>
      </span>
    </Button>
  </div>
}
const nodeTypes = { stage: Stage }

// Restore layout only when requested; polling preserves manually dragged nodes.
export function reconcileMapNodes(previous: MapNode[], next: MapNode[]) {
  const positions = new Map(previous.map(node => [node.id, node.position]))
  const occupied = next.flatMap(node => positions.has(node.id) ? [positions.get(node.id)!] : [])
  return next.map(node => {
    const saved = positions.get(node.id)
    if (saved) return { ...node, position: saved }
    const position = { ...node.position }
    // New receipts must not land on an existing (possibly dragged) card.
    while (occupied.some(other => Math.abs(other.x - position.x) < nodeWidth + 16 && Math.abs(other.y - position.y) < nodeHeight + 16)) position.y += nodeHeight + 28
    occupied.push(position)
    return { ...node, position }
  })
}

function MapViewport({ focusId, offset, reset }: { focusId: string; offset: number; reset: number }) {
  const { viewportInitialized, getNodes, getNodesBounds, getViewport, setViewport } = useReactFlow<MapNode>()
  const width = useStore(state => state.width)
  const height = useStore(state => state.height)
  const nodeCount = useStore(state => state.nodeLookup.size)
  const previous = React.useRef<{ focusId: string; offset: number; reset: number; width: number; height: number } | null>(null)
  React.useEffect(() => {
    if (!viewportInitialized || !width || !height) return
    const nodes = getNodes()
    if (!nodes.length) return
    const old = previous.current
    previous.current = { focusId, offset, reset, width, height }
    if (old && old.offset === offset && old.reset === reset && old.focusId === focusId) {
      if (old.width !== width || old.height !== height) {
        const view = getViewport()
        void setViewport({ ...view, x: view.x + (width - old.width) / 2, y: view.y + (height - old.height) / 2 })
      }
      return
    }
    const overview = getViewportForBounds(getNodesBounds(nodes), width, height, 0.1, 1, fitPadding)
    if (!old || old.offset !== offset || old.reset !== reset) {
      // Fit a small graph; keep a large graph readable around the current task.
      if (overview.zoom >= 0.55 || (old && old.reset !== reset)) { void setViewport(overview); return }
    }
    const focus = nodes.find(node => node.id === focusId) ?? nodes[0]
    const box = getNodesBounds([focus])
    const zoom = Math.min(0.85, Math.max(0.3, (width - 48) / box.width))
    void setViewport({ zoom, x: width / 2 - (box.x + box.width / 2) * zoom, y: height / 2 - (box.y + box.height / 2) * zoom })
  }, [focusId, offset, reset, viewportInitialized, width, height, nodeCount, getNodes, getNodesBounds, getViewport, setViewport])
  return null
}

function GraphCanvas({ diagram }: { diagram: ReturnType<typeof timelineDiagram> }) {
  const t = useTranslations("aiChat.exploration")
  const id = React.useId()
  const [nodes, setNodes, onNodesChange] = useNodesState<MapNode>(diagram.nodes)
  const [reset, setReset] = React.useState(0)
  React.useEffect(() => { setNodes(previous => reconcileMapNodes(previous, diagram.nodes)) }, [diagram.nodes, setNodes])
  return <ReactFlow<MapNode> id={id} nodes={nodes} edges={diagram.edges} nodeTypes={nodeTypes} onNodesChange={onNodesChange}
    minZoom={0.1} maxZoom={1.8} nodesConnectable={false} nodesFocusable={false} edgesFocusable={false}
    elementsSelectable={false} deleteKeyCode={null} onlyRenderVisibleElements
    ariaLabelConfig={{ "controls.ariaLabel": t("map.controls"), "controls.zoomIn.ariaLabel": t("map.zoomIn"), "controls.zoomOut.ariaLabel": t("map.zoomOut"), "controls.fitView.ariaLabel": t("map.fit"), "minimap.ariaLabel": t("map.minimap") }}>
    <MapViewport focusId={diagram.focusId} offset={diagram.offset} reset={reset} />
    <Background gap={20} size={1} color="var(--border)" />
    <Panel position="top-left" className={styles.legend}>
      <div className="flex flex-wrap gap-x-4 gap-y-2">{kinds.map(kind => {
        const Icon = icons[kind]
        return <span key={kind} className={cn(textRole.compactPrimary, "inline-flex items-center gap-1.5")}><span className={styles.legendGlyph} style={{ color: stages[kind].color }}><Icon className="size-3.5" aria-hidden /></span>{t(stages[kind].label)}</span>
      })}</div>
      <div className="mt-2 flex flex-wrap gap-x-4 gap-y-2 border-t pt-2">{relationOrder.filter(relation => diagram.usedRelations.has(relation)).map(relation => <span key={relation} className={cn(textRole.compactCaption, "inline-flex items-center gap-1.5")}>
        <svg width="18" height="8" aria-hidden><path d="M0 4h18" stroke={relations[relation].color} strokeWidth="1.5" strokeDasharray={relations[relation].dash} /></svg>{t(relations[relation].label)}
      </span>)}</div>
    </Panel>
    <Panel position="top-right"><Button size="sm" variant="outline" onClick={() => { setNodes(diagram.nodes); setReset(value => value + 1) }}><semanticIcons.action.refresh className="size-3.5" />{t("map.relayout")}</Button></Panel>
    <Controls className={styles.controls} showInteractive={false} fitViewOptions={fitOptions} />
    <MiniMap<MapNode> pannable zoomable className={styles.minimap} nodeBorderRadius={4} maskColor="var(--muted)" nodeColor={node => stages[node.data.kind].color} />
  </ReactFlow>
}

export function TimelineMap(props: TimelineMapProps) {
  const t = useTranslations("aiChat.exploration")
  const diagram = React.useMemo(() => timelineDiagram(props, t), [props, t])
  const count = props.graph.sections.length
  const { offset } = diagram
  const previous = props.graph.sections[offset - 1]
  const next = props.graph.sections[offset + sectionWindowSize]
  return <section className={styles.frame} aria-label={t("mainline")}>
    <div className={styles.canvas}><GraphCanvas key={offset} diagram={diagram} /></div>
    <footer className={styles.mapFooter}>
      <span className={textRole.compactCaption}>{t("map.readingHint")}</span>
      <nav className="flex shrink-0 items-center gap-2" aria-label={t("taskPurpose.navigation")}>
        <span className={textRole.compactCaption}>{count > sectionWindowSize ? t("map.window", { first: offset + 1, last: Math.min(offset + sectionWindowSize, count), total: count }) : t("map.sections", { count })}</span>
        {count > sectionWindowSize && <><Button size="icon-sm" variant="outline" aria-label={t("taskPurpose.earlier")} disabled={!previous} onClick={() => previous && props.choose(previous.first, false)}><ArrowLeft className="size-4" /></Button><Button size="icon-sm" variant="outline" aria-label={t("taskPurpose.later")} disabled={!next} onClick={() => next && props.choose(next.first, false)}><ArrowRight className="size-4" /></Button></>}
      </nav>
    </footer>
  </section>
}
