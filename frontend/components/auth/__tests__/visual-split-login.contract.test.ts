import { describe, expect, it } from "vitest"
import { readFileSync } from "node:fs"
import path from "node:path"

const source = readFileSync(path.resolve(process.cwd(), "components/auth/visual-split-login.tsx"), "utf8")

describe("product login contract", () => {
  it("keeps the password auth path and shares the product UI", () => {
    expect(source).toContain("export function VisualSplitLogin")
    expect(source).toContain("translations: VisualSplitLoginTranslations")
    expect(source).toContain('data-login-visual-kind="builtIn"')
    expect(source).toContain("<Card")
    expect(source).toContain("<Input")
    expect(source).toContain("<Button")
    expect(source).toContain("textRole")
    expect(source).toContain("requestAnimationFrame(markVisualReady)")
    expect(source).toContain("usePublicLoginVisual")
    expect(source).toContain("PublishedLoginVisual")
    expect(source).toContain("onError={handleFailure}")
    expect(source).toContain("<TianqiongMark")
    expect(source).toContain("aria-label={t.title}")
    expect(source).not.toContain("TIANQIONG")
    expect(source).not.toContain("NEXT_PUBLIC_IMAGE_TAG")
    expect(source).not.toContain("CelestialOrbits")
    expect(source).not.toContain("t.tagline")
    expect(source).not.toContain("t.description")
    expect(source).toContain("{t.usernameLabel}")
    expect(source).toContain("{t.accessHelp}")
    expect(source).not.toContain("FaultyTerminal")
    expect(source).not.toContain("tianqiong-nebula.webp")
  })

  it("preserves custom media, contrast, preview, and reduced motion", () => {
    expect(source).toContain("(prefers-reduced-motion: reduce)")
    expect(source).toContain('visual?.kind === "video" && reducedMotion ? visual.posterUrl')
    expect(source).toContain("onCanPlay={handleReady}")
    expect(source).toContain("product-login__preview-control")
    expect(source).toContain("bg-background/85")
    expect(source).toContain("motion-reduce:transition-none")
  })

  it("does not expose unsupported secondary auth or account flows", () => {
    expect(source).not.toContain("Sign in with Google")
    expect(source).not.toContain("Sign in with Github")
    expect(source).not.toContain("Create an account")
    expect(source).not.toContain("Forgot Password?")
    expect(source).not.toContain("Remember this device")
  })
})
