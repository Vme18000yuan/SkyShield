"use client"

import { useEffect, useId, useRef } from "react"
import { geoDistance, geoGraticule, geoOrthographic, geoPath } from "d3-geo"
import { feature } from "topojson-client"
import type { GeometryCollection, Topology } from "topojson-specification"
import type { MultiLineString } from "geojson"
import landTopology from "world-atlas/land-110m.json"
import { getSatelliteFrames, satelliteOrbits } from "./login-satellite-orbits"

// Bundled Natural Earth coastline data; no network or GPU is needed at sign-in.
const topology = landTopology as unknown as Topology<{ land: GeometryCollection }>
const land = feature(topology, topology.objects.land)
const graticule = geoGraticule().step([30, 30])()
const initialLongitude = 110
const latitude = 18
const frameInterval = 1000 / 30
const rotationPerMillisecond = 360 / 150_000

// These connections illustrate asset coverage, not live infrastructure or traffic.
const locations: [number, number][] = [
  [116.4, 39.9], [139.7, 35.7], [103.8, 1.3], [72.8, 19.1], [151.2, -33.9],
  [-0.1, 51.5], [-74, 40.7], [-122.4, 37.8], [-46.6, -23.5], [28, -26],
]
const connections: MultiLineString = {
  type: "MultiLineString",
  coordinates: [[0, 1], [0, 2], [2, 3], [2, 4], [3, 5], [5, 6], [6, 7], [6, 8], [3, 9]]
    .map(([from, to]) => [locations[from], locations[to]]),
}
const stars = [[205, 146], [320, 97], [566, 119], [725, 195], [113, 365], [771, 402], [647, 638], [235, 657]]

function createGlobeGeometry() {
  const projection = geoOrthographic().translate([450, 390]).scale(250).clipAngle(90).precision(0.8)
  const path = geoPath(projection)

  return (angle: number) => {
    projection.rotate([-initialLongitude + angle, -latitude, -12])
    const center: [number, number] = [initialLongitude - angle, latitude]
    return {
      land: path(land) ?? "",
      grid: path(graticule) ?? "",
      connections: path(connections) ?? "",
      nodes: locations.map(location => {
        const [x, y] = projection(location) ?? [450, 390]
        const distanceToLimb = Math.PI / 2 - geoDistance(center, location)
        return {
          transform: `translate(${x.toFixed(2)} ${y.toFixed(2)})`,
          opacity: Math.max(0, Math.min(1, distanceToLimb / 0.18)),
        }
      }),
    }
  }
}

const initialFrame = createGlobeGeometry()(0)
const initialSatellites = getSatelliteFrames(0)

