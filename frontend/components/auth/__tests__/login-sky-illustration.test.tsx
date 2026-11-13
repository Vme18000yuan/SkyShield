import { act, render } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { LoginSkyIllustration } from "@/components/auth/login-sky-illustration"
import { getSatelliteFrames } from "@/components/auth/login-satellite-orbits"

describe("login globe animation lifecycle", () => {
  let callbacks: Map<number, FrameRequestCallback>
  let motionListeners: Set<EventListenerOrEventListenerObject>
  let reduced: boolean
  let hidden: boolean

  beforeEach(() => {
    callbacks = new Map()
    motionListeners = new Set()
    reduced = false
    hidden = false
    let nextFrame = 0
    vi.spyOn(window, "requestAnimationFrame").mockImplementation(callback => {
      callbacks.set(++nextFrame, callback)
      return nextFrame
    })
    vi.spyOn(window, "cancelAnimationFrame").mockImplementation(id => { callbacks.delete(id) })
    vi.spyOn(document, "hidden", "get").mockImplementation(() => hidden)
    vi.spyOn(window, "matchMedia").mockImplementation(query => ({
      get matches() { return reduced },
      media: query, onchange: null, addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
      addEventListener: (_type: string, listener: EventListenerOrEventListenerObject) => { motionListeners.add(listener) },
      removeEventListener: (_type: string, listener: EventListenerOrEventListenerObject) => { motionListeners.delete(listener) },
    } as MediaQueryList))
  })

  afterEach(() => vi.restoreAllMocks())

  function tick(time: number) {
    act(() => {
      const pending = [...callbacks.values()]
      callbacks.clear()
      pending.forEach(callback => callback(time))
    })
  }

  function changeMotion(value: boolean) {
    reduced = value
    act(() => {
      motionListeners.forEach(listener => {
        const event = new Event("change")
        if (typeof listener === "function") listener(event)
        else listener.handleEvent(event)
      })
    })
  }

  it("rotates the geography and cancels animation on unmount", () => {
    const { container, unmount } = render(<LoginSkyIllustration />)
    const land = container.querySelector('[data-globe-layer="land"]')!
    const satellite = container.querySelector('[data-satellite-layer="near"]')!
    const initial = land.getAttribute("d")
    const initialSatellite = satellite.getAttribute("transform")
    expect(initial).toBeTruthy()
    expect(callbacks.size).toBe(1)
    tick(0)
    tick(50)
    expect(land.getAttribute("d")).not.toBe(initial)
    expect(satellite.getAttribute("transform")).not.toBe(initialSatellite)
    unmount()
    expect(callbacks.size).toBe(0)
    expect(motionListeners.size).toBe(0)
  })

  it("keeps satellite periods independent and moves them between near and far layers", () => {
    const initial = getSatelliteFrames(0)
    const quarterOrbit = getSatelliteFrames(6_000)
    const farSide = getSatelliteFrames(18_000)
    const fullOrbit = getSatelliteFrames(24_000)
    expect(initial).toHaveLength(18)
    expect(quarterOrbit[0].nearOpacity).toBeGreaterThan(0)
    expect(quarterOrbit[0].farOpacity).toBe(0)
    expect(farSide[0].nearOpacity).toBe(0)
    expect(farSide[0].farOpacity).toBeGreaterThan(0)
    expect(fullOrbit[0]).toEqual(initial[0])
    expect(fullOrbit[3].transform).not.toBe(initial[3].transform)
    for (const frame of [initial, quarterOrbit, farSide, fullOrbit]) {
      for (const satellite of frame) {
        expect(satellite.nearOpacity > 0).not.toBe(satellite.farOpacity > 0)
      }
    }
  })

  it("keeps a static globe under reduced motion and responds to preference changes", () => {
    reduced = true
    const { container } = render(<LoginSkyIllustration />)
    const land = container.querySelector('[data-globe-layer="land"]')!
    const initial = land.getAttribute("d")
    expect(callbacks.size).toBe(0)
    changeMotion(false)
    tick(0)
    tick(50)
    expect(land.getAttribute("d")).not.toBe(initial)
    changeMotion(true)
    expect(callbacks.size).toBe(0)
  })

  it("pauses when covered by published media or when the tab is hidden", () => {
    const { rerender } = render(<LoginSkyIllustration active={false} />)
    expect(callbacks.size).toBe(0)
    rerender(<LoginSkyIllustration active />)
    expect(callbacks.size).toBe(1)
    hidden = true
    act(() => { document.dispatchEvent(new Event("visibilitychange")) })
    expect(callbacks.size).toBe(0)
    hidden = false
    act(() => { document.dispatchEvent(new Event("visibilitychange")) })
    expect(callbacks.size).toBe(1)
    rerender(<LoginSkyIllustration active={false} />)
    expect(callbacks.size).toBe(0)
  })
})
