// A monochrome astronomical illustration. Geometry and periods are illustrative.
import { mkdir, writeFile } from "node:fs/promises"
import { fileURLToPath } from "node:url"
import path from "node:path"

const destination = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../public/images/brand")
const center = { x: 640, y: 450 }
const planets = [
  { key: "mercury", radius: 82, size: 4.5, period: 24, phase: 214, tilt: -15 },
  { key: "venus", radius: 128, size: 7, period: 39, phase: 32, tilt: -17 },
  { key: "earth", radius: 180, size: 8.5, period: 55, phase: 108, tilt: -18 },
  { key: "mars", radius: 234, size: 6, period: 74, phase: 306, tilt: -16 },
  { key: "jupiter", radius: 312, size: 22, period: 108, phase: 334, tilt: -19 },
  { key: "saturn", radius: 392, size: 17, period: 146, phase: 147, tilt: -20 },
  { key: "uranus", radius: 474, size: 13, period: 184, phase: 231, tilt: -16 },
  { key: "neptune", radius: 554, size: 12, period: 228, phase: 34, tilt: -18 },
]

const n = value => Number(value.toFixed(3))
const point = ({ radius, tilt = -18 }, degrees) => {
  const angle = degrees * Math.PI / 180, inclination = tilt * Math.PI / 180
  const x = radius * Math.cos(angle), y = radius * .4 * Math.sin(angle)
  return { x: n(center.x + x * Math.cos(inclination) - y * Math.sin(inclination)), y: n(center.y + x * Math.sin(inclination) + y * Math.cos(inclination)) }
}
const arc = (planet, start, end) => {
  const a = point(planet, start), b = point(planet, end)
  return `M ${a.x} ${a.y} A ${planet.radius} ${n(planet.radius * .4)} ${planet.tilt} 0 1 ${b.x} ${b.y}`
}
const orbit = planet => `${arc(planet, 0, 180)} ${arc(planet, 180, 360).replace(/^M [^A]+/, "")}`

function globe(planet, detail = "") {
  const { key, size } = planet
  return `<circle r="${size}" fill="url(#shade-${key})"/><g clip-path="url(#disc-${key})">${detail}</g><circle r="${size}" stroke="#707070" stroke-width=".65" stroke-opacity=".75"/>`
}

function body(planet) {
  const { key, size: r } = planet
  if (key === "mercury") return globe(planet, '<g fill="#5b5b5b" fill-opacity=".3"><circle cx="-1.4" cy="-.8" r="1.1"/><circle cx="1.9" cy="1.5" r=".65"/><circle cx="-1" cy="2" r=".5"/></g>')
  if (key === "venus") return globe(planet, '<g stroke="#f2f2f2" stroke-width="1.3" stroke-opacity=".35"><path d="M-9-4q7 4 17-1M-9-1q8 4 18-1M-8 3q7 3 15-1"/></g>')
  if (key === "earth") return globe(planet, '<g fill="#646464" fill-opacity=".55"><path d="m-6-6 4-1 2 2-1 3-3 1 1 3-2 4-1-3-1-2-1-3-1-1ZM3-6l4 2-1 3-2 1-1 5-2 2-1-4 1-3-1-3Z"/></g><path d="M-6-6q-6 8 2 13" stroke="#fafafa" stroke-width=".5" stroke-opacity=".35"/>')
  if (key === "mars") return globe(planet, '<path d="M-5-3q5 4 9 2L2 5l-4-1Z" fill="#636363" fill-opacity=".23"/><circle cx="2.3" cy="-2" r="1.1" fill="#686868" fill-opacity=".2"/><path d="M-3-5q3-2 5 0" stroke="#f5f5f5" stroke-width="1.3" stroke-opacity=".65"/>')
  if (key === "jupiter") {
    const bands = Array.from({ length: 9 }, (_, i) => {
      const y = -19 + i * 4.5
      return `<path d="M-26 ${n(y)}q24 ${i % 2 ? 5 : 2} 52 -1" stroke="${i % 2 ? "#6b6b6b" : "#e4e4e4"}" stroke-opacity="${i % 2 ? .32 : .4}" stroke-width="${i % 3 === 0 ? 3 : 1.6}"/>`
    }).join("")
    return globe(planet, `${bands}<ellipse cx="8" cy="7" rx="5.2" ry="2.8" fill="#787878" fill-opacity=".35"/><ellipse cx="8" cy="7" rx="3.2" ry="1.2" stroke="#dedede" stroke-width=".6" stroke-opacity=".55"/>`)
  }
  if (key === "saturn") {
    const back = '<g transform="rotate(-24)"><ellipse rx="36" ry="10.5" stroke="#a1a1a1" stroke-width="5"/><ellipse rx="32" ry="8.5" stroke="#777777" stroke-width="1" stroke-opacity=".65"/><ellipse rx="38.5" ry="11.5" stroke="#9d9d9d" stroke-width=".7" stroke-opacity=".6"/></g>'
    const front = '<g transform="rotate(-24)"><path d="M36 0A36 10.5 0 0 1-36 0" stroke="#a8a8a8" stroke-width="5"/><path d="M32 0A32 8.5 0 0 1-32 0" stroke="#767676" stroke-width="1" stroke-opacity=".65"/><path d="M38.5 0A38.5 11.5 0 0 1-38.5 0" stroke="#8e8e8e" stroke-width=".7" stroke-opacity=".6"/></g>'
    return back + globe(planet, '<g stroke="#808080" stroke-opacity=".2" stroke-width="2"><path d="M-19-6q19 4 38 0M-19 1q19 4 38 0M-19 8q19 4 38 0"/></g>') + front
  }
  if (key === "uranus") return globe(planet, '<path d="M-15 1q15 3 30-1" stroke="#dedede" stroke-width="1.5" stroke-opacity=".22"/>') + '<ellipse rx="18" ry="4" transform="rotate(68)" stroke="#999999" stroke-width=".65" stroke-opacity=".65"/>'
  return globe(planet, `<path d="M-${r + 2} -3q${r} 3 ${r * 2 + 4} -1M-${r + 2} 4q${r} 3 ${r * 2 + 4} -1" stroke="#d7d7d7" stroke-width="1.3" stroke-opacity=".25"/><ellipse cx="4" cy="2" rx="3.1" ry="1.4" fill="#4b4b4b" fill-opacity=".25"/>`)
}

