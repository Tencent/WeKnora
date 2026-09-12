import assert from 'node:assert/strict'
import { Buffer } from 'node:buffer'
import { fileURLToPath } from 'node:url'
import test, { beforeEach } from 'node:test'

import { build, type Plugin } from 'esbuild'

interface RequestModule {
  refreshAccessTokenShared: (options?: {refresh?: (token: string) => Promise<{success: boolean; data?: {token: string; refreshToken: string}}>}) => Promise<string>
  getDown: (
    url: string,
    config?: { signal?: AbortSignal; timeout?: number; headers?: Record<string, string> },
  ) => Promise<Blob>
}

const virtualDependencies: Plugin = {
  name: 'request-test-dependencies',
  setup(builder) {
    const modules: Record<string, string> = {
      axios: `
        const client = Object.assign(async config => {
          globalThis.__requestRetries.push(config)
          return globalThis.__requestRetry(config)
        }, {
          delete: async () => ({}),
          get: async (url, config) => {
            globalThis.__requestGetCalls?.push({ url, config })
            return globalThis.__requestSuccess({status: 200, data: new Blob(['download']), config})
          },
          interceptors: {
            request: { use(handler) { globalThis.__requestBefore = handler } },
            response: { use(success, failure) { globalThis.__requestSuccess = success; globalThis.__requestFailure = failure } },
          },
          patch: async () => ({}),
          post: async () => ({}),
          put: async () => ({}),
        })
        class CanceledError extends Error { code = 'ERR_CANCELED'; __CANCEL__ = true }
        export default { create: () => client, CanceledError, isCancel: error => !!error?.__CANCEL__ }
      `,
      '@/i18n': `export default {
        global: { locale: { value: 'en-US' }, t: (key, params) => params ? key + ':' + JSON.stringify(params) : key }
      }`,
      './index': `
        export const MAX_FILE_SIZE_MB = 50
        export const MAX_SKILL_BUNDLE_SIZE_MB = 256
        export const generateRandomString = () => 'request-id'
      `,
      './api-base': `export const getApiBaseUrl = () => ''`,
      '../api/auth/index': `export const refreshToken = async token => { globalThis.__requestRefreshCalls++; return globalThis.__requestRefresh(token) }`,
    }

    builder.onResolve({ filter: /^(?:axios|@\/i18n|\.\/index|\.\/api-base|\.\.\/api\/auth\/index)$/ }, args => ({
      path: args.path,
      namespace: 'request-test',
    }))
    builder.onLoad({ filter: /.*/, namespace: 'request-test' }, args => ({
      contents: modules[args.path],
      loader: 'js',
    }))
  },
}

async function loadRequestModule(): Promise<RequestModule> {
  const entry = fileURLToPath(new URL('./request.ts', import.meta.url))
  const result = await build({
    entryPoints: [entry],
    bundle: true,
    format: 'esm',
    logLevel: 'silent',
    platform: 'node',
    plugins: [virtualDependencies],
    target: 'node22',
    write: false,
  })
  const source = result.outputFiles[0].text
  const url = `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`
  return import(url) as Promise<RequestModule>
}

test('Axios and SSE consumers share one refresh and settle after provider failure', async () => {
  const { refreshAccessTokenShared } = await loadRequestModule()
  storage.set('weknora_refresh_token', 'refresh')
  let release!: () => void
  const ready = new Promise<void>(resolve => { release = resolve })
  state.__requestRefresh = async () => { await ready; throw new Error('provider unavailable') }
  const axiosWaiter = state.__requestFailure(httpError(401))
  const streamWaiter = refreshAccessTokenShared()
  release()
  const results = await Promise.allSettled([axiosWaiter, streamWaiter])
  assert.equal(state.__requestRefreshCalls, 1)
  assert.ok(results.every(result => result.status === 'rejected'))
  assert.equal(storage.size, 0)
  storage.set('weknora_refresh_token', 'replacement')
  state.__requestRefresh = async () => ({success: true, data: {token: 'fresh', refreshToken: 'next'}})
  assert.equal(await refreshAccessTokenShared(), 'fresh', 'a failed cycle releases the shared lock')
})

test('Axios and SSE consumers reuse the same rotated access token', async () => {
  const { refreshAccessTokenShared } = await loadRequestModule()
  storage.set('weknora_refresh_token', 'refresh')
  let release!: () => void
  const ready = new Promise<void>(resolve => { release = resolve })
  state.__requestRefresh = async () => { await ready; return {success: true, data: {token: 'fresh', refreshToken: 'next'}} }
  const axiosWaiter = state.__requestFailure(httpError(401))
  const streamWaiter = refreshAccessTokenShared()
  release()
  const [, token] = await Promise.all([axiosWaiter, streamWaiter])
  assert.equal(token, 'fresh')
  assert.equal(state.__requestRefreshCalls, 1)
  assert.equal(state.__requestRetries[0].headers.Authorization, 'Bearer fresh')
})

