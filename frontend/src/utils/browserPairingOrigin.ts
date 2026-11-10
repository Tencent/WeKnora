const loopbackHosts = new Set(['localhost', '127.0.0.1', '::1'])

function isLoopbackHostname(hostname: string): boolean {
  return loopbackHosts.has(hostname.replace(/^\[|\]$/g, '').toLowerCase())
}

// Native GetAPIBaseURL is the desktop listener. The page global is only a
// fallback, because any script in the Lite window can overwrite it.
export function preferredDesktopAPIBase(nativeBase?: string | null, injectedBase?: string | null): string {
  const native = (nativeBase ?? '').trim()
  if (native) return native
  return (injectedBase ?? '').trim()
}

// The pairing link is a WebSocket the Chrome extension dials. A normal site
// uses the page origin so a proxy port is preserved. The Lite window is
// wails://wails.localhost, which cannot become ws/wss, so a loopback desktop
// API base supplies the origin instead.
export function pairingPageOrigin(pageOrigin: string, apiBase?: string): string {
  try {
    const page = new URL(pageOrigin)
    if (page.protocol === 'http:' || page.protocol === 'https:') {
      return page.origin
    }
  } catch {
    /* custom schemes and empty origins fall through */
  }
  const base = (apiBase || '').trim()
  if (!base) {
    return pageOrigin
  }
  try {
    const api = new URL(base)
    if ((api.protocol !== 'http:' && api.protocol !== 'https:') || !isLoopbackHostname(api.hostname)) {
      return pageOrigin
    }
    return api.origin
  } catch {
    return pageOrigin
  }
}
