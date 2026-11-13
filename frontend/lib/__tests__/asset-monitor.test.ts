import { describe, expect, it } from "vitest"
import { geoEquirectangular, geoPath } from "d3-geo"
import { isAssetMonitorScreenPath, monitorScreenHref, parseMonitorSelection } from "@/lib/asset-monitor-selection"
import { MONITOR_MAP_PROJECTION, monitorGeography, projectMonitorPoint, resolveMonitorLocations, selectMonitorLabelKeys } from "@/lib/asset-monitor-geography"
import { DEFAULT_MONITOR_THEME, MONITOR_THEMES, isMonitorTheme, parseMonitorTheme } from "@/lib/asset-monitor-theme"
import { isPublicPathname } from "@/lib/auth-runtime.mjs"
import type { MonitorLocation } from "@/types/asset-monitor.types"

describe("asset monitor organization scope", () => {
  it("keeps a deterministic multi-organization URL", () => {
    expect(parseMonitorSelection("7,2,13")).toEqual([2, 7, 13])
    expect(monitorScreenHref([7, 2])).toBe("/asset-monitor/screen/?organizations=2,7")
    expect(monitorScreenHref([7, 2], "black")).toBe("/asset-monitor/screen/?organizations=2,7&theme=black")
  })
  it("rejects missing, repeated, malformed and oversized selections without a global fallback", () => {
    for (const input of [null, "", "1,1", "0", "-1", "1,", "1.5", "01", "2147483648", Array.from({ length: 51 }, (_, i) => i + 1).join(",")]) expect(parseMonitorSelection(input)).toBeNull()
    expect(isAssetMonitorScreenPath("/asset-monitor/screen/")).toBe(true)
    expect(isAssetMonitorScreenPath("/asset-monitor/")).toBe(false)
    expect(isAssetMonitorScreenPath("/asset-monitor/screen/fake")).toBe(false)
    expect(isPublicPathname("/asset-monitor/screen/")).toBe(false)
  })
})

describe("asset monitor geographic evidence", () => {
  const country: MonitorLocation = { key: "cn", latitude: null, longitude: null, countryCode: "CN", label: "China", count: 18, precision: "country", expired: false }
  it("positions recorded country attribution at a country centroid and keeps its precision", () => {
    const [point] = resolveMonitorLocations([country], "zh")
    expect(point.regionName).toBe("中国")
    expect(point.longitude).toBeGreaterThan(75)
    expect(point.longitude).toBeLessThan(135)
    expect(point.latitude).toBeGreaterThan(20)
    expect(point.latitude).toBeLessThan(55)
    expect(point.count).toBe(18)
    expect(point.precision).toBe("country")
  })
  it("uses measured public-IP coordinates without replacing them with a centroid", () => {
    const [point] = resolveMonitorLocations([{ ...country, key: "us-ip", precision: "ip", countryCode: "US", label: "", latitude: 37.4, longitude: -122.1, expired: true }], "zh")
    expect(point.regionName).toBe("美国")
    expect(point.longitude).toBe(-122.1)
    expect(point.expired).toBe(true)
  })
  it("does not invent coordinates for unknown or invalid locations", () => {
    expect(resolveMonitorLocations([{ ...country, countryCode: "XX" }, { ...country, precision: "ip", latitude: 95, longitude: 10 }], "zh")).toEqual([])
  })

  it("retains DNS-estimate precision when resolving geographic points", () => {
    const [point] = resolveMonitorLocations([{ ...country, precision: "dns", countryCode: "US", label: "", latitude: 37.4, longitude: -122.1 }], "zh")
    expect(point.precision).toBe("dns")
    expect(point.latitude).toBe(37.4)
  })
  it("supports small country and territory geometry", () => {
    expect(resolveMonitorLocations([{ ...country, countryCode: "SG" }, { ...country, key: "hk", countryCode: "HK" }], "zh")).toHaveLength(2)
  })

  it("localizes provider cities without exposing English region names", () => {
    const input = { ...country, precision: "ip" as const, countryCode: "US", latitude: 37.33, longitude: -121.89, regionName: "California", cityName: "San Jose" }
    const [city] = resolveMonitorLocations([input], "zh")
    expect(city.regionName).toBe("美国 · 圣何塞")
    expect(city.cityEstimated).toBe(false)
    const [english] = resolveMonitorLocations([input], "en")
    expect(english.regionName).toBe("United States · California · San Jose")
  })

  it("distinguishes cities with the same English name using country and coordinates", () => {
    const [california, argentina] = resolveMonitorLocations([
      { ...country, precision: "ip", countryCode: "US", latitude: 37.7749, longitude: -122.4194, cityName: "San Francisco" },
      { ...country, precision: "ip", countryCode: "AR", latitude: -31.428, longitude: -62.082, cityName: "San Francisco" },
    ], "zh")
    expect(california.cityName).toBe("旧金山")
    expect(argentina.cityName).toBe("圣弗朗西斯科")
    const [shenzhen, shenzhenTown] = resolveMonitorLocations([
      { ...country, precision: "ip", latitude: 22.54554, longitude: 114.0683, cityName: "Shenzhen" },
      { ...country, precision: "ip", latitude: 22.1823, longitude: 111.11793, cityName: "Shenzhen" },
    ], "zh")
    expect(shenzhen.cityName).toBe("深圳")
    expect(shenzhenTown.cityName).toBe("深镇")
  })

  it("converts Chinese labels and keeps unknown translations explicit", () => {
    const [traditional, unknown] = resolveMonitorLocations([
      { ...country, precision: "ip", countryCode: "TW", latitude: 25.03, longitude: 121.56, cityName: "臺北", regionName: "臺灣" },
      { ...country, precision: "ip", countryCode: "CN", latitude: 35, longitude: 110, cityName: "Unknown City XYZ", regionName: "Unknown Region" },
    ], "zh")
    expect(traditional.regionName).toContain("台北")
    expect(traditional.regionName).not.toMatch(/[A-Za-z]/)
    expect(unknown.regionName).toBe("中国")
    expect(unknown.cityTranslationUnavailable).toBe(true)
  })

  it("infers a nearby city conservatively and never invents one for country-only evidence", () => {
    const [nearby, unknown, countryOnly] = resolveMonitorLocations([
      { ...country, precision: "ip", latitude: 39.9075, longitude: 116.39723, label: "" },
      { ...country, precision: "ip", latitude: 35, longitude: 110, label: "" },
      { ...country, cityName: "Beijing" },
    ], "zh")
    expect(nearby.cityName).toBe("北京")
    expect(nearby.cityEstimated).toBe(true)
    expect(unknown.cityName).toBe("")
    expect(unknown.cityEstimated).toBe(false)
    expect(countryOnly.cityName).toBe("")
    expect(countryOnly.regionName).toBe("中国")
  })

  it("does not treat a city neighborhood as a separate city", () => {
    const [point] = resolveMonitorLocations([{ ...country, precision: "ip", countryCode: "US", latitude: 40.71649, longitude: -73.99625, label: "" }], "zh")
    expect(point.cityName).toBe("纽约")
    expect(point.cityEstimated).toBe(true)
  })
})

