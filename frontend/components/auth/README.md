# Auth UI Rules

`frontend/components/auth` owns the local composition rules for public auth entry surfaces and protected auth shell handoff. This README is the near-code contract for login visual readiness, boot loading handoff, and auth-specific fallback behavior.

## Protected Route Redirects

- Protected-route unauthenticated access must redirect directly to `/login/?returnTo=...`.
- `returnTo` must preserve the originally requested in-app destination so auth recovery can resume the workflow after login.
- The root app entry `/` is a redirect-only entry and must normalize to `/overview/` before it is stored or consumed as `returnTo`.
- `returnTo` must stay canonical and in-app only. Non-canonical or external values must be rejected and fall back to `/overview/`; auth recovery must not retain a legacy locale-prefix compatibility branch.
- Protected-route `401` is an auth transition, not a page-level error state. `AuthLayout`, `AuthGuard`, and transport ownership must redirect instead of rendering shell-scoped business error UI.
- **Auth pending must never mount the protected app shell.** When `authenticated` is false or auth state is unresolved on a protected route, `AuthLayout` must hold `data-boot-handoff-pending="true"` with a non-visual blocker so the global boot layer remains the only visible first-screen owner. It must not render `AppShellWarmup`, sidebar, header, protected shell chrome, or a second visible auth warmup card. The `ProtectedAuthLayout` component only renders after authentication is confirmed.
- **Protected-shell and route-child suspense use the same non-visual handoff.** While their lazy chunks are pending, they must keep `data-boot-handoff-pending="true"` so the root boot layer remains visible. Do not render `AppShellWarmup`, a route fallback, or a detail-shell skeleton before the route's own approved workspace owner commits.
- **Protected sidebar soft navigation stays framework-owned.** Every navigable sidebar `Link` reports its pending state through `useLinkStatus`; pathname remains the committed route source of truth. The shared sidebar scope suppresses the committed selection while the clicked Link is pending, so success commits the new item and cancellation or failure restores the previous item without a timer-backed route store.
- The protected shell uses the one `protectedAppShellContentFrameClassName` from `protected-app-shell.ts` in both the resolved layout and `AppShellWarmup`. It consumes `COMPACT_CONTENT_FRAME_CLASS` to cap ordinary route frames at 1624px (1600px content plus existing gutters), centered after the sidebar. Active page-level canvases may opt out with `data-workspace-width="full"`; the workflow management list remains bounded. The full-width scroll area must not add an extra shell-owned side gutter; route-owned page content applies `COMPACT_CONTENT_GUTTER_CLASS` where readable outer whitespace is intended.
- `ProtectedAuthLayout` MUST NOT observe sidebar pending state to hide, replace, or overlay the committed content region. Any first-screen skeleton belongs to the destination page or workspace through its own query/loading state or `ContentHandoff`; a destination that is already ready may commit content directly.
- If transport-level `401` handling runs while the browser is already on a public auth route such as `/login/`, it must redirect to the canonical `/login/` path without wrapping the current login URL into a new `returnTo`. Recursive `/login/?returnTo=/login?...` redirects are invalid.

System upgrades run on the deployment host. The old `/system-upgrade/` route
redirects to the overview, and the protected shell renders without polling
for an upgrade operation.

## Login Boot Handoff

- `/login/` must keep the server-rendered `lunafox-boot-layer` as the only visible first-screen loading owner until the login surface can show a stable first visual frame.
- Protected unauthenticated entry should flow as: global boot layer remains visible, auth redirects to `/login/?returnTo=...`, login content mounts behind the boot layer, then the boot layer exits only after the login visual is ready. Do not insert the `AUTH / 加载中` warmup card between boot and login.
- `app/login/content.tsx` owns the route-level readiness gate through `loginVisualReady` and `data-boot-handoff-pending`.
- `/login/` must keep the login surface mounted while auth state is unresolved. Client-only token reads can make the browser know more than the server on the first render; that must not remove `ContentReveal` or `data-boot-handoff-pending` before hydration completes.
- `BootLayerController` must treat `data-boot-handoff-pending="true"` as a blocker before starting the boot layer exit.
- `VisualSplitLogin` calls `onVisualReady` when the theme background and login form have a committed frame, or a published custom image/video has a usable frame. A failed custom visual leaves the theme background visible.
- Do not use `document.readyState`, a fixed timeout, or an unrelated component mount as the route handoff signal. Those can fire before the login visual has a visible first frame.

## Protected App Shell Scroll Geometry

- `protectedAppShellStyle` must not override `--sidebar-width`; `SidebarProvider` in `frontend/components/ui/sidebar.tsx` is the shared owner of expanded, icon-only, and mobile sidebar widths.
- The protected app shell main content scroller must use `protectedAppShellScrollAreaClassName` and `protectedAppShellScrollViewportClassName` from `frontend/components/auth/protected-app-shell.ts` through the shared Base UI `ScrollArea`.
- The shared `ScrollArea` uses `type="always"`; its overlay scrollbar stays visible while `protectedAppShellScrollViewportClassName` clips only page-level horizontal overflow. It therefore cannot reserve space from either side of the route canvas: page-level content owns its left/right whitespace through the shared 12px gutter token.
- `ProtectedAuthLayout` and `AppShellWarmup` must use the same ScrollArea root, viewport, and content-frame owners. Do not hand-roll a separate page-level main scroller in route code to compensate for scrollbar jitter; fix or extend the shared shell owner instead.
- The inner protected route frame must use `protectedAppShellContentFrameClassName` from the same module. It owns the shared fluid width, maximum width, centering, and `min-w-0` boundary for authenticated routes; see `components/shared/layout/README.md` for the width and canvas opt-out contract.
- Keep the scroll area full width and apply route-owned readable-width or overflow constraints inside the inner frame. Dense tables, logs, editors, and workflow canvases retain their existing internal overflow behavior.
- `ProtectedAuthLayout` must not add a whole-shell entry animation. Route `ContentReveal` and workspace `ContentHandoff` already own the visible handoff motion; adding shell-level fade creates a double reveal on hard reload.

