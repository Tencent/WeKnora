export interface McpOAuthAuthorization {
  authorizationUrl: string
  authorizationAttempt: string
}

export interface McpOAuthPopupWindow {
  open: (url: string, target?: string, features?: string) => { closed: boolean; close: () => void } | null
  setInterval: (handler: () => void | Promise<void>, timeout: number) => number
  clearInterval: (id: number) => void
}

export type McpOAuthPopupResult =
  | { status: 'opened'; stop: () => void }
  | { status: 'inactive' }
  | { status: 'missing' }

const POPUP_NAME = 'mcp_oauth'
const POPUP_FEATURES = 'width=600,height=720'
const POLL_MS = 1500

/** The callback page tells the already-open app that consent finished. */
export const MCP_OAUTH_RETURN_CHANNEL = 'weknora-mcp-oauth'

/**
 * The consent popup lands here after the provider redirects back. Tell the
 * page that opened it, then close the popup.
 */
export function notifyOpenerOfMcpOAuthReturn(): boolean {
  const hash = window.location.hash.startsWith('#') ? window.location.hash.slice(1) : ''
  if (!hash.includes('mcp_oauth_result=') && !hash.includes('mcp_oauth_error=')) return false
  const ok = new URLSearchParams(hash).get('mcp_oauth_result') === 'success'
  try {
    const channel = new BroadcastChannel(MCP_OAUTH_RETURN_CHANNEL)
    channel.postMessage({ ok })
    channel.close()
  } catch {
    /* private mode has no channel; the opener is polling the API anyway */
  }
  const path = `${window.location.pathname}${window.location.search}`
  window.history.replaceState({}, document.title, path || '/')
  if (window.opener) {
    try { window.close() } catch { /* the browser may refuse */ }
  }
  return true
}

/**
 * Opens the consent popup and polls until this authorization attempt succeeds.
 * The attempt id is sent on every poll so an older stored token is not treated
 * as fresh consent.
 *
 * The popup closing is not a cancel: the provider can sever the window while
 * it navigates, and the callback page reports completion on its own. stop()
 * drops a late poll result. isActive() lets the caller ignore a result after
 * the drawer closes, the service changes, or a newer attempt starts.
 */
export async function openMcpOAuthPopup(options: {
  win: McpOAuthPopupWindow
  serviceId: string
  redirectUri: string
  frontendRedirect: string
  authorize: (
    serviceId: string,
    body: { redirect_uri: string; frontend_redirect: string },
  ) => Promise<McpOAuthAuthorization>
  pollStatus: (serviceId: string, authorizationAttempt: string) => Promise<boolean>
  isActive?: () => boolean
  onStatus?: (authorized: boolean) => void
  onAuthorized?: () => void | Promise<void>
  onClosed?: () => void
}): Promise<McpOAuthPopupResult> {
  const authorization = await options.authorize(options.serviceId, {
    redirect_uri: options.redirectUri,
    frontend_redirect: options.frontendRedirect,
  })
  if (options.isActive && !options.isActive()) return { status: 'inactive' }
  if (!authorization.authorizationUrl || !authorization.authorizationAttempt) return { status: 'missing' }

  const popup = options.win.open(authorization.authorizationUrl, POPUP_NAME, POPUP_FEATURES)
  let stopped = false
  let timer: number | null = null
  let channel: BroadcastChannel | null = null
  const stop = () => {
    if (stopped) return
    stopped = true
    if (timer !== null) {
      options.win.clearInterval(timer)
      timer = null
    }
    channel?.close()
    channel = null
  }
  const stale = () => stopped || (options.isActive ? !options.isActive() : false)
  const tick = async () => {
    if (stale()) return
    let authorized: boolean | undefined
    try {
      authorized = await options.pollStatus(options.serviceId, authorization.authorizationAttempt)
    } catch {
      authorized = undefined
    }
    if (stale()) return
    if (authorized !== undefined) options.onStatus?.(authorized)
    if (!authorized) return
    stop()
    try { popup?.close() } catch { /* cross-origin close may throw */ }
    await options.onAuthorized?.()
  }

  if (typeof window !== 'undefined') {
    try {
      channel = new BroadcastChannel(MCP_OAUTH_RETURN_CHANNEL)
      channel.onmessage = () => { void tick() }
    } catch {
      channel = null
    }
  }
  timer = options.win.setInterval(() => { void tick() }, POLL_MS)

  return { status: 'opened', stop }
}
