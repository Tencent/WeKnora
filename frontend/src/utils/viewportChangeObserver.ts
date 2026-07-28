export interface ViewportChangeObserverOptions {
  captureScroll?: boolean
  immediate?: boolean
}

/**
 * Observe layout and VisualViewport changes through one animation-frame-throttled callback.
 * The returned cleanup function is idempotent and cancels any queued callback.
 */
export function observeViewportChanges(
  callback: () => void,
  options: ViewportChangeObserverOptions = {},
): () => void {
  if (typeof window === 'undefined') return () => undefined

  const captureScroll = options.captureScroll ?? true
  let active = true
  let frame: number | null = null

  const schedule = () => {
    if (!active || frame !== null) return
    frame = window.requestAnimationFrame(() => {
      frame = null
      if (active) callback()
    })
  }

  window.addEventListener('resize', schedule, { passive: true })
  window.addEventListener('scroll', schedule, { passive: true, capture: captureScroll })
  window.visualViewport?.addEventListener('resize', schedule, { passive: true })
  window.visualViewport?.addEventListener('scroll', schedule, { passive: true })

  if (options.immediate) schedule()

  return () => {
    if (!active) return
    active = false
    window.removeEventListener('resize', schedule)
    window.removeEventListener('scroll', schedule, { capture: captureScroll })
    window.visualViewport?.removeEventListener('resize', schedule)
    window.visualViewport?.removeEventListener('scroll', schedule)
    if (frame !== null) {
      window.cancelAnimationFrame(frame)
      frame = null
    }
  }
}
