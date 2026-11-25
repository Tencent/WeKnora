import { getAbsoluteFrontendBaseUrl } from './api-base'

/**
 * Normalize a configured embed base URL to `origin + path`.
 *
 * The configured value is authoritative: when `EMBED_BASE_URL` is set we do NOT
 * append the build-time app sub-path (Vite BASE_URL), so a dedicated embed
 * origin behaves exactly as it does in a root-path deployment:
 *   - `https://embed.example.com`   -> `https://embed.example.com`
 *   - `https://embed.example.com/`  -> `https://embed.example.com`
 *   - `https://embed.example.com/p` -> `https://embed.example.com/p`
 *
 * Only absolute http(s) URLs and root-relative paths are accepted. Anything
 * else (a scheme-less typo like `embed.example.com`, `javascript:`…) returns
 * null so the caller falls back to the default embed base.
 */
export function normalizeEmbedBaseUrl(raw: string, pageHref?: string): string | null {
  const trimmed = raw.trim()
  if (!trimmed) return null
  // `new URL('embed.example.com', pageHref)` would silently resolve a missing
  // scheme against the current page path and yield a bogus path, so require an
  // explicit scheme or a leading `/` (root-relative) before parsing.
  if (!/^https?:\/\//i.test(trimmed) && !trimmed.startsWith('/')) return null
  try {
    const u = new URL(trimmed, pageHref)
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return null
    const pathname = u.pathname.replace(/\/+$/, '')
    return `${u.origin}${pathname}`
  } catch {
    return null
  }
}

/**
 * Resolve the public base (origin + optional path) used in embed iframe /
 * widget URLs. `EMBED_BASE_URL` / `VITE_EMBED_BASE_URL` may point at a
 * dedicated embed origin and may include a path prefix (e.g.
 * `https://embed.example.com/weknora/`); when set, the value is used as-is and
 * the build-time sub-path is not applied. Without an override the current
 * origin plus the app's Vite BASE_URL is used, so sub-path deployments
 * (e.g. `/app/weknora/`) produce working embed URLs.
 */
export function resolveEmbedBaseUrl(): string {
  if (typeof window === 'undefined') return ''
  const runtime = (window as Window & { __RUNTIME_CONFIG__?: { EMBED_BASE_URL?: string } }).__RUNTIME_CONFIG__
  const fromRuntime = runtime?.EMBED_BASE_URL?.trim()
  if (fromRuntime) {
    const resolved = normalizeEmbedBaseUrl(fromRuntime, window.location.href)
    if (resolved) return resolved
  }
  const fromEnv = import.meta.env?.VITE_EMBED_BASE_URL?.trim()
  if (fromEnv) {
    const resolved = normalizeEmbedBaseUrl(fromEnv, window.location.href)
    if (resolved) return resolved
  }
  return getAbsoluteFrontendBaseUrl()
}
