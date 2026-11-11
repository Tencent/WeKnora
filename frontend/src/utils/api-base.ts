export function getApiBaseUrl(): string {
  // LocalHub plugin patch (2026-04-29): respect vite's BASE_URL so that
  // axios calls work at `/app/weknora/` (LocalHub reverse proxy). Without
  // this · axios hits `/api/v1/...` at LocalHub root · gets 404 "Cannot
  // POST". Strip trailing slash so axios doesn't produce `/app/weknora//api/v1/...`.
  // See: plugins/weknora/patches/api-base-baseurl.patch
  const base = (import.meta.env?.BASE_URL || '/').replace(/\/+$/, '');
  return base;
}

/**
 * Origin of the embedded backend when running inside the Wails desktop shell,
 * or '' elsewhere / when the backend listener is not up yet.
 *
 * Why uploads need this: the desktop webview serves the SPA through the Wails
 * asset server, which intercepts every request to the app origin. JSON bodies
 * survive that hop, but multipart uploads do not — WebView2 hands the
 * interceptor a streaming body whose file parts read back empty, so every
 * upload landed as a 0-byte file (identical empty-content MD5, which then
 * tripped the duplicate-file check on the next upload). The loopback listener
 * is reachable from the page and the backend CORS config allows any origin,
 * so large-body requests go straight to http://127.0.0.1:<port> instead of
 * through the interceptor.
 */
let desktopApiOriginCache: string | undefined;

export function getDesktopApiOrigin(): string {
  if (desktopApiOriginCache !== undefined) return desktopApiOriginCache;
  try {
    const url: unknown = (window as any).go?.main?.App?.GetAPIBaseURL?.();
    if (typeof url === 'string' && url.startsWith('http')) {
      // The binding returns .../api/v1; axios paths already include /api/v1.
      // Cache only a success: the binding reports '' until the embedded
      // listener has picked its port, and a cached miss would pin uploads to
      // the interceptor for the whole session.
      desktopApiOriginCache = url.replace(/\/api\/v1\/?$/, '');
    }
  } catch {
    // Not in the desktop shell (window.go absent) — retry cost is negligible.
  }
  return desktopApiOriginCache ?? '';
}
