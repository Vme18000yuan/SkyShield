import OpenCC from "opencc-js/t2cn"
import cityData from "@/lib/asset-monitor-data/cities.zh.json"

type City = [country: string, latitude: number, longitude: number, chinese: string, names: string[]]
const cities = cityData as City[]
const convertToSimplified = OpenCC.Converter({ from: "tw", to: "cn" })
const byName = new Map<string, City[]>()
const byLatitude = new Map<string, City[]>()
let indexed = false

function normalizeName(name: string) {
  return name.normalize("NFKD").replace(/\p{Diacritic}/gu, "").toLowerCase().replace(/[^a-z0-9]/g, "")
}

function indexCities() {
  if (indexed) return
  indexed = true
  for (const city of cities) {
    const bucket = `${city[0]}:${Math.floor(city[1])}`
    const nearby = byLatitude.get(bucket) ?? []
    nearby.push(city); byLatitude.set(bucket, nearby)
    for (const name of new Set(city[4].map(normalizeName).filter(Boolean))) {
      const key = `${city[0]}:${name}`
      const matches = byName.get(key) ?? []
      matches.push(city); byName.set(key, matches)
    }
  }
}

function distanceKm(latitude: number, longitude: number, city: City) {
  const radians = Math.PI / 180
  const dLat = (city[1] - latitude) * radians, dLon = (city[2] - longitude) * radians
  const a = Math.sin(dLat / 2) ** 2 + Math.cos(latitude * radians) * Math.cos(city[1] * radians) * Math.sin(dLon / 2) ** 2
  return 12742 * Math.asin(Math.min(1, Math.sqrt(a)))
}

// Accept Chinese provider labels directly after script conversion, but do not
// expose an unverified English or mixed-script label as a Chinese city name.
export function simplifiedMonitorName(name: string | undefined) {
  const value = name?.trim() ?? ""
  return value && /[\u3400-\u9fff]/u.test(value) && !/[A-Za-z0-9]/.test(value) ? convertToSimplified(value) : ""
}

export function translateMonitorCity(name: string, country: string, latitude: number, longitude: number) {
  const chinese = simplifiedMonitorName(name)
  if (chinese) return chinese
  const normalized = normalizeName(name)
  if (!normalized || !country) return ""
  indexCities()
  let nearest: City | undefined, best = 120
  for (const city of byName.get(`${country}:${normalized}`) ?? []) {
    const distance = distanceKm(latitude, longitude, city)
    if (distance < best) { nearest = city; best = distance }
  }
  return nearest?.[3] ?? ""
}

// Coordinates supplied by an IP provider are estimates. A nearby city is a
// useful hint only when a same-country gazetteer entry is close and clearly
// nearer than differently named places; its inferred status must be shown.
export function nearbyMonitorCity(country: string, latitude: number, longitude: number) {
  if (!country) return ""
  indexCities()
  let nearest: City | undefined, best = 3
  const candidates: Array<[City, number]> = []
  for (let band = Math.floor(latitude) - 1; band <= Math.floor(latitude) + 1; band++) {
    for (const city of byLatitude.get(`${country}:${band}`) ?? []) {
      // All candidates relevant to the 3 km threshold and ambiguity margin.
      if (Math.abs(city[2] - longitude) > 1 && Math.abs(latitude) < 88) continue
      const distance = distanceKm(latitude, longitude, city)
      if (distance <= 6) candidates.push([city, distance])
      if (distance < best) { nearest = city; best = distance }
    }
  }
  if (!nearest) return ""
  if (candidates.some(([city, distance]) => city[3] !== nearest![3] && distance < Math.max(0.5, best * 2))) return ""
  return nearest[3]
}
