export const DEFAULT_POPOVER_EDGE_GAP = 8

const finiteNonNegative = (value: number): number =>
  Number.isFinite(value) ? Math.max(0, value) : 0

const effectiveEdgeGap = (viewportSize: number, edgeGap: number): number =>
  Math.min(finiteNonNegative(edgeGap), finiteNonNegative(viewportSize) / 2)

export interface ViewportSize {
  width: number
  height: number
}

export interface AnchorRect {
  top: number
  right: number
  bottom: number
  left: number
  width: number
  height: number
}

export interface AnchoredPopoverOptions {
  preferredWidth: number
  preferredHeight: number
  minSpaceBelow?: number
  maxHeightRatio?: number
  edgeGap?: number
  offsetY?: number
  align?: 'start' | 'end'
}

export interface AnchoredPopoverLayout {
  style: Record<string, string>
  width: number
  maxHeight: number
  left: number
  openBelow: boolean
}

export function clampPopoverWidth(
  preferredWidth: number,
  viewportWidth: number,
  edgeGap = DEFAULT_POPOVER_EDGE_GAP,
): number {
  const safeViewportWidth = finiteNonNegative(viewportWidth)
  const safeEdgeGap = effectiveEdgeGap(safeViewportWidth, edgeGap)
  return Math.min(finiteNonNegative(preferredWidth), safeViewportWidth - safeEdgeGap * 2)
}

export function clampPopoverLeft(
  preferredLeft: number,
  popoverWidth: number,
  viewportWidth: number,
  edgeGap = DEFAULT_POPOVER_EDGE_GAP,
): number {
  const safeViewportWidth = finiteNonNegative(viewportWidth)
  const safePopoverWidth = Math.min(finiteNonNegative(popoverWidth), safeViewportWidth)
  const safeEdgeGap = effectiveEdgeGap(safeViewportWidth, edgeGap)
  const minLeft = safeEdgeGap
  const maxLeft = Math.max(minLeft, safeViewportWidth - safePopoverWidth - safeEdgeGap)
  const candidate = Number.isFinite(preferredLeft) ? Math.floor(preferredLeft) : minLeft
  return Math.max(minLeft, Math.min(maxLeft, candidate))
}

export function computeAnchoredPopoverLayout(
  anchor: AnchorRect,
  viewport: ViewportSize,
  options: AnchoredPopoverOptions,
): AnchoredPopoverLayout {
  const viewportWidth = finiteNonNegative(viewport.width)
  const viewportHeight = finiteNonNegative(viewport.height)
  const requestedEdgeGap = options.edgeGap ?? DEFAULT_POPOVER_EDGE_GAP
  const horizontalEdgeGap = effectiveEdgeGap(viewportWidth, requestedEdgeGap)
  const verticalEdgeGap = effectiveEdgeGap(viewportHeight, requestedEdgeGap)
  const offsetY = finiteNonNegative(options.offsetY ?? 8)
  const width = clampPopoverWidth(options.preferredWidth, viewportWidth, horizontalEdgeGap)
  const preferredLeft = options.align === 'end' ? anchor.right - width : anchor.left
  const left = clampPopoverLeft(preferredLeft, width, viewportWidth, horizontalEdgeGap)
  const anchorTop = Number.isFinite(anchor.top) ? anchor.top : 0
  const anchorBottom = Number.isFinite(anchor.bottom) ? anchor.bottom : anchorTop
  const availableBelow = Math.max(0, viewportHeight - anchorBottom - offsetY - verticalEdgeGap)
  const availableAbove = Math.max(0, anchorTop - offsetY - verticalEdgeGap)
  const openBelow = availableBelow >= (options.minSpaceBelow ?? 160) || availableBelow > availableAbove
  const availableHeight = openBelow ? availableBelow : availableAbove
  const ratio = finiteNonNegative(options.maxHeightRatio ?? 1)
  const ratioLimit = viewportHeight * ratio
  const maxHeight = Math.min(finiteNonNegative(options.preferredHeight), ratioLimit, availableHeight)

  return {
    width,
    maxHeight,
    left,
    openBelow,
    style: {
      position: 'fixed',
      width: `${width}px`,
      left: `${left}px`,
      maxHeight: `${maxHeight}px`,
      ...(openBelow
        ? { top: `${Math.floor(anchorBottom + offsetY)}px`, bottom: 'auto' }
        : { top: 'auto', bottom: `${Math.floor(viewportHeight - anchorTop + offsetY)}px` }),
    },
  }
}
