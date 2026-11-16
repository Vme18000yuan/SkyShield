"use client"

import * as React from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { useLocale, useTranslations } from "next-intl"
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip as ChartTooltip, XAxis, YAxis } from "recharts"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { TianqiongMark } from "@/components/brand/tianqiong-mark"
import { semanticIcons } from "@/components/icons"
import { ContentHandoff } from "@/components/shared/loading/content-handoff"
import { useAssetMonitor } from "@/hooks/use-asset-monitor"
import { useAssetMonitorTheme } from "@/hooks/use-asset-monitor-theme"
import { parseMonitorSelection } from "@/lib/asset-monitor-selection"
import { isMonitorTheme } from "@/lib/asset-monitor-theme"
import { assetDisplayType } from "@/lib/asset-display-type"
import { textRole } from "@/lib/typography"
import { resolveMonitorLocations, type MonitorMapPoint } from "@/lib/asset-monitor-geography"
import type { AssetMonitorSnapshot, MonitorCount } from "@/types/asset-monitor.types"
import type { AssetDisplayType, AssetEntityType } from "@/types/asset-entity.types"
import { AssetDistributionMap } from "./asset-distribution-map"
import "./monitor.css"

const TYPE_KEYS = ["root_domain", "subdomain", "ip", "web", "service", "endpoint", "app"] as const
const SEVERITY_KEYS = ["critical", "high", "medium", "low", "info", "unknown"] as const
const SEVERITY_COLORS = SEVERITY_KEYS.map(key => `var(--monitor-severity-${key})`)

