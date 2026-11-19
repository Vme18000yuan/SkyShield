"use client"

import * as React from "react"
import {
  Background, Controls, getViewportForBounds, Handle, MiniMap,
  Position, ReactFlow, useReactFlow, useStore,
} from "@xyflow/react"
import type { NodeProps } from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { ArrowLeft, ArrowRight, Compass, FileCheck2, Flag, GitBranch, Target } from "lucide-react"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { relations, sectionWindowSize, stages, timelineDiagram, type MapNode, type TimelineMapProps } from "./conversation-timeline-graph"
import styles from "./conversation-timeline-map.module.css"

const icons = { goal: Target, section: Compass, finding: FileCheck2, end: Flag }
const kindOrder = ["goal", "section", "finding", "end"] as const
const relationOrder = ["order", "registration", "citation"] as const
const fitPadding = { x: "22px", top: "46px", bottom: "32px" } as const
const fitOptions = { padding: fitPadding, maxZoom: 1 }

function Stage({ data, targetPosition = Position.Top }: NodeProps<MapNode>) {
  const t = useTranslations("aiChat.exploration")
  const { kind, badge } = data
  const Icon = icons[kind]
  return (
    <div className={styles.node} style={{ "--stage-color": stages[kind].color } as React.CSSProperties}>
      {kind !== "goal" && kind !== "finding" && <Handle id="previous" type="target" position={Position.Left} className={styles.port} />}
      {(kind === "goal" || kind === "section") && <Handle id="next" type="source" position={Position.Right} className={styles.port} />}
      {kind === "section" && <>
        <Handle id="branch-up" type="source" position={Position.Top} className={styles.branchPort} />
        <Handle id="branch-down" type="source" position={Position.Bottom} className={styles.branchPort} />
      </>}
      {kind === "finding" && <>
        <Handle id="receipt" type="target" position={targetPosition} className={styles.port} />
        <Handle id="citation" type="target" position={targetPosition} className={styles.citationPort} style={{ opacity: data.cited ? 1 : 0 }} />
      </>}
      <button type="button" className={"nopan " + styles.card} aria-pressed={data.selected}
        aria-haspopup={kind === "section" ? undefined : "dialog"} onClick={data.open}>
        <span className="flex w-full items-center gap-2">
          <span className={styles.glyph}><Icon size={15} strokeWidth={2} aria-hidden /></span>
          <span className="min-w-0 flex-1 truncate text-xs font-semibold">{t(stages[kind].label)}</span>
          {badge && <span className={styles.badge} data-tone={badge.tone}>{badge.text}</span>}
        </span>
        <strong className={styles.headline} title={data.title}>{data.title}</strong>
        {data.context && <span className={styles.context} title={data.context}>{data.context}</span>}
        {(data.label || data.meta) && <span className={styles.footer}>
          <span className="min-w-0 truncate" title={data.label}>{data.label}</span>
          {data.meta && <span className="shrink-0 tabular-nums" title={data.meta}>{data.meta}</span>}
        </span>}
      </button>
    </div>
  )
}
const nodeTypes = { stage: Stage }

// Leave room for the legend and controls. For a long trace, open on the current
// section at reading size; the fit control can still show the entire window.
function frameAxis(start: number, length: number, anchor: number, size: number, inset: number, zoom: number) {
  const first = inset - start * zoom
  const last = size - inset - (start + length) * zoom
  return first <= last ? (first + last) / 2 : Math.min(first, Math.max(last, size / 2 - anchor * zoom))
}

function MapViewport({ nodes, focusId, offset }: { nodes: MapNode[]; focusId: string; offset: number }) {
  const { viewportInitialized, getNodesBounds, getViewport, setViewport } = useReactFlow<MapNode>()
  const width = useStore(state => state.width)
  const height = useStore(state => state.height)
  const last = React.useRef<{ focusId: string; offset: number; width: number; height: number } | null>(null)

  React.useEffect(() => {
    if (!viewportInitialized || !width || !height) return
    const previous = last.current
    const changedWindow = !previous || previous.offset !== offset || previous.width !== width
    last.current = { focusId, offset, width, height }
    if (!changedWindow && previous.focusId === focusId) {
      // When new receipts add a lane, keep the same point under the centre
      // of the canvas, including after the reader has panned or zoomed.
      if (previous.height !== height) {
        const view = getViewport()
        void setViewport({ ...view, y: view.y + (height - previous.height) / 2 })
      }
      return
    }
    const focus = nodes.find(node => node.id === focusId)
    if (!focus) return
    const bounds = getNodesBounds(nodes)
    const overview = getViewportForBounds(bounds, width, height, 0.1, 1, fitPadding)
    if (changedWindow && overview.zoom >= 0.74) {
      void setViewport(overview)
      return
    }
    const box = getNodesBounds([focus])
    const view = getViewport()
    const left = view.x + box.x * view.zoom
    const top = view.y + box.y * view.zoom
    if (!changedWindow && left >= 8 && top >= 44 && left + box.width * view.zoom <= width - 8 && top + box.height * view.zoom <= height - 42) return
    const zoom = changedWindow ? Math.min(0.92, (width - 24) / box.width) : view.zoom
    void setViewport({
      zoom,
      x: frameAxis(bounds.x, bounds.width, box.x + box.width / 2, width, 22, zoom),
      y: frameAxis(bounds.y, bounds.height, box.y + box.height / 2, height, 52, zoom),
    })
  }, [focusId, getNodesBounds, getViewport, height, nodes, offset, setViewport, viewportInitialized, width])
  return null
}

