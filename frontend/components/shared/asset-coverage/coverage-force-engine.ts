import { forceCollide, forceLink, forceManyBody, forceSimulation, forceX, forceY, type SimulationNodeDatum } from "d3-force"
import type { CoverageForceInput, ForceFrame, ForceRequest } from "./coverage-force-layout"

type Particle = SimulationNodeDatum & CoverageForceInput & { x: number; y: number; pinned: boolean }

// Runs in a worker normally; the same bounded scheduler is the browser fallback.
export function createCoverageForceEngine(publish: (frame: ForceFrame) => void) {
  let simulation: ReturnType<typeof forceSimulation<Particle>> | null = null
  let generation = 0
  let particles = new Map<string, Particle>()
  let timer: ReturnType<typeof setTimeout> | null = null
  let disposed = false
  let lastFrame = 0
  const frame = (settled: boolean) => {
    if (disposed || !simulation) return
    publish({ generation, settled, points: simulation.nodes().map(node => ({ id: node.id, x: node.x, y: node.y, pinned: node.pinned })) })
  }
  const tick = () => {
    timer = null
    if (disposed || !simulation) return
    simulation.tick(particles.size < 300 ? 3 : 1)
    const settled = simulation.alpha() < simulation.alphaMin()
    if (settled || performance.now() - lastFrame >= (particles.size > 1000 ? 180 : 60)) {
      frame(settled)
      lastFrame = performance.now()
    }
    if (!settled) timer = setTimeout(tick, 16)
  }
  const wake = () => { if (!disposed && timer === null) timer = setTimeout(tick, 0) }
  return {
    receive(message: ForceRequest) {
      if (disposed) return
      if (message.type === "start") {
        simulation?.stop()
        if (timer !== null) clearTimeout(timer)
        timer = null
        generation = message.generation
        const saved = new Map(message.points.map(point => [point.id, point]))
        const nodes: Particle[] = message.nodes.map(node => {
          const point = saved.get(node.id)
          return { ...node, x: point?.x ?? 0, y: point?.y ?? 0, pinned: !!point?.pinned, fx: point?.pinned ? point.x : null, fy: point?.pinned ? point.y : null }
        })
        particles = new Map(nodes.map(node => [node.id, node]))
        const links = message.edges.filter(edge => edge.src !== edge.dst && particles.has(edge.src) && particles.has(edge.dst)).map(edge => ({ source: edge.dst, target: edge.src }))
        simulation = forceSimulation(nodes).stop()
          .alphaMin(.018).alphaDecay(.035).velocityDecay(.42)
          .force("links", forceLink<Particle, { source: string; target: string }>(links).id(node => node.id).distance(170).strength(.42))
          .force("charge", forceManyBody<Particle>().strength(-420).distanceMin(36).distanceMax(1200))
          .force("collide", forceCollide<Particle>().radius(node => Math.max(82, node.radius + 48)).iterations(2))
          .force("x", forceX<Particle>(0).strength(.018))
          .force("y", forceY<Particle>(0).strength(.018))
        lastFrame = 0
        wake()
      } else if (message.generation === generation && simulation) {
        const node = particles.get(message.point.id)
        if (!node) return
        node.x = node.fx = message.point.x
        node.y = node.fy = message.point.y
        node.vx = node.vy = 0
        node.pinned = true
        simulation.alpha(Math.max(.25, simulation.alpha()))
        wake()
      }
    },
    dispose() {
      disposed = true
      if (timer !== null) clearTimeout(timer)
      simulation?.stop()
      particles.clear()
    },
  }
}
