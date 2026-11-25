/** Resolve the public base URL used in embed iframe / widget URLs: the origin
 * plus the deployment's subpath prefix, if any.
 *
 * Resolution order: the __RUNTIME_CONFIG__ EMBED_BASE_URL (dedicated embed
 * origin), VITE_EMBED_BASE_URL, then the current origin plus the Vite base
 * path the SPA was built with ('' at root, '/weknora' under a prefix).
 * A configured value's path is kept, so EMBED_BASE_URL=https://host/weknora
 * yields https://host/weknora instead of dropping the prefix.
 */
function stripTrailingSlash(path: string): string {
  return path.replace(/\/+$/, '')
}

function originWithPath(raw: string | undefined): string {
  if (!raw) return ''
  try {
    const u = new URL(raw, window.location.href)
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return ''
    return u.origin + stripTrailingSlash(u.pathname)
  } catch {
    return ''
  }
}

export function resolveEmbedBaseUrl(): string {
  if (typeof window === 'undefined') return ''
  const runtime = (window as Window & { __RUNTIME_CONFIG__?: { EMBED_BASE_URL?: string } })
    .__RUNTIME_CONFIG__
  const fromRuntime = originWithPath(runtime?.EMBED_BASE_URL?.trim())
  if (fromRuntime) return fromRuntime
  const fromEnv = originWithPath(import.meta.env?.VITE_EMBED_BASE_URL?.trim())
  if (fromEnv) return fromEnv
  return window.location.origin + stripTrailingSlash(import.meta.env?.BASE_URL || '/')
}
