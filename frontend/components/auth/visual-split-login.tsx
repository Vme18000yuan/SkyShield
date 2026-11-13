"use client"

/* eslint-disable @next/next/no-img-element -- Published login media is served by the configured backend. */

import * as React from "react"

import { TianqiongMark } from "@/components/brand/tianqiong-mark"
import { IconEye, IconEyeOff, IconLock, ArrowRight } from "@/components/icons"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardFooter, CardHeader } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { usePublicLoginVisual } from "@/hooks/use-login-visual"
import { clearRememberedLogin, loadRememberedLogin, saveRememberedLogin } from "@/lib/remembered-login"
import { toastFeedback } from "@/lib/toast-helpers"
import { textRole } from "@/lib/typography"
import { cn } from "@/lib/utils"
import type { PublicLoginVisual } from "@/types/login-visual.types"
import { LoginSkyIllustration } from "./login-sky-illustration"

import "./product-login-background.css"

interface VisualSplitLoginProps {
  onLogin: (username: string, password: string) => Promise<void>
  authDone?: boolean
  isPending?: boolean
  onVisualReady?: () => void
  className?: string
  translations: VisualSplitLoginTranslations
  preview?: VisualSplitLoginPreview
}

export interface VisualSplitLoginTranslations {
  ariaLabel: string
  title: string
  brand: string
  platform: string
  formTitle: string
  formDescription: string
  usernameLabel: string
  usernamePlaceholder: string
  passwordLabel: string
  passwordPlaceholder: string
  showPassword: string
  hidePassword: string
  rememberPassword: string
  rememberPasswordHint: string
  rememberPasswordStorageError: string
  submit: string
  accessHelp: string
}

export interface VisualSplitLoginPreview {
  visual: PublicLoginVisual
  visualControl: React.ReactNode
}

function useReducedMotion() {
  const [reducedMotion, setReducedMotion] = React.useState(false)

  React.useEffect(() => {
    const query = window.matchMedia("(prefers-reduced-motion: reduce)")
    const update = () => setReducedMotion(query.matches)
    update()
    query.addEventListener("change", update)
    return () => query.removeEventListener("change", update)
  }, [])

  return reducedMotion
}

function PublishedLoginVisual({ visual, onReady, onVisibilityChange }: {
  visual: PublicLoginVisual | undefined
  onReady: () => void
  onVisibilityChange: (visible: boolean) => void
}) {
  const [ready, setReady] = React.useState(false)
  const [failed, setFailed] = React.useState(false)
  const reducedMotion = useReducedMotion()
  const source = visual?.kind === "video" && reducedMotion ? visual.posterUrl : visual?.mediaUrl

  React.useEffect(() => {
    setReady(false)
    setFailed(false)
    onVisibilityChange(false)
  }, [source, visual?.kind, onVisibilityChange])

  if (!visual || visual.kind === "builtIn" || !source || failed) return null

  const handleReady = () => {
    setReady(true)
    onVisibilityChange(true)
    onReady()
  }
  const handleFailure = () => {
    setFailed(true)
    onVisibilityChange(false)
  }
  const className = cn(
    "product-login__media absolute inset-0 size-full object-cover transition-opacity duration-200 motion-reduce:transition-none",
    ready ? "opacity-100" : "opacity-0"
  )

  if (visual.kind === "image" || reducedMotion) {
    return <img alt="" className={className} data-login-visual-kind={visual.kind} onError={handleFailure} onLoad={handleReady} src={source} />
  }

  return <video aria-hidden="true" autoPlay className={className} data-login-visual-kind="video" loop muted onCanPlay={handleReady} onError={handleFailure} playsInline src={source} />
}

