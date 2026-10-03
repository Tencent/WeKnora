export const SIDEBAR_DEFAULT_WIDTH = 260
export const SIDEBAR_MIN_WIDTH = 220
export const SIDEBAR_MAX_WIDTH = 280
export const SIDEBAR_COLLAPSED_WIDTH = 60
export const SIDEBAR_COLLAPSE_THRESHOLD = 180

export function clampSidebarWidth(width: number): number {
  if (!Number.isFinite(width) || width <= 0) return SIDEBAR_DEFAULT_WIDTH
  return Math.min(SIDEBAR_MAX_WIDTH, Math.max(SIDEBAR_MIN_WIDTH, Math.round(width)))
}