export function TimelineMap({ graph, from, choose, goal, outcome, receipts }: TimelineMapProps) {
  const t = useTranslations("aiChat.exploration")
  const id = React.useId()
  const { nodes, edges, usedRelations, offset, focusId } = React.useMemo(
    () => timelineDiagram({ graph, from, choose, goal, outcome, receipts }, t),
    [graph, from, choose, goal, outcome, receipts, t],
  )
  const count = graph.sections.length
  const previous = graph.sections[offset - 1]
  const next = graph.sections[offset + sectionWindowSize]
  const kinds = new Set(nodes.map(node => node.data.kind))
  const lanes = nodes.some(node => node.position.y < 0) ? 2 : Number(kinds.has("finding"))
  return (
    <section className={styles.frame} aria-label={t("mainline")}>
      <header className={styles.header}>
        <h3 className="flex shrink-0 items-center gap-2 text-sm font-semibold">
          <GitBranch size={16} className="text-muted-foreground" aria-hidden />{t("mainline")}
        </h3>
        <span className="text-xs tabular-nums text-muted-foreground">{count > sectionWindowSize
          ? t("map.window", { first: offset + 1, last: Math.min(offset + sectionWindowSize, count), total: count })
          : t("map.sections", { count })}</span>
        {count > sectionWindowSize && <nav className="flex items-center gap-1" aria-label={t("taskPurpose.navigation")}>
          <Button size="icon-sm" variant="outline" aria-label={t("taskPurpose.earlier")} disabled={!previous} onClick={() => previous && choose(previous.first, false)}><ArrowLeft size={14} /></Button>
          <Button size="icon-sm" variant="outline" aria-label={t("taskPurpose.later")} disabled={!next} onClick={() => next && choose(next.first, false)}><ArrowRight size={14} /></Button>
        </nav>}
        <div className={styles.edgeLegend}>
          {relationOrder.filter(key => usedRelations.has(key)).map(key => <span key={key} className="inline-flex items-center gap-1.5">
            <svg width="21" height="8" aria-hidden><path d="M1 4h19" fill="none" stroke={relations[key].color} strokeWidth="2" strokeDasharray={relations[key].dash} strokeLinecap="round" /></svg>
            {t(relations[key].label)}
          </span>)}
        </div>
      </header>
      <div className={styles.board}>
        <div className={styles.legend} role="group" aria-label={t("map.legend")}>
          {kindOrder.filter(kind => kinds.has(kind)).map(kind => {
            const Icon = icons[kind]
            return <span key={kind} className="inline-flex items-center gap-1.5">
              <span className={styles.legendGlyph} style={{ background: stages[kind].color }}><Icon size={12} aria-hidden /></span>
              {t(stages[kind].label)}
            </span>
          })}
        </div>
        <div className={styles.canvas} data-lanes={lanes}>
          <ReactFlow<MapNode> id={id} nodes={nodes} edges={edges} nodeTypes={nodeTypes} minZoom={0.1} maxZoom={1.8}
            nodesDraggable={false} nodesConnectable={false} nodesFocusable={false} edgesFocusable={false}
            elementsSelectable={false} deleteKeyCode={null} zoomOnScroll={false} preventScrolling={false}
            attributionPosition="top-right"
            ariaLabelConfig={{
              "controls.ariaLabel": t("map.controls"), "controls.zoomIn.ariaLabel": t("map.zoomIn"),
              "controls.zoomOut.ariaLabel": t("map.zoomOut"), "controls.fitView.ariaLabel": t("map.fit"),
              "minimap.ariaLabel": t("map.minimap"),
            }}>
            <MapViewport nodes={nodes} focusId={focusId} offset={offset} />
            <Background gap={18} size={1} color="var(--trace-grid)" />
            <Controls className={styles.controls} orientation="horizontal" showInteractive={false} fitViewOptions={fitOptions} />
            {nodes.length > 2 && <MiniMap<MapNode> pannable zoomable className={styles.minimap}
              style={{ width: 118, height: 68 }} maskColor="var(--trace-mask)" nodeBorderRadius={5}
              nodeColor={node => stages[node.data.kind].color} />}
          </ReactFlow>
        </div>
      </div>
    </section>
  )
}