## Protected Session Renewal

- `/asset-monitor/screen/` is an exact-match standalone authenticated canvas. It retains `AuthLayout`, the upgrade boundary, and the single session-renewal lifecycle, and renders its route-owned display header instead of ordinary app navigation. `/asset-monitor/` continues to use the standard protected app shell. The display route is never added to public paths.

- `ProtectedAuthLayout` owns exactly one `useSessionRenewal` lifecycle while authentication is confirmed. Do not move this ownership into dynamically loaded shell consumers such as `NotificationDrawer`.
- The coordinator schedules from the current JWT `exp`, wakes when the document becomes visible or the browser returns online, and cleans up its timer/listeners with the protected shell.
- Axios and fetch-based notification SSE must still run the shared request-time freshness check. Browser timers can be throttled, so the existing one-retry `401` and SSE `TOKEN_EXPIRED` recovery paths remain required.
- Renewal failure is terminal for the current local session and remains owned by the shared auth redirect boundary; shell or stream code must not add a parallel retry loop.

## Login Appearance and Background

- Login reuses the authenticated product's theme tokens, typography, Card, Input, Label, and Button. The brand and form are centered as the foreground entry point. The Earth and satellites are an enlarged, low-opacity background, clipped by the full-viewport decorative layer. They do not occupy a layout column or contribute scroll overflow. The card remains opaque and readable; there is no vertical divider or orbit drawn over the controls.
- Container queries use the login surface's own dimensions, so the scaled settings preview preserves desktop composition even inside a narrow host viewport. Published custom media remains full-bleed; a ready image or video hides the built-in scene and suspends its animation, and failed media restores it.
- `login-sky-illustration.tsx` owns a complete SVG globe with coastlines from the installed `world-atlas` Natural Earth land dataset, latitude/longitude lines, orbits and illustrative asset nodes. It loads no remote textures or geographic data, uses existing theme tokens, and never represents live security status. The initial globe is server-rendered so it does not delay the login boot handoff.
- The requested globe rotation is a bounded decorative visualization exception: one revolution per 150 seconds, at most 30 geometry updates per second, and SVG refs instead of per-frame React state. Back-facing geography and nodes are clipped. Reduced-motion preferences keep a static globe; hidden tabs, off-screen artwork, and a ready custom background suspend the animation. Every frame, observer and listener is cleaned up on unmount. On narrow screens the full globe becomes a quiet backdrop above the form.
- `login-satellite-orbits.ts` owns six inclined circular orbits and eighteen illustrative satellites, with independent 24–42 second periods and short trails. Satellites use a small body/solar-panel silhouette, not status dots. The orbit radius always exceeds the globe radius; depth separates near and far render layers, so the opaque Earth occludes satellites on the far side. Satellites share the globe's single animation clock, pause conditions and cleanup; no extra animation loop or React state is introduced.
- The Chinese login contains the product identity, account fields, submit action, and access help. Decorative English, promotional copy, and a development-version footer are omitted.
- Published administrator-selected images and videos remain supported behind a theme-colored veil. The native form stays opaque and readable over custom backgrounds.
- Password visibility uses the shared Button with a localized accessible name and pressed state. It preserves the password value and autocomplete semantics, never submits the form, and stays disabled during submission or settings preview. Login and the settings preview share the same translated copy and composition.
- The background must not render its own animated loading indicator such as dots, spinners, pulses, shimmer, or progress bars. A second local loading scene would compete with the global boot loader.
- The intended sequence is:
  1. global boot loader is visible;
  2. login page mounts the same login surface on the server and first client render while `data-boot-handoff-pending="true"`;
  3. the committed theme background or a ready published visual clears the pending flag;
  4. global boot loader exits over an already-ready login visual.
- The form stays usable on narrow screens; reduced-motion preferences continue to replace a published video with its poster. The scaled settings preview uses the same component and disables credential inputs; only the surrounding background is a replacement control.
- The public login owns a viewport-height scroll container because the global body does not scroll. Short windows and expanded remember-password help must keep the submit action and access help reachable; the scaled preview retains its bounded, non-scrolling frame.

## Verification

- Code guards:
  - `components/__tests__/boot-layer-controller.contract.test.ts`
  - `app/login/__tests__/content.contract.test.ts`
  - `app/login/__tests__/content.test.tsx`
  - `components/auth/__tests__/visual-split-login.contract.test.ts`
  - `components/auth/__tests__/auth-layout.contract.test.ts`
  - `components/__tests__/faulty-terminal.contract.test.ts`
- Browser smoke for this handoff should sample `/login/` in an unauthenticated context and confirm:
  - `data-boot-handoff-pending` stays true until the theme background and login form are ready to paint;
  - `.auth-shader-fallback__dots` and `.auth-shader-fallback__dot` do not exist;
  - the boot layer exits after the built-in or published visual is ready, and a failed published image leaves the native login form usable.
