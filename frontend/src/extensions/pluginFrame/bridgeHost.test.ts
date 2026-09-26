import assert from 'node:assert/strict'
import test from 'node:test'

import { reactive } from 'vue'

// The page's side of the bridge, as plugins ship it.
import { BridgeError, connect, contentHeight } from '../../../../packages/plugin-ui/index.js'
import { createBridgeHost, createRateLimiter, isAppPath, plainCopy, type BridgeHandlers, type BridgeInit } from './bridgeHost'

type Listener = (event: { data: unknown; source: unknown }) => void

/** A window that other windows post to; `source` is set by the pairing. */
class FakeWindow {
  listeners: Listener[] = []
  parent: FakeWindow = this
  from: FakeWindow | null = null
  document = undefined
  addEventListener(_type: string, fn: Listener) {
    this.listeners.push(fn)
  }
  postMessage(data: unknown) {
    const source = this.from
    const copy = structuredClone(data)
    setTimeout(() => this.listeners.forEach((fn) => fn({ data: copy, source })), 0)
  }
}

function setup(overrides: Partial<BridgeHandlers> = {}, burst = 30, context: Record<string, unknown> = { knowledgeBaseId: 'kb1' }) {
  const app = new FakeWindow()
  const frame = new FakeWindow()
  frame.parent = app
  app.from = frame // what reaches the app comes from the frame
  frame.from = app
  const calls: string[] = []
  const handlers: BridgeHandlers = {
    apiRequest: async (p) => {
      calls.push(`api ${p.method} ${p.path} ${JSON.stringify(p.body ?? null)}`)
      return p.path === '/missing' ? { status: 404, body: { error: 'no such link' } } : { status: 200, body: { ok: true } }
    },
    toast: (m, t) => calls.push(`toast ${t} ${m}`),
    confirm: async (m) => m === 'yes?',
    navigate: (p) => calls.push(`navigate ${p}`),
    resize: (h) => calls.push(`resize ${h}`),
    close: () => calls.push('close'),
    ...overrides,
  }
  const host = createBridgeHost({
    frame: () => frame as unknown as Window,
    init: () => ({
      pluginId: 'acme.links', version: '1.0.0', mount: 'pages/links', locale: 'zh-CN', role: 'viewer',
      theme: { mode: 'dark', tokens: { 'brand-color': '#0052d9' } }, context,
    }) as BridgeInit,
    handlers,
    burst,
    perSecond: 0.001,
  })
  app.addEventListener('message', (e) => host.handle(e as unknown as MessageEvent))
  return { app, frame, host, calls }
}

test('a page connects, calls its backend and the app', async () => {
  const { frame, calls } = setup()
  const wk = await connect({ window: frame as unknown as Window, autoResize: false, applyTheme: false })
  assert.equal(wk.context.mount, 'pages/links')
  assert.deepEqual(wk.context.context, { knowledgeBaseId: 'kb1' })

  assert.deepEqual(await wk.put('/links', { a: 1 }), { status: 200, body: { ok: true } })
  await assert.rejects(wk.get('/missing'), (e: unknown) => e instanceof BridgeError && e.status === 404 && e.message === 'no such link')
  assert.equal((await wk.get('/missing', { throwOnError: false })).status, 404)

  await wk.toast('saved', 'success')
  assert.equal(await wk.confirm('yes?'), true)
  assert.equal(await wk.confirm('no?'), false)
  await wk.navigate('/platform/knowledge-bases')
  await assert.rejects(wk.navigate('//evil.example'), /path of the app/)
  await wk.resize(321.4)
  await wk.close()
  assert.deepEqual(calls, [
    'api PUT /links {"a":1}',
    'api GET /missing null',
    'api GET /missing null',
    'toast success saved',
    'navigate /platform/knowledge-bases',
    'resize 322',
    'close',
  ])
})

