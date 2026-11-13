import { describe, expect, it } from "vitest"
import { readFileSync } from "node:fs"
import path from "node:path"
import { TIANQIONG_MARK_DATA_URI } from "@/components/brand/tianqiong-mark-data"

const source = readFileSync(path.resolve(process.cwd(), "components/brand/tianqiong-mark.tsx"), "utf8")
const compatibility = readFileSync(path.resolve(process.cwd(), "components/brand/lunafox-mark.tsx"), "utf8")
const globals = readFileSync(path.resolve(process.cwd(), "app/globals.css"), "utf8")
const themeBase = readFileSync(path.resolve(process.cwd(), "styles/themes/base.css"), "utf8")

describe("Tianqiong mark contract", () => {
  it("owns the shared brand mark and favicon svg helper", () => {
    expect(source).toContain("export function TianqiongMark")
    expect(source).toContain("export function getTianqiongFaviconSvg")
    expect(source).toContain("export async function getTianqiongFaviconPngDataUrl")
    expect(source).toContain("export async function getTianqiongBrandedFaviconPngDataUrl")
    expect(source).toContain("TIANQIONG_MARK_DATA_URI")
    expect(source).toContain("background: string")
    expect(source).toContain("foreground: string")
    expect(source).toContain('data-slot="tianqiong-mark"')
    expect(source).toContain('mask="url(#tianqiong-mask)"')
    expect(source).toContain('href="${TIANQIONG_MARK_DATA_URI}"')
    expect(compatibility).toContain("TianqiongMark as LunaFoxMark")
  })

  it("renders the mark through shared mask styling and theme tokens", () => {
    expect(globals).toContain(".tianqiong-mark")
    expect(globals).toContain('mask-image: url("/images/brand/tianqiong-mark.svg")')
    expect(globals).toContain("background-color: var(--brand-mark-foreground);")
    expect(themeBase).toContain("--brand-mark-foreground")
  })

  it("derives the embedded favicon mask from the shared vector source", () => {
    const vector = readFileSync(path.resolve(process.cwd(), "public/images/brand/tianqiong-mark.svg"), "utf8")
    const embedded = decodeURIComponent(TIANQIONG_MARK_DATA_URI.replace("data:image/svg+xml,", ""))
    expect(embedded).toBe(vector.replaceAll('fill="#000"', 'fill="#fff"'))
  })
})
