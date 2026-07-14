/** Resolve the public origin used in embed iframe / widget URLs. */
export function resolveEmbedBaseUrl(): string {
  if (typeof window === 'undefined') return ''
  const runtime = (window as Window & { __RUNTIME_CONFIG__?: { EMBED_BASE_URL?: string } }).__RUNTIME_CONFIG__
  const fromRuntime = runtime?.EMBED_BASE_URL?.trim()
  if (fromRuntime) {
    try {
      const u = new URL(fromRuntime, window.location.href)
      if (u.protocol === 'http:' || u.protocol === 'https:') return u.origin
    } catch {
      // fall through
    }
  }
  const fromEnv = import.meta.env.VITE_EMBED_BASE_URL?.trim()
  if (fromEnv) {
    try {
      const u = new URL(fromEnv, window.location.href)
      if (u.protocol === 'http:' || u.protocol === 'https:') return u.origin
    } catch {
      // fall through
    }
  }
  return window.location.origin
}