test('blob downloads override the client default with caller timeout and AbortSignal', async () => {
  const { getDown } = await loadRequestModule()
  const calls: Array<{
    config: { responseType: string; signal?: AbortSignal; timeout?: number; headers?: Record<string, string> }
    url: string
  }> = []
  ;(globalThis as any).__requestGetCalls = calls
  const controller = new AbortController()

  await getDown('/large-export', { signal: controller.signal, timeout: 600_000, headers: {'X-Export': 'audit'} })

  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, '/large-export')
  assert.equal(calls[0].config.responseType, 'blob')
  assert.equal(calls[0].config.timeout, 600_000)
  assert.equal(calls[0].config.signal, controller.signal)
  assert.deepEqual(calls[0].config.headers, {'X-Export': 'audit'})
})

const state = globalThis as any
const storage = new Map<string, string>()
beforeEach(async () => {
  await loadRequestModule()
  storage.clear()
  Object.defineProperty(globalThis, 'localStorage', {configurable: true, value: {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
    removeItem: (key: string) => storage.delete(key),
  }})
  state.window = {location: {pathname: '/platform/evaluations', href: ''}}
  state.__requestRetries = []
  state.__requestRefreshCalls = 0
  state.__requestRefresh = async () => ({success: true, data: {token: 'fresh', refreshToken: 'refresh-new'}})
  state.__requestRetry = async () => ({success: true})
})

const httpError = (status: number, data: unknown = {}, config: any = {}) => ({
  config: {url: '/api/v1/models', headers: {}, ...config}, response: {status, data},
})

test('response interceptor preserves objects, arrays, Blob and primitive payload contracts', async () => {
  for (const status of [200, 201, 202]) {
    for (const payload of [{success: true}, ['a'], new Blob(['content'])]) {
      const expected = JSON.stringify(payload)
      const result = state.__requestSuccess({status, data: payload})
      assert.equal(result, payload)
      assert.equal((result as any).$httpStatus, status)
      assert.equal(Object.keys(result).includes('$httpStatus'), false)
      assert.equal('$httpStatus' in {...result}, false)
      assert.equal(JSON.stringify(result), expected)
      assert.deepEqual(Object.getOwnPropertyDescriptor(result, '$httpStatus'), {
        value: status, enumerable: false, writable: false, configurable: true,
      })
    }
  }
  for (const data of [null, undefined, '', 'ok', 0, false]) {
    assert.equal(state.__requestSuccess({status: 204, data}), data)
  }
  await assert.rejects(state.__requestSuccess({status: 304, data: {message: 'unchanged'}}), e => (e as any).$httpStatus === 304)
})

test('HTTP errors retain nested model reference details and authoritative status', async () => {
  const details = {knowledge_bases: [{id: 'kb', bindings: ['embedding_model']}], agents: [], long_term_memory: {bindings: []}}
  await assert.rejects(state.__requestFailure(httpError(400, {success: false, status: 999, error: {code: 2300, message: 'In use', details}})), error => {
    const e = error as any
    assert.equal(e.$httpStatus, 400)
    assert.equal(e.status, 999)
    assert.equal(e.message, 'In use')
    assert.equal(e.error.details, details)
    assert.equal(Object.keys(e).includes('$httpStatus'), false)
    return true
  })
  await assert.rejects(state.__requestFailure(httpError(500, 'server failed')), {message: 'server failed'})
  await assert.rejects(state.__requestFailure(httpError(500, null)), e => (e as any).$httpStatus === 500)
})

test('413 identifies separate regular-file and skill-bundle limits', async () => {
  await assert.rejects(state.__requestFailure(httpError(413, '', {url: '/api/v1/knowledge/file'})), {message: 'error.fileSizeExceeded:{"size":50}'})
  await assert.rejects(state.__requestFailure(httpError(413, '', {url: '/api/v1/skills/catalog'})), {message: 'settings.sandbox.skillBundleTooLarge:{"size":256}'})
})

test('public authentication and embed errors preserve session and never refresh', async () => {
  storage.set('weknora_token', 'existing')
  for (const url of ['/api/v1/auth/login', '/api/v1/auth/invitations/lookup', '/api/v1/embed/chat']) {
    await assert.rejects(state.__requestFailure(httpError(401, {error: 'invalid'}, {url})), {message: 'invalid'})
  }
  state.window.location.pathname = '/embed/test'
  await assert.rejects(state.__requestFailure(httpError(401)), e => (e as any).$httpStatus === 401)
  assert.equal(state.__requestRefreshCalls, 0)
  assert.equal(storage.get('weknora_token'), 'existing')
  assert.equal(state.window.location.href, '')
})

