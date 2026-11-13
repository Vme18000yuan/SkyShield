import { geoBounds, geoCentroid, geoContains, geoEquirectangular } from "d3-geo"
import { feature } from "topojson-client"
import type { Topology, GeometryCollection } from "topojson-specification"
import type { Feature, FeatureCollection, Geometry } from "geojson"
import atlas from "world-atlas/countries-50m.json"
import type { MonitorLocation } from "@/types/asset-monitor.types"
import { nearbyMonitorCity, simplifiedMonitorName, translateMonitorCity } from "@/lib/asset-monitor-cities"

type Country = Feature<Geometry, { name: string }>
const topology = atlas as unknown as Topology<{ countries: GeometryCollection<{ name: string }> }>
export const monitorCountries = (feature(topology, topology.objects.countries) as FeatureCollection<Geometry, { name: string }>).features
  .filter(country => country.properties.name !== "Antarctica")
export const monitorGeography: FeatureCollection<Geometry, { name: string }> = { type: "FeatureCollection", features: monitorCountries }
// Fits the world (excluding Antarctica) inside the 1000 × 380 view, with the
// Pacific between Asia and the Americas as in the display's world map.
export const MONITOR_MAP_PROJECTION = { rotate: [-160, 0, 0] as [number, number, number], center: [0, 12.55] as [number, number], scale: 145 }
export const MONITOR_MAP_CENTER: [number, number] = [160, 12.55]
export const MONITOR_MAP_MAX_ZOOM = 64
export interface MonitorMapTransform { x: number; y: number; k: number }
const monitorProjection = geoEquirectangular().rotate(MONITOR_MAP_PROJECTION.rotate).center(MONITOR_MAP_PROJECTION.center).scale(MONITOR_MAP_PROJECTION.scale).translate([500, 190])

export function projectMonitorPoint(point: MonitorMapPoint, transform: MonitorMapTransform) {
  const projected = monitorProjection([point.longitude, point.latitude])
  return projected ? [projected[0] * transform.k + transform.x, projected[1] * transform.k + transform.y] : null
}

export function monitorCityLabel(point: MonitorMapPoint) {
  const city = point.precision === "country" ? "" : point.cityName?.trim() ?? ""
  return city.length > 18 ? `${city.slice(0, 16)}…` : city
}

export function monitorMarkerRadius(count: number, maximum: number) {
  return 3 + Math.sqrt(count / Math.max(1, maximum)) * 9
}

const aliases: Record<string, string> = {
  US: "United States of America", GB: "United Kingdom", KR: "South Korea", KP: "North Korea",
  CI: "Côte d'Ivoire", CD: "Dem. Rep. Congo", CG: "Congo", CF: "Central African Rep.",
  DO: "Dominican Rep.", GQ: "Eq. Guinea", SS: "S. Sudan", BA: "Bosnia and Herz.",
  TL: "Timor-Leste", SZ: "eSwatini", SB: "Solomon Is.", FK: "Falkland Is.",
  HK: "Hong Kong", MO: "Macao",
}
const englishRegions = new Intl.DisplayNames(["en"], { type: "region" })
const codeToCountry = new Map<string, Country>()
const countryToCode = new Map<string, string>()
const countryBounds = monitorCountries.map(country => ({ country, bounds: geoBounds(country) }))
for (let a = 65; a <= 90; a++) for (let b = 65; b <= 90; b++) {
  const code = String.fromCharCode(a, b)
  const name = aliases[code] ?? englishRegions.of(code)
  const country = monitorCountries.find(item => item.properties.name === name)
  if (country) { codeToCountry.set(code, country); countryToCode.set(country.properties.name, code) }
}

export interface MonitorMapPoint extends MonitorLocation { latitude: number; longitude: number; regionName: string; cityEstimated?: boolean; cityTranslationUnavailable?: boolean }