export function LoginSkyIllustration({ active = true }: { active?: boolean }) {
  const id = useId()
  const svgRef = useRef<SVGSVGElement>(null)
  const landRef = useRef<SVGPathElement>(null)
  const gridRef = useRef<SVGPathElement>(null)
  const connectionsRef = useRef<SVGPathElement>(null)
  const nodesRef = useRef<Array<SVGGElement | null>>([])
  const elapsedRef = useRef(0)
  const nearSatellitesRef = useRef<Array<SVGGElement | null>>([])
  const farSatellitesRef = useRef<Array<SVGGElement | null>>([])
  const nearTrailsRef = useRef<Array<SVGPathElement | null>>([])
  const farTrailsRef = useRef<Array<SVGPathElement | null>>([])

  useEffect(() => {
    if (!active) return

    const geometry = createGlobeGeometry()
    const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)")
    let frame = 0
    let lastTime: number | null = null
    let lastDraw = 0
    let inViewport = true

    const animate = (time: number) => {
      const elapsed = lastTime === null ? 0 : Math.min(time - lastTime, 100)
      lastTime = time
      elapsedRef.current += elapsed

      if (time - lastDraw >= frameInterval) {
        lastDraw = time
        const next = geometry((elapsedRef.current * rotationPerMillisecond) % 360)
        landRef.current?.setAttribute("d", next.land)
        gridRef.current?.setAttribute("d", next.grid)
        connectionsRef.current?.setAttribute("d", next.connections)
        next.nodes.forEach((node, index) => {
          nodesRef.current[index]?.setAttribute("transform", node.transform)
          nodesRef.current[index]?.setAttribute("opacity", String(node.opacity))
        })
        getSatelliteFrames(elapsedRef.current).forEach((satellite, index) => {
          nearSatellitesRef.current[index]?.setAttribute("transform", satellite.transform)
          farSatellitesRef.current[index]?.setAttribute("transform", satellite.transform)
          nearSatellitesRef.current[index]?.setAttribute("opacity", String(satellite.nearOpacity))
          farSatellitesRef.current[index]?.setAttribute("opacity", String(satellite.farOpacity))
          nearTrailsRef.current[index]?.setAttribute("d", satellite.trail)
          farTrailsRef.current[index]?.setAttribute("d", satellite.trail)
          nearTrailsRef.current[index]?.setAttribute("opacity", String(satellite.nearOpacity))
          farTrailsRef.current[index]?.setAttribute("opacity", String(satellite.farOpacity))
        })
      }

      frame = window.requestAnimationFrame(animate)
    }

    const syncAnimation = () => {
      window.cancelAnimationFrame(frame)
      lastTime = null
      if (!reducedMotion.matches && !document.hidden && inViewport) {
        frame = window.requestAnimationFrame(animate)
      }
    }

    const observer = typeof IntersectionObserver === "undefined" ? null : new IntersectionObserver(([entry]) => {
      inViewport = entry.isIntersecting
      syncAnimation()
    })
    if (svgRef.current) observer?.observe(svgRef.current)
    reducedMotion.addEventListener("change", syncAnimation)
    document.addEventListener("visibilitychange", syncAnimation)
    syncAnimation()

    return () => {
      window.cancelAnimationFrame(frame)
      observer?.disconnect()
      reducedMotion.removeEventListener("change", syncAnimation)
      document.removeEventListener("visibilitychange", syncAnimation)
    }
  }, [active])

  const renderSatellites = (layer: "near" | "far") => (
    <g data-satellite-orbits={layer}>
      {satelliteOrbits.map((orbit, index) => (
        <path key={`orbit-${index}`} d={orbit[layer]} stroke="var(--muted-foreground)" strokeWidth="0.8" strokeOpacity={layer === "near" ? 0.25 : 0.16} />
      ))}
      {initialSatellites.map((satellite, index) => (
        <path
          key={`trail-${index}`}
          ref={element => { (layer === "near" ? nearTrailsRef : farTrailsRef).current[index] = element }}
          d={satellite.trail}
          opacity={layer === "near" ? satellite.nearOpacity : satellite.farOpacity}
          stroke={satellite.accent ? "var(--interaction-accent)" : "var(--muted-foreground)"}
          strokeOpacity="0.5"
          strokeWidth="1.6"
          strokeLinecap="round"
        />
      ))}
      {initialSatellites.map((satellite, index) => (
        <g
          key={`satellite-${index}`}
          ref={element => { (layer === "near" ? nearSatellitesRef : farSatellitesRef).current[index] = element }}
          data-satellite={index}
          data-satellite-layer={layer}
          transform={satellite.transform}
          opacity={layer === "near" ? satellite.nearOpacity : satellite.farOpacity}
        >
          <use href={`#${id}-satellite`} />
        </g>
      ))}
    </g>
  )

  return (
    <div className="product-login__sky" aria-hidden="true">
      <svg ref={svgRef} className="product-login__sky-art" viewBox="0 0 900 780" fill="none" focusable="false">
        <defs>
          <g id={`${id}-satellite`} stroke="var(--foreground)" strokeWidth="0.9" strokeLinejoin="round">
            <path d="M-6 0H6 M0-5V-8 M-3-10Q0-6 3-10" fill="none" strokeOpacity="0.7" />
            <rect x="-16" y="-4" width="10" height="8" rx="0.5" fill="var(--background)" />
            <rect x="6" y="-4" width="10" height="8" rx="0.5" fill="var(--background)" />
            <path d="M-11-4V4 M11-4V4 M-16 0H-6 M6 0H16" strokeOpacity="0.35" />
            <rect x="-3.5" y="-5" width="7" height="10" rx="1" fill="var(--muted)" />
            <path d="M0-2V2" stroke="var(--interaction-accent)" strokeWidth="1.5" />
          </g>
          <radialGradient id={`${id}-ocean`} cx="35%" cy="28%" r="75%">
            <stop stopColor="var(--background)" />
            <stop offset="0.7" stopColor="var(--background)" />
            <stop offset="1" stopColor="var(--muted)" />
          </radialGradient>
          <radialGradient id={`${id}-shade`} cx="35%" cy="28%" r="75%">
            <stop offset="0.5" stopColor="var(--foreground)" stopOpacity="0" />
            <stop offset="1" stopColor="var(--foreground)" stopOpacity="0.06" />
          </radialGradient>
          <radialGradient id={`${id}-aura`}>
            <stop offset="0.6" stopColor="var(--interaction-accent)" stopOpacity="0.06" />
            <stop offset="1" stopColor="var(--interaction-accent)" stopOpacity="0" />
          </radialGradient>
        </defs>

        <circle cx="450" cy="390" r="340" fill={`url(#${id}-aura)`} />
        <g fill="var(--muted-foreground)" opacity="0.45">
          {stars.map(([x, y], index) => <circle key={index} cx={x} cy={y} r={index % 3 === 0 ? 1.8 : 1.1} />)}
        </g>
        <g stroke="var(--muted-foreground)" strokeWidth="1">
          <circle cx="450" cy="390" r="324" strokeDasharray="1 11" opacity="0.25" />
          <path d="M450 48V91 M450 689V732 M88 390H131 M769 390H812" opacity="0.22" />
        </g>

        {/* The opaque Earth occludes the far half of every orbit and satellite. */}
        {renderSatellites("far")}

        <circle cx="450" cy="390" r="250" fill={`url(#${id}-ocean)`} stroke="var(--muted-foreground)" strokeOpacity="0.4" />
        <path ref={gridRef} d={initialFrame.grid} stroke="var(--muted-foreground)" strokeOpacity="0.17" strokeWidth="0.8" />
        <path ref={landRef} data-globe-layer="land" d={initialFrame.land} fill="var(--muted-foreground)" fillOpacity="0.16" stroke="var(--foreground)" strokeOpacity="0.28" strokeWidth="0.8" strokeLinejoin="round" />
        <circle cx="450" cy="390" r="250" fill={`url(#${id}-shade)`} />
        <path ref={connectionsRef} d={initialFrame.connections} stroke="var(--interaction-accent)" strokeOpacity="0.45" strokeWidth="1.1" />

        {initialFrame.nodes.map((node, index) => (
          <g key={index} ref={element => { nodesRef.current[index] = element }} transform={node.transform} opacity={node.opacity}>
            <circle r="8" fill="var(--background)" stroke="var(--interaction-accent)" strokeOpacity="0.35" />
            <circle r="3" fill="var(--interaction-accent)" opacity="0.8" />
          </g>
        ))}

        {renderSatellites("near")}
        <g stroke="var(--muted-foreground)" opacity="0.45">
          <path d="M690 126V138 M684 132H696 M211 564V572 M207 568H215" />
          <path d="M424 719H476 M438 725H462" opacity="0.5" />
        </g>
      </svg>
    </div>
  )
}
