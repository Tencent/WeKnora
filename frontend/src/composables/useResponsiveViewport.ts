import { onMounted, onUnmounted, readonly, ref } from 'vue'
import { getRootZoom } from '@/utils/zoom'
import { resolveResponsiveViewportGeometry } from './responsiveViewportMetrics'

export const PHONE_MAX_WIDTH = 767
export const COMPACT_MAX_WIDTH = 899
export const TABLET_MAX_WIDTH = 1199
export const COMPACT_LAYOUT_QUERY =
  `(max-width: ${COMPACT_MAX_WIDTH}px), (max-height: 520px) and (pointer: coarse)`

type ViewportKind = 'phone' | 'tablet' | 'desktop'
type Orientation = 'portrait' | 'landscape'

const layoutWidth = ref(0)
const layoutHeight = ref(0)
const visualWidth = ref(0)
const visualHeight = ref(0)
const visualOffsetTop = ref(0)
const visualScale = ref(1)
const keyboardInset = ref(0)
const isPinchZoomed = ref(false)
const isKeyboardOpen = ref(false)
const isCoarsePointer = ref(false)
const orientation = ref<Orientation>('portrait')
const viewportKind = ref<ViewportKind>('desktop')
const isPhone = ref(false)
const isTablet = ref(false)
const isDesktop = ref(true)
const isCompact = ref(false)

let consumers = 0
let listening = false
let animationFrame: number | null = null
let coarsePointerQuery: MediaQueryList | null = null
const focusMeasureTimers = new Set<number>()

export function matchesCompactLayout(): boolean {
  return typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia(COMPACT_LAYOUT_QUERY).matches
}

const applyRootMetrics = () => {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  root.style.setProperty('--app-layout-width', `${layoutWidth.value}px`)
  root.style.setProperty('--app-layout-height', `${layoutHeight.value}px`)
  root.style.setProperty('--app-viewport-width', `${visualWidth.value}px`)
  root.style.setProperty('--app-viewport-height', `${visualHeight.value}px`)
  root.style.setProperty('--app-viewport-offset-top', `${visualOffsetTop.value}px`)
  root.style.setProperty('--app-visual-scale', String(visualScale.value))
  root.style.setProperty('--app-keyboard-inset', `${keyboardInset.value}px`)
  root.dataset.viewport = viewportKind.value
  root.dataset.orientation = orientation.value
  root.classList.toggle('app-compact-viewport', isCompact.value)
  root.classList.toggle('app-pinch-zoomed', isPinchZoomed.value)
  root.classList.toggle('app-keyboard-open', isKeyboardOpen.value)
  root.classList.toggle('app-coarse-pointer', isCoarsePointer.value)
}

export function measureResponsiveViewport() {
  if (typeof window === 'undefined') return
  const viewport = window.visualViewport
  const metrics = resolveResponsiveViewportGeometry({
    innerWidth: window.innerWidth,
    innerHeight: window.innerHeight,
    rootZoom: getRootZoom(),
    visualWidth: viewport?.width,
    visualHeight: viewport?.height,
    visualOffsetTop: viewport?.offsetTop,
    visualScale: viewport?.scale,
  })
  const {
    layoutWidth: nextLayoutWidth,
    layoutHeight: nextLayoutHeight,
    visualWidth: nextVisualWidth,
    visualHeight: nextVisualHeight,
    visualOffsetTop: nextOffsetTop,
    visualScale: nextVisualScale,
    keyboardInset: nextKeyboardInset,
    isPinchZoomed: nextIsPinchZoomed,
  } = metrics
  const coarse = coarsePointerQuery?.matches
    ?? window.matchMedia?.('(pointer: coarse)').matches
    ?? false
  const landscape = nextLayoutWidth > nextLayoutHeight
  const compact = nextLayoutWidth <= COMPACT_MAX_WIDTH || (coarse && landscape && nextLayoutHeight <= 520)
  const phone = nextLayoutWidth <= PHONE_MAX_WIDTH || (coarse && landscape && nextLayoutHeight <= 520)

  layoutWidth.value = nextLayoutWidth
  layoutHeight.value = nextLayoutHeight
  visualWidth.value = nextVisualWidth
  visualHeight.value = nextVisualHeight
  visualOffsetTop.value = nextOffsetTop
  visualScale.value = nextVisualScale
  keyboardInset.value = nextKeyboardInset
  isPinchZoomed.value = nextIsPinchZoomed
  isCoarsePointer.value = coarse
  orientation.value = landscape ? 'landscape' : 'portrait'
  isCompact.value = compact
  isPhone.value = phone
  isTablet.value = !phone && nextLayoutWidth <= TABLET_MAX_WIDTH
  isDesktop.value = nextLayoutWidth > TABLET_MAX_WIDTH
  viewportKind.value = phone ? 'phone' : isTablet.value ? 'tablet' : 'desktop'
  isKeyboardOpen.value = !nextIsPinchZoomed
    && coarse
    && nextKeyboardInset > 100
    && nextVisualHeight < nextLayoutHeight * 0.86

  applyRootMetrics()
}

