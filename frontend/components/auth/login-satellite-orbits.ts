// Decorative circular orbits projected into the globe's SVG coordinate space.
// Every radius is outside the Earth (250); positive depth is in front of it.
const TAU = Math.PI * 2

const orbitDefinitions = [
  { radius: 294, flattening: 0.36, tilt: -28, period: 24, phase: 0, direction: 1 },
  { radius: 318, flattening: 0.52, tilt: 36, period: 30, phase: 0.7, direction: -1 },
  { radius: 340, flattening: 0.65, tilt: 79, period: 36, phase: 1.3, direction: 1 },
  { radius: 352, flattening: 0.4, tilt: -64, period: 42, phase: 0.3, direction: 1 },
  { radius: 310, flattening: 0.77, tilt: 8, period: 28, phase: 1.7, direction: -1 },
  { radius: 334, flattening: 0.86, tilt: 138, period: 38, phase: 2.3, direction: 1 },
]

type Orbit = typeof orbitDefinitions[number]

function project(orbit: Orbit, phase: number) {
  const tilt = orbit.tilt * Math.PI / 180
  const x = orbit.radius * Math.cos(phase)
  const y = orbit.radius * orbit.flattening * Math.sin(phase)
  const dx = -orbit.direction * Math.sin(phase)
  const dy = orbit.direction * orbit.flattening * Math.cos(phase)

  return {
    x: 450 + x * Math.cos(tilt) - y * Math.sin(tilt),
    y: 390 + x * Math.sin(tilt) + y * Math.cos(tilt),
    depth: Math.sqrt(1 - orbit.flattening ** 2) * Math.sin(phase),
    heading: Math.atan2(dx * Math.sin(tilt) + dy * Math.cos(tilt), dx * Math.cos(tilt) - dy * Math.sin(tilt)) * 180 / Math.PI + 90,
  }
}

function orbitPath(orbit: Orbit, start: number, end: number, segments: number) {
  return Array.from({ length: segments + 1 }, (_, index) => {
    const point = project(orbit, start + (end - start) * index / segments)
    return `${index === 0 ? "M" : "L"}${point.x.toFixed(2)} ${point.y.toFixed(2)}`
  }).join(" ")
}

export const satelliteOrbits = orbitDefinitions.map((orbit, index) => ({
  near: orbitPath(orbit, 0, Math.PI, 90),
  far: orbitPath(orbit, Math.PI, TAU, 90),
  accent: index === 0 || index === 3,
}))

export function getSatelliteFrames(elapsedMilliseconds: number) {
  return orbitDefinitions.flatMap((orbit, orbitIndex) => Array.from({ length: 3 }, (_, index) => {
    const phase = orbit.phase + index * TAU / 3 + orbit.direction * (elapsedMilliseconds % (orbit.period * 1000)) / (orbit.period * 1000) * TAU
    const point = project(orbit, phase)
    const opacity = 0.78 + point.depth * 0.14
    return {
      transform: `translate(${point.x.toFixed(2)} ${point.y.toFixed(2)}) rotate(${point.heading.toFixed(2)}) scale(${(0.86 + point.depth * 0.14).toFixed(3)})`,
      nearOpacity: point.depth >= 0 ? opacity : 0,
      farOpacity: point.depth < 0 ? opacity : 0,
      trail: orbitPath(orbit, phase - orbit.direction * 0.18, phase, 8),
      accent: satelliteOrbits[orbitIndex].accent,
    }
  }))
}