describe("asset monitor display", () => {
  it("fits the world within the map and places Asia and the Americas across the Pacific", () => {
    const projection = geoEquirectangular().rotate(MONITOR_MAP_PROJECTION.rotate).center(MONITOR_MAP_PROJECTION.center).scale(MONITOR_MAP_PROJECTION.scale).translate([500, 190])
    const [[left, top], [right, bottom]] = geoPath(projection).bounds(monitorGeography)
    expect(left).toBeGreaterThanOrEqual(0); expect(right).toBeLessThanOrEqual(1000)
    expect(top).toBeGreaterThanOrEqual(0); expect(bottom).toBeLessThanOrEqual(380)
    const europe = projection([15, 50])!, china = projection([105, 35])!, america = projection([-100, 40])!
    expect(europe[0]).toBeLessThan(china[0]); expect(china[0]).toBeLessThan(america[0])
    expect(china[0]).toBeGreaterThan(300); expect(china[0]).toBeLessThan(450)
  })
  it("supports all palettes and restores a safe default for unknown preferences", () => {
    expect(MONITOR_THEMES).toEqual(["gold", "ocean", "violet", "teal", "black"])
    for (const theme of MONITOR_THEMES) expect(parseMonitorTheme(theme)).toBe(theme)
    expect(isMonitorTheme("black")).toBe(true)
    expect(isMonitorTheme("invalid")).toBe(false)
    expect(parseMonitorTheme(null)).toBe(DEFAULT_MONITOR_THEME)
    expect(parseMonitorTheme("invalid")).toBe(DEFAULT_MONITOR_THEME)
  })
  it("keeps the selected location label when nearby labels overlap", () => {
    const base = { latitude: 39.9, longitude: 116.4, regionName: "北京", key: "a", label: "", count: 100, countryCode: "CN", precision: "ip" as const, expired: false }
    const nearby = { ...base, key: "b", count: 90 }
    const labels = selectMonitorLabelKeys([base, nearby], "b", "zh")
    expect(labels.has("b")).toBe(true)
    expect(labels.has("a")).toBe(false)
  })

  it("reveals nearby city labels after zooming and excludes off-screen labels", () => {
    const a = { latitude: 39.9, longitude: 116.4, regionName: "甲城", cityName: "甲城", key: "a", label: "", count: 100, countryCode: "CN", precision: "ip" as const, expired: false }
    const b = { ...a, longitude: 116.9, cityName: "乙城", key: "b", count: 90 }
    expect(selectMonitorLabelKeys([a, b], "a", "zh").has("b")).toBe(false)
    const [x, y] = projectMonitorPoint(a, { x: 0, y: 0, k: 1 })!
    const zoomed = { x: 500 - x * 32, y: 190 - y * 32, k: 32 }
    expect(selectMonitorLabelKeys([a, b], "a", "zh", zoomed)).toEqual(new Set(["a", "b"]))
    expect(selectMonitorLabelKeys([a, b], "a", "zh", { ...zoomed, x: zoomed.x + 2000 }).size).toBe(0)
  })
})