function MonitorPanel({ title, meta, children, className = "" }: { title: string; meta?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return <section className={`monitor-panel ${className}`}><header className="monitor-panel-heading"><h2 className={textRole.panelTitle}>{title}</h2>{meta}</header>{children}</section>
}

function CountBars({ items, title, label, countLabel, empty }: { items: MonitorCount[]; title: string; label?: (key: string) => string; countLabel?: (count: number) => string; empty: string }) {
  const locale = useLocale()
  const viewportRef = React.useRef<HTMLDivElement>(null)
  const rowsRef = React.useRef<HTMLDivElement>(null)
  const [scrolling, setScrolling] = React.useState(false)
  const visibleItems = items.slice(0, 40)
  const maximum = Math.max(1, ...visibleItems.map(item => item.count))

  React.useEffect(() => {
    const viewport = viewportRef.current, rows = rowsRef.current
    if (!viewport || !rows) return
    const measure = () => setScrolling(rows.scrollHeight > viewport.clientHeight + 1)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(viewport)
    observer.observe(rows)
    return () => observer.disconnect()
  }, [items])

  if (!visibleItems.length) return <p className="monitor-empty">{empty}</p>
  const rows = (copy: boolean) => <div className="monitor-bars" ref={copy ? undefined : rowsRef} aria-hidden={copy || undefined}>{visibleItems.map((item, index) => <div className="monitor-bar" key={item.key}>
    <div className="monitor-bar-label"><span className="monitor-rank">{String(index + 1).padStart(2, "0")}</span><span title={label?.(item.key) ?? item.key}>{label?.(item.key) ?? item.key}</span><strong>{countLabel?.(item.count) ?? item.count.toLocaleString(locale)}</strong></div>
    <div className="monitor-bar-track"><i style={{ width: `${item.count / maximum * 100}%` }} /></div>
  </div>)}</div>
  return <div ref={viewportRef} className="monitor-bars-viewport" role="region" tabIndex={scrolling ? 0 : undefined} aria-label={title}><div className={`monitor-bars-track${scrolling ? " monitor-bars-track-animated" : ""}`} style={{ animationDuration: `${Math.max(18, visibleItems.length * 2.6)}s` }}>{rows(false)}{scrolling && rows(true)}</div></div>
}

function AssetTypes({ data }: { data: AssetMonitorSnapshot }) {
  const t = useTranslations("assetMonitor"), locale = useLocale()
  const hasInventory = Array.isArray(data.inventoryCategories)
  const hasCategories = Array.isArray(data.assetCategories)
  const counts = new Map((data.inventoryCategories ?? data.assetCategories ?? data.assetTypes).map(item => [item.key, item.count]))
  const keys = hasInventory ? (["subdomain", "ip", "service"] as const) : TYPE_KEYS.filter(key => hasCategories ? key !== "endpoint" || (counts.get(key) ?? 0) > 0 : key !== "web")
  return <div className="monitor-type-list">{keys.map(key => {
    const count = counts.get(key) ?? 0
    const share = data.summary.totalAssets ? count / data.summary.totalAssets * 100 : 0
    const label = key === "service" && hasInventory ? t("types.inventory_service") : key === "service" && !hasCategories ? t("types.legacy_service") : t(`types.${key}`)
    return <div className="monitor-type" key={key}><div className="monitor-type-main"><div><span>{label}</span><strong>{count.toLocaleString(locale)}</strong></div><div className="monitor-type-meter"><i style={{ width: `${Math.min(100, share)}%` }} /></div></div><span className="monitor-type-share">{share.toFixed(1)}%</span></div>
  })}</div>
}

function RecentAssets({ data }: { data: AssetMonitorSnapshot }) {
  const t = useTranslations("assetMonitor"), locale = useLocale()
  const items = data.recentAssets
  const viewportRef = React.useRef<HTMLDivElement>(null)
  const rowsRef = React.useRef<HTMLDivElement>(null)
  const [scrolling, setScrolling] = React.useState(false)

  React.useEffect(() => {
    const viewport = viewportRef.current, rows = rowsRef.current
    if (!viewport || !rows) return
    const measure = () => setScrolling(rows.scrollHeight > viewport.clientHeight + 1)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(viewport)
    observer.observe(rows)
    return () => observer.disconnect()
  }, [items])

  if (!items.length) return <p className="monitor-empty">{t("noAssets")}</p>
  const rows = (copy: boolean) => <div ref={copy ? undefined : rowsRef} aria-hidden={copy || undefined}>{items.map(item => {
    const displayType = assetDisplayType({ assetType: item.assetType as AssetEntityType, label: item.label, displayType: item.displayType as AssetDisplayType | undefined })
    const label = data.inventoryCategories && displayType === "service" ? t("types.inventory_service") : TYPE_KEYS.includes(displayType as typeof TYPE_KEYS[number]) ? t(`types.${displayType}`) : displayType
    return <div className="monitor-recent-row" key={`${copy ? "copy-" : ""}${item.id}`}><span className="monitor-asset-tag">{label}</span><span className="monitor-recent-label" title={item.label}>{item.label}</span><time dateTime={item.firstSeenAt}>{new Date(item.firstSeenAt).toLocaleString(locale, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false })}</time></div>
  })}</div>
  return <><div className="monitor-recent-columns" aria-hidden="true"><span>{t("assetTypeColumn")}</span><span>{t("assetColumn")}</span><span>{t("firstSeenColumn")}</span></div><div ref={viewportRef} className="monitor-recent-list" tabIndex={scrolling ? 0 : undefined} aria-label={t("recentDiscoveries")}><div className={`monitor-recent-track${scrolling ? " monitor-recent-track-animated" : ""}`} style={{ animationDuration: `${Math.max(18, items.length * 2.6)}s` }}>{rows(false)}{scrolling && rows(true)}</div></div></>
}

