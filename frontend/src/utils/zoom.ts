/**
 * Helpers for positioning floating UI (fixed/absolute popovers) under CSS
 * `zoom` applied on the root `<html>` element.
 *
 * Why this exists:
 * - `composables/useFont.ts` writes `zoom: <scale>` onto `<html>` to honor the
 *   user's font-size preference. That zoom multiplies everything inside.
 * - `getBoundingClientRect()`, `window.innerWidth/innerHeight`, `MouseEvent.clientX/Y`
 *   and similar APIs return values in **visual (device-like) pixels** — already
 *   scaled by zoom.
 * - CSS lengths written to a fixed/absolute element living inside the zoom
 *   context (anywhere under `<html>`) are interpreted in **CSS pixels** and
 *   then multiplied by zoom when rendered.
 *
 * To anchor a popover to a visual coordinate (e.g. the rect of a button), we
 * must divide the visual measurement by the root zoom before writing it into
 * CSS `left/top/right/bottom/width/height/maxHeight/...`.
 */

/**
 * Read the current `zoom` value applied to `<html>`. Returns 1 when zoom is
 * unset, computed as a non-finite value, or in environments without a DOM.
 */
export function getRootZoom(): number {
  if (typeof document === 'undefined') return 1
  const root = document.documentElement
  if (!root) return 1
  const raw = getComputedStyle(root).zoom
  const zoom = Number.parseFloat(raw || '1')
  if (!Number.isFinite(zoom) || zoom <= 0) return 1
  return zoom
}

/**
 * Convert a visual-pixel measurement to the CSS-pixel value that should be
 * written into the style of a fixed/absolute element under the root zoom.
 *
 * Pass `zoom` explicitly when you already cached it for a calculation to
 * avoid re-reading computed style multiple times.
 */
export function toCssPx(visualPx: number, zoom: number = getRootZoom()): number {
  return visualPx / zoom
}

/**
 * Normalize a `DOMRect`-like value from `getBoundingClientRect()` (visual
 * pixels) into CSS pixels. Returns a plain object so callers can read/write
 * freely without worrying about the immutable `DOMRect` shape.
 */
export function rectToCssPx(
  rect: { top: number; left: number; right: number; bottom: number; width: number; height: number },
  zoom: number = getRootZoom(),
): { top: number; left: number; right: number; bottom: number; width: number; height: number } {
  return {
    top: rect.top / zoom,
    left: rect.left / zoom,
    right: rect.right / zoom,
    bottom: rect.bottom / zoom,
    width: rect.width / zoom,
    height: rect.height / zoom,
  }
}

/**
 * Viewport size in CSS pixels (i.e., the coordinate system used by CSS
 * lengths under the root zoom). `window.innerWidth/innerHeight` are visual
 * pixels, so divide by zoom.
 */
export function cssViewportSize(zoom: number = getRootZoom()): { width: number; height: number } {
  if (typeof window === 'undefined') return { width: 0, height: 0 }
  return {
    width: window.innerWidth / zoom,
    height: window.innerHeight / zoom,
  }
}
