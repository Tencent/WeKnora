import { onScopeDispose, watch, type Ref } from 'vue'

/** Animate live title changes, while keeping initial loads and session switches still. */
export function useSessionTitleMotion(
  element: Ref<HTMLElement | null>,
  sessionId: () => string | undefined,
  title: () => string,
) {
  let animation: Animation | undefined

  watch([sessionId, title], ([id, text], [previousId, previousText]) => {
    animation?.cancel()
    animation = undefined

    if (!id || id !== previousId || !text.trim() || text.trim() === previousText.trim()) return
    if (typeof window === 'undefined' || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    animation = element.value?.animate?.(
      [
        { opacity: 0, transform: 'translateY(5px)' },
        { opacity: 1, transform: 'translateY(0)' },
      ],
      { duration: 360, easing: 'cubic-bezier(0.22, 1, 0.36, 1)' },
    )
  }, { flush: 'post' })

  onScopeDispose(() => animation?.cancel())
}