function RiskDistribution({ data }: { data: AssetMonitorSnapshot }) {
  const t = useTranslations("assetMonitor"), locale = useLocale()
  const counts = SEVERITY_KEYS.map(key => ({ key, count: data.severities.filter(item => item.key === key).reduce((sum, item) => sum + item.count, 0) }))
  const known = new Set<string>(SEVERITY_KEYS)
  counts[counts.length - 1].count += data.severities.filter(item => !known.has(item.key)).reduce((sum, item) => sum + item.count, 0)
  const priority = counts[0].count + counts[1].count
  return <div className="monitor-risk">
    <div className="monitor-risk-priority"><strong data-elevated={priority > 0}>{priority.toLocaleString(locale)}</strong><span>{t("priorityRisks")}</span></div>
    <div className="monitor-risk-scale" role="img" aria-label={t("riskTotal", { count: data.summary.risks })}>{counts.map((item, index) => <i key={item.key} style={{ width: `${data.summary.risks ? item.count / data.summary.risks * 100 : 0}%`, background: SEVERITY_COLORS[index] }} />)}</div>
    <div className="monitor-risk-legend">{counts.map((item, index) => <div key={item.key}><i style={{ background: SEVERITY_COLORS[index] }} /><span>{t(`severity.${item.key}`)}</span><strong>{item.count.toLocaleString(locale)}</strong></div>)}</div>
  </div>
}

function DiscoveryTrends({ data }: { data: AssetMonitorSnapshot }) {
  const t = useTranslations("assetMonitor"), locale = useLocale(), id = React.useId()
  if (!data.trend.length) return <p className="monitor-empty">{t("noTrend")}</p>
  return <div className="monitor-trend-pair">{(["assets", "risks"] as const).map(key => {
    const label = t(key === "assets" ? "newAssets" : "newRisks")
    const color = key === "assets" ? "var(--monitor-cyan)" : "var(--monitor-gold)"
    const total = data.trend.reduce((sum, day) => sum + day[key], 0)
    return <div className="monitor-trend-series" key={key}>
      <div className="monitor-trend-summary"><span><i className={`monitor-dot${key === "risks" ? " monitor-dot-gold" : ""}`} />{label}</span><div><strong>{total.toLocaleString(locale)}</strong><span>{t("periodTotal")}</span></div></div>
      <div className="monitor-trend" role="img" aria-label={label}><ResponsiveContainer width="100%" height="100%"><AreaChart data={data.trend} margin={{ top: 12, right: 6, bottom: 0, left: -22 }}>
        <defs><linearGradient id={`${id}-${key}`} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor={color} stopOpacity={0.12} /><stop offset="1" stopColor={color} stopOpacity={0} /></linearGradient></defs>
        <CartesianGrid stroke="var(--monitor-line)" vertical={false} />
        <XAxis dataKey="date" tickFormatter={value => String(value).slice(5)} tick={{ fill: "var(--monitor-muted)", fontSize: 11 }} axisLine={false} tickLine={false} minTickGap={22} />
        <YAxis allowDecimals={false} tickCount={3} tick={{ fill: "var(--monitor-muted)", fontSize: 11 }} axisLine={false} tickLine={false} />
        <ChartTooltip contentStyle={{ background: "var(--monitor-surface)", border: "1px solid var(--monitor-border)", borderRadius: 4, color: "var(--monitor-text)", fontSize: 12 }} />
        <Area type="linear" dataKey={key} name={label} stroke={color} strokeWidth={1.7} fill={`url(#${id}-${key})`} isAnimationActive={false} />
      </AreaChart></ResponsiveContainer></div>
    </div>
  })}</div>
}