function sun() {
  const hatching = Array.from({ length: 18 }, (_, i) => {
    const y = -36 + i * 4.2
    return `<path d="M-42 ${n(y)}q18 ${i % 2 ? -3 : 4} 38 1t46 -1"/>`
  }).join("")
  const grain = Array.from({ length: 120 }, (_, i) => {
    const radius = Math.sqrt((i + .5) / 120) * 38
    const angle = i * 2.399963
    return `<circle cx="${n(Math.cos(angle) * radius)}" cy="${n(Math.sin(angle) * radius)}" r="${i % 5 === 0 ? .85 : .4}"/>`
  }).join("")
  return `<g transform="translate(${center.x} ${center.y})"><circle r="40" fill="url(#shade-sun)"/><g clip-path="url(#disc-sun)"><g stroke="#787878" stroke-opacity=".16" stroke-width=".6">${hatching}</g><g fill="#6e6e6e" fill-opacity=".2">${grain}</g></g><circle r="40" stroke="#8b8b8b" stroke-width=".85"/><circle r="43" stroke="#b0b0b0" stroke-width=".6" stroke-opacity=".36"/></g>`
}

function render(animated) {
  const gradients = [{ key: "sun", size: 40 }, ...planets].map(({ key, size }) => {
    const colors = key === "sun" ? ["#f4f4f4", "#d3d3d3", "#a6a6a6", "#777777"] : key === "neptune" ? ["#d5d5d5", "#a9a9a9", "#747474", "#4f4f4f"] : ["#eeeeee", "#c4c4c4", "#898989", "#565656"]
    return `<radialGradient id="shade-${key}" cx="28%" cy="25%" r="82%"><stop stop-color="${colors[0]}"/><stop offset=".38" stop-color="${colors[1]}"/><stop offset=".8" stop-color="${colors[2]}"/><stop offset="1" stop-color="${colors[3]}"/></radialGradient><clipPath id="disc-${key}"><circle r="${size}"/></clipPath>`
  }).join("")
  const guides = planets.map(planet => `<path d="${orbit(planet)}" stroke="#999999" stroke-width=".85" stroke-opacity=".4"/><path d="${arc(planet, 0, 180)}" stroke="#898989" stroke-width="1.15" stroke-opacity=".65"/>`).join("")
  const belt = Array.from({ length: 150 }, (_, i) => {
    const p = point({ radius: 266 + (i * 31) % 23, tilt: -18 }, i * 137.508)
    return `<circle cx="${p.x}" cy="${p.y}" r="${i % 6 === 0 ? .85 : .45}"/>`
  }).join("")
  const bodies = planets.map(planet => {
    const p = point(planet, planet.phase)
    const transform = animated ? "" : ` transform="translate(${p.x} ${p.y})"`
    const motion = animated ? `<animateMotion dur="${planet.period}s" begin="-${n(planet.period * planet.phase / 360)}s" repeatCount="indefinite" path="${orbit(planet)}"/>` : ""
    return `<g data-planet="${planet.key}"${transform}>${motion}${body(planet)}</g>`
  }).join("")
  return `<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="900" viewBox="0 0 1280 900" fill="none">
  <title>太阳系轨道示意</title>
  <defs>${gradients}</defs>
  <g>${guides}</g>
  <g fill="#969696" fill-opacity=".36">${belt}</g>
  ${sun()}
  <g stroke-linecap="round" stroke-linejoin="round">${bodies}</g>
</svg>\n`
}

await mkdir(destination, { recursive: true })
await Promise.all([
  writeFile(path.join(destination, "tianqiong-solar-system-v2.svg"), render(true)),
  writeFile(path.join(destination, "tianqiong-solar-system-v2-static.svg"), render(false)),
])
console.log("Refined solar system SVGs rebuilt")