// A crowded region can contain many real points; keep all markers interactive
// while avoiding unreadable, overlapping labels. The selected point wins.
export function selectMonitorLabelKeys(points: MonitorMapPoint[], selected: string | null, locale: string, transform: MonitorMapTransform = { x: 0, y: 0, k: 1 }): Set<string> {
  const ordered = [...points].sort((a, b) => b.count - a.count)
  const active = ordered.find(point => point.key === selected)
  const candidates = [...(active ? [active] : []), ...ordered.filter(point => point.key !== active?.key)]
  const maximum = Math.max(1, ...points.map(point => point.count))
  const boxes: Array<{ left: number; right: number; top: number; bottom: number }> = []
  const visible = new Set<string>()
  for (const point of candidates) {
    if (visible.size >= (transform.k >= 3 ? 60 : 10)) break
    const projected = projectMonitorPoint(point, transform)
    if (!projected) continue
    if (projected[0] < 0 || projected[0] > 1000 || projected[1] < 0 || projected[1] > 380) continue
    const radius = monitorMarkerRadius(point.count, maximum)
    const city = transform.k >= 3 || point.key === selected ? monitorCityLabel(point) : ""
    const cityWidth = Array.from(city).reduce((width, character) => width + (character.charCodeAt(0) > 255 ? 14 : 8), 0)
    const width = Math.max(16, point.count.toLocaleString(locale).length * 8, cityWidth)
    const box = { left: projected[0] - width / 2 - 3, right: projected[0] + width / 2 + 3, top: projected[1] - radius - (city ? 40 : 24), bottom: projected[1] - radius - 7 }
    if (boxes.some(other => box.left < other.right && box.right > other.left && box.top < other.bottom && box.bottom > other.top)) continue
    boxes.push(box); visible.add(point.key)
  }
  return visible
}

export function resolveMonitorLocations(locations: MonitorLocation[], locale: string): MonitorMapPoint[] {
  const regions = new Intl.DisplayNames([locale], { type: "region" })
  return locations.flatMap(location => {
    let latitude = location.latitude, longitude = location.longitude
    let country: Country | undefined
    if (location.precision === "country") {
      country = codeToCountry.get(location.countryCode)
      if (!country) return []
      ;[longitude, latitude] = geoCentroid(country)
    } else if (latitude !== null && longitude !== null && Number.isFinite(latitude) && Number.isFinite(longitude) && Math.abs(latitude) <= 90 && Math.abs(longitude) <= 180) {
      country = countryBounds.find(({ country: item, bounds: [[west, south], [east, north]] }) =>
        latitude! >= south && latitude! <= north && (west <= east ? longitude! >= west && longitude! <= east : longitude! >= west || longitude! <= east) && geoContains(item, [longitude!, latitude!]))?.country
    } else return []
    const code = country ? countryToCode.get(country.properties.name) : location.countryCode
    const displayCountry = code && regions.of(code)
    const chineseLocale = locale.startsWith("zh")
    const countryName = displayCountry && displayCountry !== code ? displayCountry : (!chineseLocale ? country?.properties.name : "")
    const providerCity = location.precision === "country" ? "" : location.cityName?.trim() ?? ""
    // A provider's country can be more specific than the bundled world polygon
    // (for example, small territories). Fall back to the polygon for old caches.
    const cityCountry = codeToCountry.has(location.countryCode) ? location.countryCode : code ?? ""
    let cityName = chineseLocale ? translateMonitorCity(providerCity, cityCountry, latitude!, longitude!) : providerCity
    let cityEstimated = false
    if (chineseLocale && location.precision !== "country" && !cityName) {
      cityName = nearbyMonitorCity(cityCountry, latitude!, longitude!)
      cityEstimated = Boolean(cityName)
    }
    const region = chineseLocale ? simplifiedMonitorName(location.regionName) : location.regionName?.trim()
    const cityRegion = cityName ? [...new Set([countryName, region, cityName].filter(Boolean))].join(" · ") : ""
    const fallbackLabel = !chineseLocale && location.precision !== "country" ? location.label.trim() : ""
    const regionName = cityRegion || fallbackLabel || countryName
    return [{ ...location, cityName, cityEstimated, cityTranslationUnavailable: chineseLocale && Boolean(providerCity) && !cityName, latitude: latitude!, longitude: longitude!, regionName: regionName || (chineseLocale ? "IP 定位区域" : "IP location") }]
  })
}
