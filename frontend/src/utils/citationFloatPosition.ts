export type CitationAnchorRect = {
  top: number
  bottom: number
  left: number
  width: number
}

export type CitationFloatSize = {
  width: number
  height: number
}

export type CitationViewport = {
  width: number
  height: number
  scrollX: number
  scrollY: number
}

export type CitationFloatPlacement = 'bottom' | 'top' | 'clamped'

export type CitationFloatPosition = {
  top: number
  left: number
  placement: CitationFloatPlacement
}

const DEFAULT_GAP = 6
const DEFAULT_PADDING = 16

const clamp = (value: number, min: number, max: number) => {
  if (max < min) return min
  return Math.min(Math.max(value, min), max)
}

export function computeCitationFloatPosition({
  anchor,
  floatSize,
  viewport,
  offsetY = 0,
  gap = DEFAULT_GAP,
  padding = DEFAULT_PADDING,
}: {
  anchor: CitationAnchorRect
  floatSize: CitationFloatSize
  viewport: CitationViewport
  offsetY?: number
  gap?: number
  padding?: number
}): CitationFloatPosition {
  const anchorTop = anchor.top + viewport.scrollY
  const anchorBottom = anchor.bottom + viewport.scrollY
  const visibleTop = viewport.scrollY + padding
  const visibleBottom = viewport.scrollY + viewport.height - padding

  const belowTop = anchorBottom + gap + offsetY
  const aboveTop = anchorTop - floatSize.height - gap - offsetY
  const belowFits = belowTop + floatSize.height <= visibleBottom
  const aboveFits = aboveTop >= visibleTop

  let top = belowTop
  let placement: CitationFloatPlacement = 'bottom'
  if (!belowFits && aboveFits) {
    top = aboveTop
    placement = 'top'
  } else if (!belowFits) {
    top = clamp(belowTop, visibleTop, visibleBottom - floatSize.height)
    placement = 'clamped'
  }

  const left = clamp(
    anchor.left + viewport.scrollX,
    viewport.scrollX + padding,
    viewport.scrollX + viewport.width - padding - floatSize.width,
  )

  return { top, left, placement }
}

export function currentCitationViewport(): CitationViewport {
  return {
    width: window.innerWidth,
    height: window.innerHeight,
    scrollX: window.scrollX,
    scrollY: window.scrollY,
  }
}

export function elementAnchorRect(el: HTMLElement): CitationAnchorRect {
  const rect = el.getBoundingClientRect()
  return {
    top: rect.top,
    bottom: rect.bottom,
    left: rect.left,
    width: rect.width,
  }
}