const ScreenData = React.memo(function ScreenData({ data }: { data: AssetMonitorSnapshot }) {
  const t = useTranslations("assetMonitor"), locale = useLocale()
  const points = React.useMemo(() => resolveMonitorLocations(data.locations, locale), [data.locations, locale])
  const [selectedKey, setSelectedKey] = React.useState<string | null>(null)
  const selected: MonitorMapPoint | undefined = points.find(point => point.key === selectedKey) ?? points[0]
  const mapped = points.reduce((sum, point) => sum + point.count, 0)
  const domainCount = (data.inventoryCategories ?? data.assetCategories ?? data.assetTypes).find(item => item.key === "subdomain")?.count ?? 0
  const kpis = [
    { label: t("totalAssets"), value: data.summary.totalAssets, sub: t("new24h", { count: data.summary.newAssets24h }) },
    { label: t("monitoredTargets"), value: data.summary.targets, sub: t("selectedOrganizationCount", { count: data.organizations.length }) },
    { label: t("ipAssets"), value: data.summary.ipAssets, sub: null },
    { label: t("domains"), value: domainCount, sub: t("domainCountHint") },
    { label: t("riskFindings"), value: data.summary.risks, sub: t("newRisk24h", { count: data.summary.newRisks24h }) },
    { label: t("scanning"), value: data.summary.runningScans, sub: t("pendingScans", { count: data.summary.pendingScans }) },
  ]
  return <>
    <div className="monitor-kpis">{kpis.map((item, index) => <div className="monitor-kpi" data-featured={index === 0} key={item.label}><span className="monitor-kpi-label">{item.label}</span><strong>{typeof item.value === "number" ? item.value.toLocaleString(locale) : item.value}</strong>{item.sub && <span className="monitor-kpi-sub">{item.sub}</span>}</div>)}</div>
    <div className="monitor-main-grid">
      <div className="monitor-side-stack monitor-side-stack-left">
        <MonitorPanel title={t("assetComposition")}><AssetTypes data={data} /></MonitorPanel>
        <MonitorPanel title={t("exposedPorts")}><CountBars items={data.ports} title={t("exposedPorts")} label={port => t("portLabel", { port })} countLabel={count => t("portCount", { count: count.toLocaleString(locale) })} empty={t("noPorts")} /></MonitorPanel>
      </div>
      <MonitorPanel title={t("mapTitle")} className="monitor-map-panel" meta={<div className="monitor-map-metric"><strong>{mapped.toLocaleString(locale)}</strong><span>{t("mappedAssets")}</span></div>}>
        <AssetDistributionMap points={points} selected={selected?.key ?? null} onSelect={point => setSelectedKey(point.key)} />
        <div className="monitor-map-readout">{selected ? <><div><span>{t("selectedRegion")}</span><strong>{selected.regionName}</strong></div><div><span>{t(selected.precision === "country" ? "countryPrecision" : selected.precision === "dns" ? "dnsPrecision" : "ipPrecision")}{selected.cityEstimated ? ` · ${t("cityNearCoordinate")}` : selected.cityTranslationUnavailable ? ` · ${t("cityTranslationUnavailable")}` : selected.precision !== "country" && !selected.cityName ? ` · ${t("cityUnavailable")}` : ""}{selected.expired ? ` · ${t("expiredLocation")}` : ""}</span><strong className="monitor-cyan">{selected.count.toLocaleString(locale)} <small>{t("assetsUnit")}</small></strong></div></> : <p>{t("noLocationEvidence")}</p>}</div>
      </MonitorPanel>
      <div className="monitor-side-stack monitor-side-stack-right"><MonitorPanel title={t("riskDistribution")}><RiskDistribution data={data} /></MonitorPanel><MonitorPanel title={t("technologyProfile")}><CountBars items={data.technologies} title={t("technologyProfile")} empty={t("noTechnologies")} /></MonitorPanel></div>
    </div>
    <div className="monitor-bottom-grid">
      <MonitorPanel title={t("discoveryTrend")} meta={<span className="monitor-panel-note">{t("beijingTime")}</span>}>
        <DiscoveryTrends data={data} />
      </MonitorPanel>
      <MonitorPanel title={t("recentDiscoveries")} meta={<span className="monitor-panel-note">{t("latestCount", { count: data.recentAssets.length })}</span>}>
        <RecentAssets data={data} />
      </MonitorPanel>
    </div>
  </>
})

export function MonitorScreenLoading() {
  const t = useTranslations("assetMonitor")
  return <div className="monitor-root monitor-loading" data-loading-owner="asset-monitor-screen" role="status"><TianqiongMark size={32} decorative /><p className={textRole.bodySubtle}>{t("loading")}</p></div>
}