test('the app ignores other windows and rate-limits a page', async () => {
  const { app, frame, calls } = setup({}, 2)
  const wk = await connect({ window: frame as unknown as Window, autoResize: false, applyTheme: false })

  // A message that does not come from the plugin's frame is dropped.
  const stranger = new FakeWindow()
  app.listeners.forEach((fn) =>
    fn({ data: { weknora: 1, kind: 'request', id: 99, method: 'ui.close', params: {} }, source: stranger }),
  )
  assert.equal(calls.length, 0)

  await wk.toast('one')
  await wk.toast('two')
  await assert.rejects(wk.toast('three'), /too many requests/)
})

test('helpers', () => {
  assert.equal(isAppPath('/platform/x'), true)
  assert.equal(isAppPath('//x.com'), false)
  assert.equal(isAppPath('https://x.com'), false)
  let t = 0
  const allow = createRateLimiter(1, 1, () => t)
  assert.equal(allow(), true)
  assert.equal(allow(), false)
  t = 1000
  assert.equal(allow(), true)
})

test('an editor page sets its form and hears its changes', async () => {
  const set: unknown[] = []
  const { frame, host } = setup({ setValues: (v) => set.push(v) })
  const wk = await connect({ window: frame as unknown as Window, autoResize: false, applyTheme: false })
  await wk.form.set({ settings: { project: 'ENG' } })
  await assert.rejects(wk.form.set('nope' as unknown as Record<string, unknown>), /must be an object/)
  assert.deepEqual(set, [{ settings: { project: 'ENG' } }])
  const heard = new Promise((resolve) => wk.on('values', resolve))
  host.send('values', { settings: { project: 'OPS' } })
  assert.deepEqual(await heard, { settings: { project: 'OPS' } })

  // Pages outside a form have none.
  const other = setup()
  const page = await connect({ window: other.frame as unknown as Window, autoResize: false, applyTheme: false })
  await assert.rejects(page.form.set({ a: 1 }), /not in a form/)
})

test('reactive data reaches the page as a plain copy', async () => {
  // A tool call still streaming: its arguments and result are Vue proxies,
  // which postMessage cannot clone.
  const event = reactive({ arguments: { q: 'x' }, structured: { items: [1, 2] } })
  assert.throws(() => structuredClone({ context: { result: event.structured } }), { name: 'DataCloneError' })
  const { frame, host } = setup({}, 30, { tool: 'search', arguments: event.arguments, result: event.structured })
  const wk = await connect({ window: frame as unknown as Window, autoResize: false, applyTheme: false })
  assert.deepEqual(wk.context.context, { tool: 'search', arguments: { q: 'x' }, result: { items: [1, 2] } })

  const heard = new Promise((resolve) => wk.on('values', resolve))
  host.send('values', reactive({ a: [1] }))
  assert.deepEqual(await heard, { a: [1] })

  assert.deepEqual(plainCopy(reactive({ a: { b: [1] } })), { a: { b: [1] } })
  assert.equal(plainCopy(undefined), undefined)
})

test('autoResize measures the content, not the frame', () => {
  // The frame is 600px tall; the content ends far above it.
  const el = (bottom: number, style: Record<string, string> = {}) => ({ bottom, style })
  const child = el(120, { marginBottom: '16px' })
  const body = { ...el(110, { marginBottom: '8px' }), children: [child, el(600, { position: 'fixed' }), el(0, { display: 'none' })] }
  const root = el(600, { paddingBottom: '4px', borderBottomWidth: '1px' })
  const all = [body, root, ...body.children]
  const win = {
    scrollY: 10,
    document: { body: { ...body, getBoundingClientRect: () => ({ bottom: body.bottom }) }, documentElement: { scrollHeight: 600 } },
    getComputedStyle: (e: { style?: Record<string, string> }) =>
      all.find((x) => x.style === e.style)?.style ?? root.style,
  }
  for (const c of body.children) Object.assign(c, { getBoundingClientRect: () => ({ bottom: c.bottom }) })
  win.document.body.children = body.children
  // A child's margin collapsing through the body's: 120 + 16, scrolled by 10,
  // plus the root's padding and border; the fixed child does not count.
  assert.equal(contentHeight(win as unknown as Window), 10 + 136 + 4 + 1)
})