const scheduleMeasure = () => {
  if (!listening || animationFrame !== null || typeof window === 'undefined') return
  animationFrame = window.requestAnimationFrame(() => {
    animationFrame = null
    measureResponsiveViewport()
  })
}

const addMediaListener = (query: MediaQueryList, listener: () => void) => {
  if (typeof query.addEventListener === 'function') query.addEventListener('change', listener)
  else query.addListener?.(listener)
}

const removeMediaListener = (query: MediaQueryList, listener: () => void) => {
  if (typeof query.removeEventListener === 'function') query.removeEventListener('change', listener)
  else query.removeListener?.(listener)
}

const scheduleDelayedMeasure = (delay: number) => {
  const timer = window.setTimeout(() => {
    focusMeasureTimers.delete(timer)
    scheduleMeasure()
  }, delay)
  focusMeasureTimers.add(timer)
}

const handleFocusChange = () => {
  scheduleMeasure()
  scheduleDelayedMeasure(80)
  scheduleDelayedMeasure(240)
}

const startListening = () => {
  if (listening || typeof window === 'undefined') return
  listening = true
  coarsePointerQuery = window.matchMedia?.('(pointer: coarse)') ?? null
  window.addEventListener('resize', scheduleMeasure, { passive: true })
  window.addEventListener('orientationchange', scheduleMeasure, { passive: true })
  window.addEventListener('pageshow', scheduleMeasure, { passive: true })
  window.visualViewport?.addEventListener('resize', scheduleMeasure, { passive: true })
  window.visualViewport?.addEventListener('scroll', scheduleMeasure, { passive: true })
  document.addEventListener('focusin', handleFocusChange, true)
  document.addEventListener('focusout', handleFocusChange, true)
  if (coarsePointerQuery) addMediaListener(coarsePointerQuery, scheduleMeasure)
  measureResponsiveViewport()
}

const stopListening = () => {
  if (!listening || typeof window === 'undefined') return
  listening = false
  window.removeEventListener('resize', scheduleMeasure)
  window.removeEventListener('orientationchange', scheduleMeasure)
  window.removeEventListener('pageshow', scheduleMeasure)
  window.visualViewport?.removeEventListener('resize', scheduleMeasure)
  window.visualViewport?.removeEventListener('scroll', scheduleMeasure)
  document.removeEventListener('focusin', handleFocusChange, true)
  document.removeEventListener('focusout', handleFocusChange, true)
  if (coarsePointerQuery) removeMediaListener(coarsePointerQuery, scheduleMeasure)
  coarsePointerQuery = null
  if (animationFrame !== null) {
    window.cancelAnimationFrame(animationFrame)
    animationFrame = null
  }
  focusMeasureTimers.forEach(timer => window.clearTimeout(timer))
  focusMeasureTimers.clear()
}

export function retainResponsiveViewport() {
  consumers += 1
  if (consumers === 1) startListening()
  let released = false
  return () => {
    if (released) return
    released = true
    consumers = Math.max(0, consumers - 1)
    if (consumers === 0) stopListening()
  }
}

if (typeof window !== 'undefined') measureResponsiveViewport()

export function useResponsiveViewport() {
  let release: (() => void) | null = null
  onMounted(() => { release = retainResponsiveViewport() })
  onUnmounted(() => { release?.(); release = null })

  return {
    layoutWidth: readonly(layoutWidth),
    layoutHeight: readonly(layoutHeight),
    visualWidth: readonly(visualWidth),
    visualHeight: readonly(visualHeight),
    visualOffsetTop: readonly(visualOffsetTop),
    visualScale: readonly(visualScale),
    isPinchZoomed: readonly(isPinchZoomed),
    keyboardInset: readonly(keyboardInset),
    isKeyboardOpen: readonly(isKeyboardOpen),
    isCoarsePointer: readonly(isCoarsePointer),
    orientation: readonly(orientation),
    viewportKind: readonly(viewportKind),
    isPhone: readonly(isPhone),
    isTablet: readonly(isTablet),
    isDesktop: readonly(isDesktop),
    isCompact: readonly(isCompact),
    refresh: measureResponsiveViewport,
  } as const
}