export function VisualSplitLogin({
  onLogin,
  authDone = false,
  isPending = false,
  onVisualReady,
  className,
  translations: t,
  preview,
}: VisualSplitLoginProps) {
  const [username, setUsername] = React.useState("")
  const [password, setPassword] = React.useState("")
  const [passwordVisible, setPasswordVisible] = React.useState(false)
  const [rememberPassword, setRememberPassword] = React.useState(false)
  const [isSubmitting, setIsSubmitting] = React.useState(false)
  const [publishedVisualReady, setPublishedVisualReady] = React.useState(false)
  const visualReadyRef = React.useRef(false)
  const publicVisual = usePublicLoginVisual()

  const submitting = isSubmitting || isPending || authDone
  const formDisabled = submitting || Boolean(preview)
  const visual = preview?.visual ?? publicVisual.data

  React.useEffect(() => {
    if (preview) return
    const saved = loadRememberedLogin()
    if (!saved) return
    setUsername(saved.username)
    setPassword(saved.password)
    setRememberPassword(true)
  }, [preview])

  const markVisualReady = React.useCallback(() => {
    if (visualReadyRef.current) return
    visualReadyRef.current = true
    onVisualReady?.()
  }, [onVisualReady])

  React.useEffect(() => {
    // The theme-owned background is ready to paint with the committed form.
    const frame = window.requestAnimationFrame(markVisualReady)
    return () => window.cancelAnimationFrame(frame)
  }, [markVisualReady])

  const handleSubmit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (formDisabled || !username.trim() || !password.trim()) return

    setIsSubmitting(true)
    try {
      await onLogin(username.trim(), password)
      if (rememberPassword && !saveRememberedLogin({ username: username.trim(), password })) {
        toastFeedback.warning(t.rememberPasswordStorageError)
      }
    } catch {
      // Authentication errors are reported by the login mutation.
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <section
      aria-label={t.ariaLabel}
      data-login-preview={preview ? "true" : undefined}
      className={cn("product-login relative isolate flex w-full flex-col bg-background text-foreground", preview ? "h-full min-h-0 overflow-hidden" : "h-svh overflow-x-hidden overflow-y-auto", className)}
    >
      <div className="product-login__sky-container absolute inset-0 bg-sidebar" data-login-visual-kind="builtIn" aria-hidden="true">
        <div className="product-login__scene" hidden={publishedVisualReady}>
          <LoginSkyIllustration active={!publishedVisualReady} />
        </div>
        <PublishedLoginVisual onReady={markVisualReady} onVisibilityChange={setPublishedVisualReady} visual={visual} />
        <div className="product-login__veil absolute inset-0 bg-background/85" />
      </div>
      {preview?.visualControl && (
        <div className="product-login__preview-control absolute inset-y-0 left-0 z-10 w-1/3">
          {preview.visualControl}
        </div>
      )}

      <main className={cn("relative z-20 flex flex-1 items-center justify-center px-6 py-12 sm:py-16", preview && "pointer-events-none")}>
        <div className="product-login__form relative pointer-events-auto w-full max-w-sm">
          <header className="mb-8 flex items-center justify-center gap-3 px-1">
            <TianqiongMark size={44} decorative />
            <div className="min-w-0 space-y-2">
              <h1 aria-label={t.title} className={textRole.pageTitleDisplay}>{t.brand}</h1>
              <p className={textRole.bodySubtle}>{t.platform}</p>
            </div>
          </header>

          <Card className="product-login__card gap-0 overflow-hidden border-t py-0">
            <CardHeader className="gap-2 px-6 pt-6 pb-6">
              <div className="flex items-center justify-between gap-3">
                <h2 className={textRole.panelTitle}>{t.formTitle}</h2>
                <IconLock aria-hidden="true" className="size-4 text-muted-foreground" />
              </div>
              <p className={textRole.bodySubtle}>{t.formDescription}</p>
            </CardHeader>
            <CardContent>
              <form className="space-y-5" onSubmit={handleSubmit}>
                <div className="space-y-2">
                  <Label htmlFor={preview ? "login-visual-preview-email" : "email"} className={textRole.bodyStrong}>
                    {t.usernameLabel}
                  </Label>
                  <Input
                    id={preview ? "login-visual-preview-email" : "email"}
                    name="username"
                    type="text"
                    autoComplete="username"
                    autoCapitalize="none"
                    spellCheck={false}
                    placeholder={t.usernamePlaceholder}
                    required
                    value={username}
                    onChange={(event) => setUsername(event.target.value)}
                    disabled={formDisabled}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor={preview ? "login-visual-preview-password" : "password"} className={textRole.bodyStrong}>
                    {t.passwordLabel}
                  </Label>
                  <div className="relative">
                    <Input
                      id={preview ? "login-visual-preview-password" : "password"}
                      name="password"
                      type={passwordVisible ? "text" : "password"}
                      autoComplete="current-password"
                      placeholder={t.passwordPlaceholder}
                      required
                      value={password}
                      onChange={(event) => setPassword(event.target.value)}
                      disabled={formDisabled}
                      className="pr-11"
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      className="absolute right-0.5 top-1/2 -translate-y-1/2 text-muted-foreground"
                      aria-label={passwordVisible ? t.hidePassword : t.showPassword}
                      aria-pressed={passwordVisible}
                      aria-controls={preview ? "login-visual-preview-password" : "password"}
                      disabled={formDisabled}
                      onClick={() => setPasswordVisible((visible) => !visible)}
                    >
                      {passwordVisible ? <IconEyeOff aria-hidden="true" /> : <IconEye aria-hidden="true" />}
                    </Button>
                  </div>
                </div>
                <div className="space-y-1.5">
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id={preview ? "login-visual-preview-remember" : "remember-password"}
                      checked={rememberPassword}
                      disabled={formDisabled}
                      aria-describedby={rememberPassword ? "remember-password-hint" : undefined}
                      onCheckedChange={(checked) => {
                        if (!checked && !clearRememberedLogin()) {
                          toastFeedback.warning(t.rememberPasswordStorageError)
                          return
                        }
                        setRememberPassword(checked)
                      }}
                    />
                    <Label htmlFor={preview ? "login-visual-preview-remember" : "remember-password"}>
                      {t.rememberPassword}
                    </Label>
                  </div>
                  {rememberPassword && (
                    <p id="remember-password-hint" className={textRole.helperText}>{t.rememberPasswordHint}</p>
                  )}
                </div>
                <Button type="submit" disabled={formDisabled} loading={!preview && submitting} loadingLabel={t.submit} layout="fullWidth">
                  {t.submit}
                  <ArrowRight aria-hidden="true" />
                </Button>
              </form>
            </CardContent>
            <CardFooter className="mt-6 justify-center border-t bg-muted/30 px-6 py-4">
              <p className={cn(textRole.helperText, "text-center")}>{t.accessHelp}</p>
            </CardFooter>
          </Card>
        </div>
      </main>
    </section>
  )
}
