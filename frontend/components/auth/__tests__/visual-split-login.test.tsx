import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { VisualSplitLogin, type VisualSplitLoginTranslations } from "@/components/auth/visual-split-login"
import { loadRememberedLogin, saveRememberedLogin } from "@/lib/remembered-login"
import { toastFeedback } from "@/lib/toast-helpers"
import type { PublicLoginVisual } from "@/types/login-visual.types"

const visualState = vi.hoisted(() => ({ data: { kind: "builtIn" } as { kind: string; mediaUrl?: string; posterUrl?: string } }))
vi.mock("@/hooks/use-login-visual", () => ({ usePublicLoginVisual: () => ({ data: visualState.data }) }))

const translations: VisualSplitLoginTranslations = {
  ariaLabel: "登录", title: "天穹 企业级攻击面管理平台", brand: "天穹", platform: "企业级攻击面管理平台",
  formTitle: "登录控制台", formDescription: "使用您的账户，进入天穹工作空间。",
  usernameLabel: "用户名", usernamePlaceholder: "请输入用户名", passwordLabel: "密码",
  passwordPlaceholder: "请输入密码", rememberPassword: "记住密码", rememberPasswordHint: "密码保存在本浏览器",
  showPassword: "显示密码", hidePassword: "隐藏密码",
  rememberPasswordStorageError: "无法保存或清除登录信息", submit: "登录", accessHelp: "请联系管理员",
}

