import assert from 'node:assert/strict'
import test from 'node:test'
import { openMcpOAuthPopup, type McpOAuthPopupWindow } from './mcpOAuthPopup'

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

function fakeWindow() {
  const timers = new Map<number, () => Promise<void>>()
  let opened = 0
  let next = 0
  const win: McpOAuthPopupWindow = {
    open: () => {
      opened++
      return { closed: false, close() {} }
    },
    setInterval: (handler) => {
      timers.set(++next, handler as () => Promise<void>)
      return next
    },
    clearInterval: (id) => { timers.delete(id) },
  }
  return { timers, win, opened: () => opened }
}

test('polling is bound to the new authorization attempt', async () => {
  const attempts: string[] = []
  const { timers, win } = fakeWindow()
  const result = await openMcpOAuthPopup({
    win,
    serviceId: 'svc',
    redirectUri: 'https://app.example/callback',
    frontendRedirect: '/',
    authorize: async (_id, body) => {
      assert.equal(body.redirect_uri, 'https://app.example/callback')
      assert.equal(body.frontend_redirect, '/')
      return { authorizationUrl: 'https://auth.example/consent', authorizationAttempt: 'attempt-9' }
    },
    pollStatus: async (_id, attempt) => {
      attempts.push(attempt)
      return false
    },
  })
  assert.equal(result.status, 'opened')
  await timers.get(1)!()
  assert.deepEqual(attempts, ['attempt-9'])
  assert.equal(timers.size, 1)
})

test('a stopped popup ignores a late success', async () => {
  const pending = deferred<boolean>()
  const applied: boolean[] = []
  const { timers, win } = fakeWindow()
  const result = await openMcpOAuthPopup({
    win,
    serviceId: 'svc',
    redirectUri: 'https://app.example/callback',
    frontendRedirect: '/',
    authorize: async () => ({ authorizationUrl: 'https://auth.example/consent', authorizationAttempt: 'attempt-1' }),
    pollStatus: () => pending.promise,
    onStatus: (authorized) => { applied.push(authorized) },
    onAuthorized: () => { applied.push(true) },
  })
  assert.equal(result.status, 'opened')
  if (result.status !== 'opened') return
  const tick = timers.get(1)!()
  result.stop()
  pending.resolve(true)
  await tick
  assert.deepEqual(applied, [])
  assert.equal(timers.size, 0)
})

test('a system browser with no window handle keeps polling until consent', async () => {
  const closed: string[] = []
  const authorized: string[] = []
  const { timers, win } = fakeWindow()
  win.open = () => null
  const result = await openMcpOAuthPopup({
    win,
    serviceId: 'svc',
    redirectUri: 'https://app.example/callback',
    frontendRedirect: '/',
    authorize: async () => ({ authorizationUrl: 'https://auth.example/consent', authorizationAttempt: 'attempt-2' }),
    pollStatus: async () => authorized.length > 0,
    onClosed: () => { closed.push('closed') },
    onAuthorized: () => { authorized.push('ok') },
  })
  assert.equal(result.status, 'opened')
  await timers.get(1)!()
  assert.deepEqual(closed, [] as string[])
  assert.deepEqual(authorized, [] as string[])
  authorized.push('ready')
  await timers.get(1)!()
  assert.deepEqual(authorized, ['ready', 'ok'])
  assert.deepEqual(closed, [] as string[])
})

test('consent opens in a sized popup', async () => {
  const opened: Array<{ url: string; target?: string; features?: string }> = []
  const { win } = fakeWindow()
  win.open = (url, target, features) => {
    opened.push({ url, target, features })
    return { closed: false, close() {} }
  }
  const result = await openMcpOAuthPopup({
    win,
    serviceId: 'svc',
    redirectUri: 'https://app.example/callback',
    frontendRedirect: '/',
    authorize: async () => ({ authorizationUrl: 'https://auth.example/consent', authorizationAttempt: 'attempt-3' }),
    pollStatus: async () => false,
  })
  assert.equal(result.status, 'opened')
  assert.deepEqual(opened, [{
    url: 'https://auth.example/consent',
    target: 'mcp_oauth',
    features: 'width=600,height=720',
  }])
})

test('a missing authorization url does not open a popup', async () => {
  const { win, opened } = fakeWindow()
  const result = await openMcpOAuthPopup({
    win,
    serviceId: 'svc',
    redirectUri: 'https://app.example/callback',
    frontendRedirect: '/',
    authorize: async () => ({ authorizationUrl: '', authorizationAttempt: '' }),
    pollStatus: async () => true,
  })
  assert.equal(result.status, 'missing')
  assert.equal(opened(), 0)
})