test('actual concurrent 401 interceptors refresh once and retry every caller with its config', async () => {
  storage.set('weknora_refresh_token', 'refresh')
  let release!: () => void
  const ready = new Promise<void>(resolve => {release = resolve})
  state.__requestRefresh = async () => {await ready; return {success: true, data: {token: 'fresh', refreshToken: 'next'}}}
  const signal = new AbortController().signal
  const requests = Array.from({length: 4}, () => state.__requestFailure(httpError(401, {}, {signal, timeout: 600000, responseType: 'blob'})))
  release()
  await Promise.all(requests)
  assert.equal(state.__requestRefreshCalls, 1)
  assert.equal(state.__requestRetries.length, 4)
  for (const config of state.__requestRetries) {
    assert.equal(config.headers.Authorization, 'Bearer fresh')
    assert.equal(config._retry, true)
    assert.equal(config.signal, signal)
    assert.equal(config.timeout, 600000)
    assert.equal(config.responseType, 'blob')
  }
  assert.equal(storage.get('weknora_refresh_token'), 'next')
})

test('missing refresh credentials reject all actual interceptor waiters and clear the session', async () => {
  for (const key of ['weknora_token', 'weknora_user', 'weknora_tenant']) storage.set(key, 'stale')
  const results = await Promise.allSettled(Array.from({length: 4}, () => state.__requestFailure(httpError(401))))
  assert.ok(results.every(result => result.status === 'rejected'))
  assert.equal(storage.size, 0)
  assert.equal(state.__requestRefreshCalls, 0)
  assert.equal(state.__requestRetries.length, 0)
  assert.equal(state.window.location.href, '/login')
})

test('refresh failure settles concurrent requests; repeated 401 has a single retry bound', async () => {
  storage.set('weknora_refresh_token', 'refresh')
  state.__requestRefresh = async () => {throw new Error('refresh failed')}
  const outcomes = await Promise.allSettled([state.__requestFailure(httpError(401)), state.__requestFailure(httpError(401))])
  assert.ok(outcomes.every(result => result.status === 'rejected'))
  assert.equal(state.__requestRefreshCalls, 1)
  storage.set('weknora_refresh_token', 'refresh')
  state.__requestRefresh = async () => ({success: true, data: {token: 'fresh', refreshToken: 'next'}})
  state.__requestRetry = (config: any) => state.__requestFailure(httpError(401, {message: 'still unauthorized'}, config))
  await assert.rejects(state.__requestFailure(httpError(401)), {message: 'still unauthorized'})
  assert.equal(state.__requestRefreshCalls, 2)
  assert.equal(state.__requestRetries.length, 1)
})

test('canceled and timed out requests preserve transport identity', async () => {
  for (const error of [{code: 'ERR_CANCELED', __CANCEL__: true}, {code: 'ECONNABORTED'}, {code: 'ETIMEDOUT'}]) {
    await assert.rejects(state.__requestFailure(error), e => e === error)
  }
  await assert.rejects(state.__requestFailure(new Error('network')), {message: 'error.networkError'})
})

test('canceling a download while refresh is pending rejects promptly without retrying or clearing other sessions', async () => {
  storage.set('weknora_refresh_token', 'refresh')
  let release!: () => void
  const ready = new Promise<void>(resolve => {release = resolve})
  state.__requestRefresh = async () => {await ready; return {success: true, data: {token: 'fresh', refreshToken: 'next'}}}
  const controller = new AbortController()
  const canceled = state.__requestFailure(httpError(401, {}, {signal: controller.signal}))
  const rejected = assert.rejects(canceled, e => (e as any).code === 'ERR_CANCELED')
  const sibling = state.__requestFailure(httpError(401))
  controller.abort()
  await rejected
  assert.equal(state.window.location.href, '')
  release()
  await sibling
  assert.equal(state.__requestRetries.length, 1)
  assert.equal(storage.get('weknora_token'), 'fresh')
})


test('request interceptor preserves Embed identity and forwards active tenant, language and request headers', () => {
  storage.set('weknora_token', 'session')
  storage.set('weknora_selected_tenant_id', '42')
  const normal = state.__requestBefore({url: '/api/v1/models', headers: {Accept: 'application/json'}})
  assert.equal(normal.headers.Authorization, 'Bearer session')
  assert.equal(normal.headers['X-Tenant-ID'], '42')
  assert.equal(normal.headers['Accept-Language'], 'en-US')
  assert.equal(normal.headers['X-Request-ID'], 'request-id')
  const embed = state.__requestBefore({url: '/api/v1/embed/chat', headers: {Authorization: 'Embed channel'}})
  assert.equal(embed.headers.Authorization, 'Embed channel')
  assert.equal(embed.headers['X-Tenant-ID'], undefined)
})


test('already canceled requests never begin a refresh cycle', async () => {
  storage.set('weknora_refresh_token', 'refresh')
  const controller = new AbortController()
  controller.abort()
  await assert.rejects(state.__requestFailure(httpError(401, {}, {signal: controller.signal})), e => (e as any).code === 'ERR_CANCELED')
  assert.equal(state.__requestRefreshCalls, 0)
  assert.equal(state.__requestRetries.length, 0)
})