describe("Tianqiong login", () => {
  beforeEach(() => { visualState.data = { kind: "builtIn" }; window.localStorage.clear() })
  afterEach(() => vi.restoreAllMocks())

  it("renders the product title and submits the existing login form", async () => {
    const onLogin = vi.fn().mockResolvedValue(undefined)
    const ready = vi.fn()
    const { container } = render(<VisualSplitLogin onLogin={onLogin} onVisualReady={ready} translations={translations} />)
    expect(screen.getByRole("heading", { name: translations.title })).toBeVisible()
    expect(container.querySelector('[data-login-visual-kind="builtIn"]')).toBeInTheDocument()
    expect(container.querySelector('img[data-login-visual-kind="builtIn"]')).toBeNull()
    await waitFor(() => expect(ready).toHaveBeenCalledTimes(1))
    fireEvent.change(screen.getByLabelText("用户名"), { target: { value: "  demo  " } })
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "fixture-secret" } })
    fireEvent.click(screen.getByRole("button", { name: "登录" }))
    await waitFor(() => expect(onLogin).toHaveBeenCalledWith("demo", "fixture-secret"))
    expect(loadRememberedLogin()).toBeNull()
  })

  it("remembers a successful login and clears it when the option is unchecked", async () => {
    const onLogin = vi.fn().mockResolvedValue(undefined)
    const { unmount } = render(<VisualSplitLogin onLogin={onLogin} translations={translations} />)
    const checkbox = screen.getByRole("checkbox", { name: "记住密码" })
    expect(checkbox).not.toBeChecked()
    fireEvent.change(screen.getByLabelText("用户名"), { target: { value: "  demo  " } })
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "fixture-secret" } })
    fireEvent.click(checkbox)
    expect(screen.getByText("密码保存在本浏览器")).toBeVisible()
    fireEvent.click(screen.getByRole("button", { name: "登录" }))
    await waitFor(() => expect(loadRememberedLogin()).toEqual({ username: "demo", password: "fixture-secret" }))
    unmount()

    render(<VisualSplitLogin onLogin={onLogin} translations={translations} />)
    expect(screen.getByLabelText("用户名")).toHaveValue("demo")
    expect(screen.getByLabelText("密码")).toHaveValue("fixture-secret")
    const restoredCheckbox = screen.getByRole("checkbox", { name: "记住密码" })
    expect(restoredCheckbox).toBeChecked()
    fireEvent.click(restoredCheckbox)
    expect(loadRememberedLogin()).toBeNull()
  })

  it("toggles password visibility without submitting or changing credentials", () => {
    const onLogin = vi.fn()
    const { rerender } = render(<VisualSplitLogin onLogin={onLogin} translations={translations} />)
    const password = screen.getByLabelText("密码")
    fireEvent.change(password, { target: { value: "fixture-secret" } })
    const toggle = screen.getByRole("button", { name: "显示密码" })
    expect(password).toHaveAttribute("type", "password")
    expect(toggle).toHaveAttribute("aria-pressed", "false")
    fireEvent.click(toggle)
    expect(password).toHaveAttribute("type", "text")
    expect(password).toHaveValue("fixture-secret")
    expect(password).toHaveAttribute("autoComplete", "current-password")
    expect(screen.getByRole("button", { name: "隐藏密码" })).toHaveAttribute("aria-pressed", "true")
    fireEvent.click(toggle)
    expect(password).toHaveAttribute("type", "password")
    expect(onLogin).not.toHaveBeenCalled()
    rerender(<VisualSplitLogin onLogin={onLogin} translations={translations} isPending />)
    expect(toggle).toBeDisabled()
  })

  it("does not save failed login attempts", async () => {
    const onLogin = vi.fn().mockRejectedValue(new Error("invalid credentials"))
    render(<VisualSplitLogin onLogin={onLogin} translations={translations} />)
    fireEvent.change(screen.getByLabelText("用户名"), { target: { value: "demo" } })
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "invalid-secret" } })
    fireEvent.click(screen.getByRole("checkbox", { name: "记住密码" }))
    fireEvent.click(screen.getByRole("button", { name: "登录" }))
    await waitFor(() => expect(onLogin).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(screen.getByRole("button", { name: "登录" })).toBeEnabled())
    expect(loadRememberedLogin()).toBeNull()
  })

  it("keeps login usable and reports blocked browser storage", async () => {
    const onLogin = vi.fn().mockResolvedValue(undefined)
    const warning = vi.spyOn(toastFeedback, "warning").mockImplementation(() => "warning")
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked") })
    render(<VisualSplitLogin onLogin={onLogin} translations={translations} />)
    fireEvent.change(screen.getByLabelText("用户名"), { target: { value: "demo" } })
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "fixture-secret" } })
    fireEvent.click(screen.getByRole("checkbox", { name: "记住密码" }))
    fireEvent.click(screen.getByRole("button", { name: "登录" }))
    await waitFor(() => expect(onLogin).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(warning).toHaveBeenCalledWith("无法保存或清除登录信息"))
  })

  it("keeps the option selected if the browser refuses to clear saved credentials", () => {
    saveRememberedLogin({ username: "demo", password: "fixture-secret" })
    const warning = vi.spyOn(toastFeedback, "warning").mockImplementation(() => "warning")
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("blocked") })
    render(<VisualSplitLogin onLogin={vi.fn()} translations={translations} />)
    const checkbox = screen.getByRole("checkbox", { name: "记住密码" })
    expect(checkbox).toBeChecked()
    fireEvent.click(checkbox)
    expect(checkbox).toBeChecked()
    expect(warning).toHaveBeenCalledWith("无法保存或清除登录信息")
    expect(loadRememberedLogin()).toEqual({ username: "demo", password: "fixture-secret" })
  })

  it("retains the theme background if an administrator-selected image fails", async () => {
    visualState.data = { kind: "image", mediaUrl: "/fixture-custom.png" }
    const ready = vi.fn()
    const { container } = render(<VisualSplitLogin onLogin={vi.fn()} onVisualReady={ready} translations={translations} />)
    const custom = container.querySelector('img[data-login-visual-kind="image"]')!
    expect(custom).toHaveAttribute("src", "/fixture-custom.png")
    fireEvent.error(custom)
    expect(container.querySelector('img[data-login-visual-kind="image"]')).toBeNull()
    expect(container.querySelector('[data-login-visual-kind="builtIn"]')).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "登录" })).toBeEnabled()
    await waitFor(() => expect(ready).toHaveBeenCalledTimes(1))
  })

  it("reveals a published image and releases the boot handoff once", async () => {
    visualState.data = { kind: "image", mediaUrl: "/fixture-custom.png" }
    const ready = vi.fn()
    const { container } = render(<VisualSplitLogin onLogin={vi.fn()} onVisualReady={ready} translations={translations} />)
    const custom = container.querySelector('img[data-login-visual-kind="image"]')!
    fireEvent.load(custom)
    expect(custom).toHaveClass("opacity-100")
    await waitFor(() => expect(ready).toHaveBeenCalledTimes(1))
  })

  it("uses the custom video poster under reduced motion and disables preview input", () => {
    saveRememberedLogin({ username: "demo", password: "fixture-secret" })
    vi.spyOn(window, "matchMedia").mockImplementation(query => ({
      matches: true, media: query, onchange: null, addListener: vi.fn(), removeListener: vi.fn(),
      addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn(),
    }))
    const visual: PublicLoginVisual = { kind: "video", mediaUrl: "/fixture.mp4", posterUrl: "/fixture-poster.png" }
    const { container } = render(<VisualSplitLogin onLogin={vi.fn()} translations={translations} preview={{ visual, visualControl: <span>更换视觉</span> }} />)
    expect(container.querySelector('img[data-login-visual-kind="video"]')).toHaveAttribute("src", "/fixture-poster.png")
    expect(container.querySelector("video")).toBeNull()
    expect(screen.getByLabelText("用户名")).toBeDisabled()
    expect(screen.getByLabelText("用户名")).toHaveValue("")
    expect(screen.getByLabelText("密码")).toHaveValue("")
    expect(screen.getByRole("button", { name: "显示密码" })).toBeDisabled()
    expect(screen.getByRole("checkbox", { name: "记住密码" })).toHaveAttribute("aria-disabled", "true")
    expect(screen.getByText("更换视觉")).toBeVisible()
  })
})
