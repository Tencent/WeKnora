/** Normalize an absolute http(s) URL to origin + path without a trailing slash; '' when invalid. */
export function normalizeMcpBaseUrl(raw?: string): string {
  const value = raw?.trim()
  if (!value) return ''
  try {
    const u = new URL(value)
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return ''
    return `${u.origin}${u.pathname}`.replace(/\/+$/, '')
  } catch {
    return ''
  }
}

/** Resolve the public base URL shown for MCP Server endpoints; '' falls back to the API origin. */
export function resolveMcpBaseUrl(): string {
  if (typeof window === 'undefined') return ''
  const runtime = (window as Window & { __RUNTIME_CONFIG__?: { MCP_BASE_URL?: string } }).__RUNTIME_CONFIG__
  return normalizeMcpBaseUrl(runtime?.MCP_BASE_URL) || normalizeMcpBaseUrl(import.meta.env.VITE_MCP_BASE_URL)
}