export function AssetMonitorScreen() {
  const t = useTranslations("assetMonitor"), locale = useLocale(), params = useSearchParams()
  const selection = React.useMemo(() => parseMonitorSelection(params.get("organizations")), [params])
  const [autoRefresh, setAutoRefresh] = React.useState(true)
  const [now, setNow] = React.useState<Date | null>(null)
  const [fullscreen, setFullscreen] = React.useState(false)
  const [storedTheme] = useAssetMonitorTheme()
  const requestedTheme = params.get("theme")
  const theme = isMonitorTheme(requestedTheme) ? requestedTheme : storedTheme
  const root = React.useRef<HTMLDivElement>(null)
  const snapshot = useAssetMonitor(selection, autoRefresh)
  React.useEffect(() => { setNow(new Date()); const timer = window.setInterval(() => setNow(new Date()), 1000); return () => window.clearInterval(timer) }, [])
  React.useEffect(() => { const change = () => setFullscreen(Boolean(document.fullscreenElement)); document.addEventListener("fullscreenchange", change); return () => document.removeEventListener("fullscreenchange", change) }, [])
  const toggleFullscreen = async () => { try { if (document.fullscreenElement) await document.exitFullscreen(); else if (root.current?.requestFullscreen) await root.current.requestFullscreen(); else toast.error(t("fullscreenUnavailable")) } catch { toast.error(t("fullscreenUnavailable")) } }
  const RefreshIcon = semanticIcons.action.refresh
  return <div ref={root} className="monitor-root" id="main-content" tabIndex={0} aria-label={t("title")} data-slot="asset-monitor-screen" data-monitor-theme={theme}>
    <header className="monitor-header"><div className="monitor-brand"><TianqiongMark size={32} decorative /><strong className={textRole.panelTitle}>{t("brand")}</strong><div className="monitor-heading"><h1 className={textRole.pageTitle}>{t("title")}</h1></div></div><div className="monitor-header-actions"><div className="monitor-clock"><strong suppressHydrationWarning>{now?.toLocaleTimeString(locale, { hour12: false }) ?? "--:--:--"}</strong><span suppressHydrationWarning>{now?.toLocaleDateString(locale, { year: "numeric", month: "2-digit", day: "2-digit", weekday: "short" }) ?? "—"}</span></div><Button size="sm" variant="outline" onClick={() => void toggleFullscreen()}>{t(fullscreen ? "exitFullscreen" : "fullscreen")}</Button></div></header>
    <div className="monitor-toolbar"><div className="monitor-refresh-status" role="status" data-stale={Boolean(snapshot.error && snapshot.data)}>{snapshot.error && snapshot.data && <><span className="monitor-dot monitor-dot-gold" /><span>{t("staleData")}</span></>}</div><div className="monitor-toolbar-controls"><Button size="sm" variant="ghost" aria-pressed={autoRefresh} onClick={() => setAutoRefresh(value => !value)}>{t(autoRefresh ? "pauseRefresh" : "resumeRefresh")}</Button><Button size="icon-sm" variant="ghost" aria-label={t("refresh")} disabled={snapshot.isFetching || !selection} loading={snapshot.isFetching} loadingLabel={<span className="sr-only">{t("refresh")}</span>} onClick={() => void snapshot.refetch()}><RefreshIcon className="size-4" /></Button><Button size="sm" variant="outline" render={<Link href="/asset-monitor/" />}>{t("changeOrganizations")}</Button></div></div>
    {!selection ? <div className="monitor-state"><h2>{t("invalidSelection")}</h2><p>{t("selectToStart")}</p><Button render={<Link href="/asset-monitor/" />}>{t("selectOrganizations")}</Button></div> : snapshot.error && !snapshot.data ? <div className="monitor-state" role="alert"><h2>{t("loadError")}</h2><p>{t("loadErrorHint")}</p><Button onClick={() => void snapshot.refetch()}>{t("retry")}</Button></div> : <ContentHandoff owner="asset-monitor-data" layer="workspace" className="monitor-handoff" contentClassName="monitor-content" isLoading={!snapshot.data} skeleton={<div className="monitor-data-skeleton" aria-label={t("loading")}><div className="monitor-kpis-skeleton" /><div className="monitor-main-skeleton" /><div className="monitor-bottom-skeleton" /></div>}>
      {snapshot.data && <ScreenData data={snapshot.data} />}
    </ContentHandoff>}
  </div>
}
