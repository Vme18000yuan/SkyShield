"use client"

import * as React from "react"
import { useLocale, useTranslations } from "next-intl"
import { ComposableMap, Geographies, Geography, Marker, ZoomableGroup, useZoomPanContext } from "react-simple-maps"
import { Button } from "@/components/ui/button"
import { MONITOR_MAP_CENTER, MONITOR_MAP_MAX_ZOOM, MONITOR_MAP_PROJECTION, monitorGeography, monitorCityLabel, monitorMarkerRadius, projectMonitorPoint, selectMonitorLabelKeys, type MonitorMapPoint } from "@/lib/asset-monitor-geography"

const mapExtent: [[number, number], [number, number]] = [[0, 0], [1000, 380]]
const geographyStyle = { default: { outline: "none" }, hover: { outline: "none" }, pressed: { outline: "none" } }
const CountryLayer = React.memo(function CountryLayer() {
  return <Geographies geography={monitorGeography}>{({ geographies }) => geographies.map(country => <Geography key={country.rsmKey} geography={country} data-country={country.properties.name} tabIndex={-1} fill="var(--monitor-map-land)" stroke="var(--monitor-map-border)" strokeWidth={0.65} vectorEffect="non-scaling-stroke" style={geographyStyle} />)}</Geographies>
})

type MapProps = { points: MonitorMapPoint[]; selected: string | null; onSelect: (point: MonitorMapPoint) => void }

function AssetMarkers({ points, selected, onSelect }: MapProps) {
  const t = useTranslations("assetMonitor"), locale = useLocale()
  const { x, y, k } = useZoomPanContext()
  const transform = { x, y, k }
  const max = Math.max(1, ...points.map(point => point.count))
  const labels = selectMonitorLabelKeys(points, selected, locale, transform)
  return <>{[...points].sort((a, b) => b.count - a.count).map(point => {
    const active = selected === point.key
    const radius = monitorMarkerRadius(point.count, max)
    const precision = point.precision === "country" ? "countryPrecision" : point.precision === "dns" ? "dnsPrecision" : "ipPrecision"
    const color = point.precision === "country" ? "var(--monitor-gold)" : point.precision === "dns" ? "var(--monitor-dns)" : "var(--monitor-cyan)"
    const projected = projectMonitorPoint(point, transform)
    const visible = projected && projected[0] >= 0 && projected[0] <= 1000 && projected[1] >= 0 && projected[1] <= 380
    const city = k >= 3 || active ? monitorCityLabel(point) : ""
    const cityHint = point.cityEstimated ? ` · ${t("cityNearCoordinate")}` : point.cityTranslationUnavailable ? ` · ${t("cityTranslationUnavailable")}` : point.precision !== "country" && !point.cityName ? ` · ${t("cityUnavailable")}` : ""
    return <Marker key={point.key} coordinates={[point.longitude, point.latitude]}>
      <g className="monitor-map-marker" data-location-key={point.key} transform={`scale(${1 / k})`} tabIndex={visible ? 0 : -1} role="button" aria-pressed={active} aria-label={`${point.regionName}${cityHint} · ${point.count.toLocaleString(locale)} ${t("assetsUnit")}`} onMouseEnter={() => onSelect(point)} onFocus={() => onSelect(point)} onClick={() => onSelect(point)} onKeyDown={event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onSelect(point) } }}>
        <title>{point.regionName}{cityHint} · {point.count.toLocaleString(locale)} · {t(precision)}</title>
        <circle r={Math.max(15, radius + 8)} fill="transparent" />
        <circle r={radius + 6} fill={point.expired ? "var(--monitor-muted)" : color} opacity={active ? 0.23 : 0.08} />
        <circle r={radius} fill={color} fillOpacity={active ? 0.35 : 0.17} stroke={point.expired ? "var(--monitor-muted)" : color} strokeWidth={active ? 1.7 : 1} strokeDasharray={point.expired || point.precision === "dns" ? "3 2" : undefined} />
        <circle r={2.5} fill={color} />
        {labels.has(point.key) && <text y={-radius - (city ? 25 : 9)} textAnchor="middle" fill="var(--monitor-text)" fontSize={14} fontWeight={600} className="monitor-map-count">
          {city && <tspan x={0} data-map-city-label="true">{city}</tspan>}
          <tspan x={0} dy={city ? 16 : 0}>{point.count.toLocaleString(locale)}</tspan>
        </text>}
      </g>
    </Marker>
  })}</>
}

export function AssetDistributionMap(props: MapProps) {
  const t = useTranslations("assetMonitor")
  const [view, setView] = React.useState({ coordinates: MONITOR_MAP_CENTER, zoom: 1 })
  const onMoveEnd = React.useCallback((position: { coordinates: [number, number]; zoom: number }) => setView(position), [])
  const changeZoom = (factor: number) => setView(current => ({ ...current, zoom: Math.min(MONITOR_MAP_MAX_ZOOM, Math.max(1, current.zoom * factor)) }))

  return <div className="monitor-map" role="group" aria-label={t("mapTitle")} data-map-zoom={view.zoom.toFixed(2)}>
    <ComposableMap width={1000} height={380} projection="geoEquirectangular" projectionConfig={MONITOR_MAP_PROJECTION} className="monitor-map-svg">
      <ZoomableGroup center={view.coordinates} zoom={view.zoom} minZoom={1} maxZoom={MONITOR_MAP_MAX_ZOOM} translateExtent={mapExtent} onMoveEnd={onMoveEnd}>
        <CountryLayer />
        <AssetMarkers {...props} />
      </ZoomableGroup>
    </ComposableMap>
    <div className="monitor-map-controls" role="group" aria-label={t("mapControls")}>
      <Button size="icon-sm" variant="surface" aria-label={t("zoomIn")} disabled={view.zoom >= MONITOR_MAP_MAX_ZOOM} onClick={() => changeZoom(2)}>+</Button>
      <Button size="icon-sm" variant="surface" aria-label={t("zoomOut")} disabled={view.zoom <= 1} onClick={() => changeZoom(0.5)}>−</Button>
      <Button size="sm" variant="surface" onClick={() => setView({ coordinates: MONITOR_MAP_CENTER, zoom: 1 })}>{t("resetMap")}</Button>
      <span className="monitor-map-zoom">{view.zoom.toFixed(1)}×</span>
    </div>
    <details className="monitor-map-attribution"><summary aria-label={t("mapCityData")} title={t("mapCityData")}>ⓘ</summary><div><a href="https://www.geonames.org/" target="_blank" rel="noopener noreferrer">GeoNames</a> (<a href="https://creativecommons.org/licenses/by/4.0/" target="_blank" rel="noopener noreferrer">CC BY 4.0</a>) · {t("mapDataModified")} · <a href="https://www.naturalearthdata.com/" target="_blank" rel="noopener noreferrer">Natural Earth</a></div></details>
    {!props.points.length && <div className="monitor-map-empty"><p>{t("noLocations")}</p><span>{t("noLocationsHint")}</span></div>}
    <div className="monitor-map-legend"><span className="monitor-map-navigation">{t("mapNavigation")}</span><span><i className="monitor-dot" />{t("ipPrecision")}</span><span title={t("locationEvidenceHint")}><i className="monitor-dot monitor-dot-dns" />{t("dnsPrecision")}</span><span><i className="monitor-dot monitor-dot-gold" />{t("countryPrecision")}</span></div>
  </div>
}
