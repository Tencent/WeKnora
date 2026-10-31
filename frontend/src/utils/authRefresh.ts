/**
 * Shared access-token refresh used by both axios and the chat SSE client.
 *
 * Refresh tokens rotate, so concurrent 401s must queue behind one in-flight
 * /auth/refresh. The SSE handshake runs on raw fetch and cannot use the
 * axios interceptor; both planes call {@link refreshAccessTokenShared}.
 */

const AUTH_STORAGE_KEYS = [
  'weknora_token',
  'weknora_refresh_token',
  'weknora_user',
  'weknora_tenant',
  'weknora_knowledge_bases',
  'weknora_current_kb',
  'weknora_selected_tenant_id',
  'weknora_selected_tenant_name',
  'weknora_memberships',
] as const

type AuthSession = {
  generation: number
  accessToken: string | null
  refreshToken: string | null
}

let sessionGeneration = 0
let activeRefresh: { session: AuthSession; promise: Promise<string> } | null = null

export class AuthSessionChangedError extends Error {
  constructor() {
    super('Authentication session changed during token refresh')
    this.name = 'AuthSessionChangedError'
  }
}

/** Detach pending refreshes when the local session is explicitly ended. */
export function invalidateAuthRefresh() {
  sessionGeneration += 1
  activeRefresh = null
}

function sameSession(a: AuthSession, b: AuthSession): boolean {
  return a.generation === b.generation
    && a.accessToken === b.accessToken
    && a.refreshToken === b.refreshToken
}

function assertCurrentSession(session: AuthSession) {
  if (activeRefresh?.session !== session || session.generation !== sessionGeneration) {
    throw new AuthSessionChangedError()
  }
  // Storage may have changed in another tab, without running our logout hook.
  if (localStorage.getItem('weknora_refresh_token') !== session.refreshToken
    || localStorage.getItem('weknora_token') !== session.accessToken) {
    throw new AuthSessionChangedError()
  }
}

export type TokenRefreshResult = {
  success: boolean
  data?: { token: string; refreshToken: string }
  message?: string
}

export type RefreshAccessTokenOptions = {
  refresh?: (refreshToken: string) => Promise<TokenRefreshResult>
  messages?: {
    pleaseRelogin: string
    tokenRefreshFailed: string
  }
}

const defaultMessages = {
  pleaseRelogin: 'Please log in again',
  tokenRefreshFailed: 'Token refresh failed',
}

export class StreamAuthError extends Error {
  constructor(readonly status: number) {
    super(`HTTP ${status}`)
    this.name = 'StreamAuthError'
  }
}

export function isStreamAuthError(err: unknown): boolean {
  if (!err || typeof err !== 'object') return false
  if (err instanceof StreamAuthError) return true
  return (err as { name?: string }).name === 'StreamAuthError'
}

export function isEmbedPage(): boolean {
  if (typeof window === 'undefined') return false
  return window.location.pathname.startsWith('/embed/')
}

export function redirectToLogin() {
  if (typeof window === 'undefined') return
  if (window.location.pathname === '/login') return
  if (isEmbedPage()) return
  window.location.href = '/login'
}

export function clearAuthStorage() {
  invalidateAuthRefresh()
  for (const key of AUTH_STORAGE_KEYS) {
    try {
      localStorage.removeItem(key)
    } catch {
      // Storage may be unavailable. Still attempt the remaining keys and
      // allow callers to reject waiting requests and redirect to login.
    }
  }
}

export function forceReloginRedirect() {
  clearAuthStorage()
  redirectToLogin()
}

async function defaultRefresh(refreshToken: string): Promise<TokenRefreshResult> {
  const { refreshToken: refreshTokenAPI } = await import('../api/auth/index')
  return refreshTokenAPI(refreshToken)
}

/**
 * Refresh the access token, de-duplicated within the current session.
 *
 * Only a still-current session can persist tokens or clear credentials on
 * failure. Superseded callers reject without changing the replacement session.
 */
export async function refreshAccessTokenShared(
  options: RefreshAccessTokenOptions = {},
): Promise<string> {
  const messages = { ...defaultMessages, ...options.messages }
  const refresh = options.refresh ?? defaultRefresh

  let session: AuthSession
  try {
    session = {
      generation: sessionGeneration,
      accessToken: localStorage.getItem('weknora_token'),
      refreshToken: localStorage.getItem('weknora_refresh_token'),
    }
  } catch (error) {
    forceReloginRedirect()
    throw error
  }

  if (activeRefresh && sameSession(activeRefresh.session, session)) {
    return activeRefresh.promise
  }

  const promise = (async () => {
    // Install the attempt before running code that can fail synchronously.
    await Promise.resolve()
    let committing = false
    try {
      assertCurrentSession(session)
      if (!session.refreshToken) throw new Error(messages.pleaseRelogin)
      const response = await refresh(session.refreshToken)
      assertCurrentSession(session)
      if (!response.success || !response.data?.token) {
        throw new Error(response.message || messages.tokenRefreshFailed)
      }

      const { token, refreshToken: newRefreshToken } = response.data
      committing = true
      localStorage.setItem('weknora_token', token)
      if (newRefreshToken) localStorage.setItem('weknora_refresh_token', newRefreshToken)
      return token
    } catch (error) {
      if (error instanceof AuthSessionChangedError) throw error
      if (activeRefresh?.session !== session || session.generation !== sessionGeneration) {
        throw new AuthSessionChangedError()
      }
      if (!committing) {
        try {
          assertCurrentSession(session)
        } catch (sessionError) {
          if (sessionError instanceof AuthSessionChangedError) throw sessionError
          // Inaccessible storage keeps the existing fail-closed cleanup policy.
        }
      }
      clearAuthStorage()
      redirectToLogin()
      throw error
    } finally {
      // An old attempt must never release a replacement session's refresh lock.
      if (activeRefresh?.session === session) activeRefresh = null
    }
  })()
  activeRefresh = { session, promise }
  return promise
}

/**
 * Replay an SSE handshake once after a 401. Refresh failure already
 * redirected; a second 401 after a successful refresh is surfaced to the
 * caller without wiping the newly minted session (axios does the same).
 *
 * Returns undefined when a newer send or an abort superseded the replay.
 */
export async function runStreamWithAuthRetry<T>(options: {
  run: (token: string) => Promise<T>
  initialToken: string
  isEmbed: boolean
  isCurrent: () => boolean
  refreshAccessToken: () => Promise<string>
  reloginMessage: string
}): Promise<T | undefined> {
  try {
    return await options.run(options.initialToken)
  } catch (err) {
    if (!isStreamAuthError(err) || options.isEmbed) throw err

    let refreshedToken: string
    try {
      refreshedToken = await options.refreshAccessToken()
    } catch {
      throw new Error(options.reloginMessage)
    }

    if (!options.isCurrent()) return undefined

    try {
      return await options.run(refreshedToken)
    } catch (retryErr) {
      if (isStreamAuthError(retryErr)) {
        throw new Error(options.reloginMessage)
      }
      throw retryErr
    }
  }
}

/** Reset module locks between unit tests. */
export function resetAuthRefreshStateForTests() {
  invalidateAuthRefresh()
}
