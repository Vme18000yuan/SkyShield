"use client"

import { TIANQIONG_MARK_DATA_URI } from "@/components/brand/tianqiong-mark-data"
import { cn } from "@/lib/utils"

type TianqiongMarkProps = {
  className?: string
  size?: number
  label?: string
  decorative?: boolean
}

let faviconMaskImagePromise: Promise<HTMLImageElement> | null = null

function loadFaviconMaskImage() {
  if (!faviconMaskImagePromise) {
    faviconMaskImagePromise = new Promise((resolve, reject) => {
      const image = new Image()
      image.onload = () => resolve(image)
      image.onerror = () => reject(new Error("Failed to load Tianqiong favicon mask image"))
      image.src = TIANQIONG_MARK_DATA_URI
    })
  }
  return faviconMaskImagePromise
}

export function TianqiongMark({
  className,
  size,
  label = "天穹 Logo",
  decorative = false,
}: TianqiongMarkProps) {
  const resolvedSize = size ? `${size}px` : undefined
  return (
    <span
      role={decorative ? undefined : "img"}
      aria-label={decorative ? undefined : label}
      aria-hidden={decorative ? "true" : undefined}
      data-slot="tianqiong-mark"
      className={cn("tianqiong-mark inline-flex shrink-0", className)}
      style={{ width: resolvedSize, height: resolvedSize, ["--tianqiong-mark-size" as string]: resolvedSize }}
    />
  )
}

export function getTianqiongFaviconSvg(color: string) {
  return [
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">',
    '<defs><mask id="tianqiong-mask">',
    '<rect width="64" height="64" fill="black"/>',
    `<image href="${TIANQIONG_MARK_DATA_URI}" width="64" height="64" preserveAspectRatio="xMidYMid meet"/>`,
    '</mask></defs>',
    `<rect width="64" height="64" fill="${color}" mask="url(#tianqiong-mask)"/>`,
    '</svg>',
  ].join("")
}

export async function getTianqiongFaviconPngDataUrl(color: string) {
  const maskImage = await loadFaviconMaskImage()
  const canvas = document.createElement("canvas")
  canvas.width = 64
  canvas.height = 64
  const context = canvas.getContext("2d")
  if (!context) throw new Error("Failed to create Tianqiong favicon canvas context")
  context.fillStyle = color
  context.fillRect(0, 0, 64, 64)
  context.globalCompositeOperation = "destination-in"
  context.drawImage(maskImage, 0, 0, 64, 64)
  context.globalCompositeOperation = "source-over"
  return canvas.toDataURL("image/png")
}

export async function getTianqiongBrandedFaviconPngDataUrl({ background, foreground }: { background: string; foreground: string }) {
  const maskImage = await loadFaviconMaskImage()
  const canvas = document.createElement("canvas")
  canvas.width = 64
  canvas.height = 64
  const context = canvas.getContext("2d")
  if (!context) throw new Error("Failed to create Tianqiong branded favicon canvas context")
  context.fillStyle = background
  context.beginPath()
  context.arc(32, 32, 30, 0, Math.PI * 2)
  context.fill()
  const mark = document.createElement("canvas")
  mark.width = 46
  mark.height = 46
  const markContext = mark.getContext("2d")
  if (!markContext) throw new Error("Failed to create Tianqiong favicon mark context")
  markContext.fillStyle = foreground
  markContext.fillRect(0, 0, 46, 46)
  markContext.globalCompositeOperation = "destination-in"
  markContext.drawImage(maskImage, 0, 0, 46, 46)
  context.drawImage(mark, 9, 9)
  return canvas.toDataURL("image/png")
}
