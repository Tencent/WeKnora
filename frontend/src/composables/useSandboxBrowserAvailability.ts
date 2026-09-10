import { ref, watch, type Ref } from 'vue'

export type BrowserCapabilityContext = {
  sessionId: string
  agentId?: string
  agentSourceTenantId?: string | number | null
  visible: boolean
  refreshKey?: unknown
}

// Invalidate immediately on session/agent changes, and discard late responses.
// A refresh for the same session keeps a mounted browser until proven absent.
export function useSandboxBrowserAvailability(
  context: () => BrowserCapabilityContext,
  request: (context: BrowserCapabilityContext) => Promise<boolean>,
): Ref<boolean> {
  const available = ref(false)
  let currentKey = ''
  watch(context, (next, _previous, onCleanup) => {
    const key = JSON.stringify([next.sessionId, next.agentId, next.agentSourceTenantId])
    if (key !== currentKey) {
      currentKey = key
      available.value = false
    }
    let cancelled = false
    onCleanup(() => { cancelled = true })
    if (!next.visible || !next.sessionId) return
    void request(next).then(value => {
      if (!cancelled) available.value = value
    }).catch(() => {
      // A transient metadata failure is not evidence that a live browser vanished.
    })
  }, { immediate: true, flush: 'sync' })
  return available
}
