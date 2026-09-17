import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import {
  StreamAuthError,
  forceReloginRedirect,
  isStreamAuthError,
  refreshAccessTokenShared,
  resetAuthRefreshStateForTests,
  runStreamWithAuthRetry,
} from './authRefresh.ts'

type Store = Record<string, string>

function installBrowser(pathname = '/chat') {
  const store: Store = {}
  Object.defineProperty(globalThis, 'localStorage', {
    value: {
      getItem: (key: string) => (key in store ? store[key] : null),
      setItem: (key: string, value: string) => {
        store[key] = String(value)
      },
      removeItem: (key: string) => {
        delete store[key]
      },
    },
    configurable: true,
    writable: true,
  })
  const location = { pathname, href: `http://localhost${pathname}` }
  Object.defineProperty(globalThis, 'window', {
    value: { location },
    configurable: true,
    writable: true,
  })
  return { store, location }
}

test.afterEach(() => {
  resetAuthRefreshStateForTests()
})

test('isStreamAuthError matches the class and name-only wrappers', () => {
  assert.equal(isStreamAuthError(new StreamAuthError(401)), true)
  assert.equal(isStreamAuthError({ name: 'StreamAuthError', message: 'HTTP 401' }), true)
  assert.equal(isStreamAuthError(new Error('HTTP 401')), false)
  assert.equal(isStreamAuthError('HTTP 401'), false)
})

test('concurrent 401s share a single refresh call', async () => {
  const { store } = installBrowser()
  store.weknora_refresh_token = 'rt-1'
  let calls = 0
  let release!: (value: { success: true; data: { token: string; refreshToken: string } }) => void
  const pending = new Promise<{ success: true; data: { token: string; refreshToken: string } }>(
    (resolve) => {
      release = resolve
    },
  )
  const refresh = async () => {
    calls += 1
    return pending
  }

  const first = refreshAccessTokenShared({ refresh })
  await Promise.resolve()
  const second = refreshAccessTokenShared({ refresh })
  assert.equal(calls, 1)

  release({ success: true, data: { token: 'access-2', refreshToken: 'rt-2' } })
  assert.deepEqual(await Promise.all([first, second]), ['access-2', 'access-2'])
  assert.equal(store.weknora_token, 'access-2')
  assert.equal(store.weknora_refresh_token, 'rt-2')
  assert.equal(calls, 1)
})

test('missing refresh token clears credentials, redirects, and throws an Error', async () => {
  const { store, location } = installBrowser()
  store.weknora_token = 'expired'
  store.weknora_user = '{}'
  store.weknora_selected_tenant_id = '9'

  await assert.rejects(
    () => refreshAccessTokenShared({
      refresh: async () => {
        throw new Error('should not refresh')
      },
      messages: { pleaseRelogin: 'please-relogin', tokenRefreshFailed: 'refresh-failed' },
    }),
    (err: unknown) => err instanceof Error && err.message === 'please-relogin',
  )
  assert.equal(store.weknora_token, undefined)
  assert.equal(store.weknora_selected_tenant_id, undefined)
  assert.equal(location.href, '/login')
})

test('a failed refresh clears the newly irrelevant session and redirects', async () => {
  const { store, location } = installBrowser()
  store.weknora_refresh_token = 'rt-dead'
  store.weknora_token = 'expired'
  store.weknora_selected_tenant_id = '9'

  await assert.rejects(
    () => refreshAccessTokenShared({
      refresh: async () => ({ success: false, message: 'revoked' }),
    }),
    /revoked/,
  )
  assert.equal(store.weknora_refresh_token, undefined)
  assert.equal(store.weknora_selected_tenant_id, undefined)
  assert.equal(location.href, '/login')
})

test('runStreamWithAuthRetry refreshes once and replays with the new token', async () => {
  installBrowser()
  const tokens: string[] = []
  const result = await runStreamWithAuthRetry({
    initialToken: 'expired',
    isEmbed: false,
    isCurrent: () => true,
    refreshAccessToken: async () => 'fresh',
    reloginMessage: 'please-relogin',
    run: async (token) => {
      tokens.push(token)
      if (token === 'expired') throw new StreamAuthError(401)
      return `ok:${token}`
    },
  })
  assert.deepEqual(tokens, ['expired', 'fresh'])
  assert.equal(result, 'ok:fresh')
})

test('embed visitors are not refreshed or redirected', async () => {
  let refreshed = 0
  await assert.rejects(
    () => runStreamWithAuthRetry({
      initialToken: 'embed-token',
      isEmbed: true,
      isCurrent: () => true,
      refreshAccessToken: async () => {
        refreshed += 1
        return 'nope'
      },
      reloginMessage: 'please-relogin',
      run: async () => {
        throw new StreamAuthError(401)
      },
    }),
    (err: unknown) => err instanceof StreamAuthError,
  )
  assert.equal(refreshed, 0)
})

test('a second handshake 401 after a successful refresh does not wipe tokens', async () => {
  const { store, location } = installBrowser()
  store.weknora_token = 'expired'
  store.weknora_refresh_token = 'rt-1'
  location.href = 'http://localhost/chat'

  await assert.rejects(
    () => runStreamWithAuthRetry({
      initialToken: 'expired',
      isEmbed: false,
      isCurrent: () => true,
      refreshAccessToken: async () => {
        const token = await refreshAccessTokenShared({
          refresh: async () => ({
            success: true,
            data: { token: 'fresh', refreshToken: 'rt-2' },
          }),
        })
        return token
      },
      reloginMessage: 'please-relogin',
      run: async () => {
        throw new StreamAuthError(401)
      },
    }),
    (err: unknown) => err instanceof Error && err.message === 'please-relogin',
  )
  assert.equal(store.weknora_token, 'fresh')
  assert.equal(store.weknora_refresh_token, 'rt-2')
  assert.equal(location.href, 'http://localhost/chat')
})

test('a superseded send skips the replay', async () => {
  let runs = 0
  const result = await runStreamWithAuthRetry({
    initialToken: 'expired',
    isEmbed: false,
    isCurrent: () => false,
    refreshAccessToken: async () => 'fresh',
    reloginMessage: 'please-relogin',
    run: async () => {
      runs += 1
      throw new StreamAuthError(401)
    },
  })
  assert.equal(runs, 1)
  assert.equal(result, undefined)
})

test('forceReloginRedirect does not bounce embed visitors to /login', () => {
  const { store, location } = installBrowser('/embed/ch-1')
  store.weknora_token = 'jwt'
  forceReloginRedirect()
  assert.equal(store.weknora_token, undefined)
  assert.equal(location.href, 'http://localhost/embed/ch-1')
})

test('chat stream wires the shared retry and keeps the abort controller', () => {
  const source = readFileSync(new URL('../api/chat/streame.ts', import.meta.url), 'utf8')
  assert.match(source, /runStreamWithAuthRetry/)
  assert.match(source, /isStreamAuthError/)
  assert.match(source, /streamAbort/)
  assert.doesNotMatch(source, /forceReloginRedirect/)
})
