export const PINCH_ZOOM_EPSILON = 0.02

export interface ResponsiveViewportGeometryInput {
  innerWidth: number
  innerHeight: number
  rootZoom?: number
  visualWidth?: number
  visualHeight?: number
  visualOffsetTop?: number
  visualScale?: number
}

export interface ResponsiveViewportGeometry {
  layoutWidth: number
  layoutHeight: number
  visualWidth: number
  visualHeight: number
  visualOffsetTop: number
  visualScale: number
  keyboardInset: number
  isPinchZoomed: boolean
}

const positiveOr = (value: number | undefined, fallback: number): number =>
  typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : fallback

const nonNegativeOr = (value: number | undefined, fallback: number): number =>
  typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : fallback

/**
 * Resolve the two viewport coordinate systems used by the application.
 *
 * `window.innerWidth/innerHeight` describe the layout viewport. A mobile
 * pinch gesture only changes `VisualViewport`, so feeding its shrunken size
 * into full-screen layout CSS makes pages collapse or appear blank. The
 * visual viewport is still useful while the software keyboard is open, but
 * only at the normal browser scale.
 */
export function resolveResponsiveViewportGeometry({
  innerWidth,
  innerHeight,
  rootZoom = 1,
  visualWidth,
  visualHeight,
  visualOffsetTop = 0,
  visualScale = 1,
}: ResponsiveViewportGeometryInput): ResponsiveViewportGeometry {
  const zoom = positiveOr(rootZoom, 1)
  const safeInnerWidth = nonNegativeOr(innerWidth, 0)
  const safeInnerHeight = nonNegativeOr(innerHeight, 0)
  const layoutWidth = safeInnerWidth / zoom
  const layoutHeight = safeInnerHeight / zoom
  const rawVisualWidth = positiveOr(visualWidth, safeInnerWidth) / zoom
  const rawVisualHeight = positiveOr(visualHeight, safeInnerHeight) / zoom
  const rawVisualOffsetTop = nonNegativeOr(visualOffsetTop, 0) / zoom
  const scale = positiveOr(visualScale, 1)
  const isPinchZoomed = Math.abs(scale - 1) > PINCH_ZOOM_EPSILON

  // During pinch zoom, VisualViewport represents only the magnified slice
  // currently visible on screen. Keep the app's layout contract stable and
  // let the browser perform the visual magnification/panning itself.
  if (isPinchZoomed) {
    return {
      layoutWidth,
      layoutHeight,
      visualWidth: layoutWidth,
      visualHeight: layoutHeight,
      visualOffsetTop: 0,
      visualScale: scale,
      keyboardInset: 0,
      isPinchZoomed: true,
    }
  }

  return {
    layoutWidth,
    layoutHeight,
    visualWidth: rawVisualWidth,
    visualHeight: rawVisualHeight,
    visualOffsetTop: rawVisualOffsetTop,
    visualScale: scale,
    keyboardInset: Math.max(0, layoutHeight - rawVisualHeight - rawVisualOffsetTop),
    isPinchZoomed: false,
  }
}