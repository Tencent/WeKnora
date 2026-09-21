import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import * as vue from 'vue'
import * as citationMarkdown from '../utils/citationMarkdown.ts'
import type { useChatCitationPopover } from './useChatCitationPopover.ts'
import type { useEmbedCitationPopover } from './useEmbedCitationPopover.ts'

const compiled = new Map<string, string>()
for (const file of ['useCitationPopover', 'useChatCitationPopover', 'useEmbedCitationPopover', '../utils/citationChunkCache']) {
  compiled.set(file, ts.transpileModule(readFileSync(new URL(`${file}.ts`, import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText)
}
const flush = async () => { await new Promise(resolve => setImmediate(resolve)); await vue.nextTick() }
function deferred() {
  let resolve!: (value: { data: { content: string } }) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<{ data: { content: string } }>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve: (content: string) => resolve({ data: { content } }), reject }
}

class Element {
  listeners = new Map<string, Set<(event: any) => void>>()
  constructor(readonly className = '', readonly attrs: Record<string, string> = {}) {}
  addEventListener(name: string, listener: (event: any) => void) {
    if (!this.listeners.has(name)) this.listeners.set(name, new Set())
    this.listeners.get(name)!.add(listener)
  }
  removeEventListener(name: string, listener: (event: any) => void) { this.listeners.get(name)?.delete(listener) }
  closest(selector: string) { return selector.split(',').some(s => s.trim() === `.${this.className}`) ? this : null }
  getAttribute(name: string) { return this.attrs[name] ?? null }
  getBoundingClientRect() { return { bottom: 20, left: 900 } }
  querySelector(selector: string) { return selector === '.tip-title' ? { textContent: 'Web title' } : null }
  fire(name: string, target: Element = this, relatedTarget: Element | null = null) {
    const event = { target, relatedTarget, prevented: false, stopped: false,
      preventDefault() { this.prevented = true }, stopPropagation() { this.stopped = true } }
    for (const listener of this.listeners.get(name) || []) listener(event)
    return event
  }
  get listenerCount() { return [...this.listeners.values()].reduce((n, listeners) => n + listeners.size, 0) }
}
const kb = (id = 'chunk') => new Element('citation-kb', { 'data-chunk-id': id, 'data-doc': 'Document' })
const web = () => new Element('citation-web', { 'data-url': 'https://example.com/source' })

function fixture(mode: 'chat' | 'embed', options: { drawer?: boolean; refs?: citationMarkdown.CitationKnowledgeRef[] } = {}) {
  const timers = new Map<number, { at: number; callback: () => void }>()
  let now = 0, nextId = 0, hovered = false
  const win = Object.assign(new Element(), {
    scrollX: 10, scrollY: 30, innerWidth: 1000,
    setTimeout(callback: () => void, delay: number) { const id = ++nextId; timers.set(id, { at: now + delay, callback }); return id },
    clearTimeout(id: number) { timers.delete(id) },
  })
  const tick = (ms: number) => {
    const end = now + ms
    while (true) {
      const first = [...timers].filter(([, timer]) => timer.at <= end).sort((a, b) => a[1].at - b[1].at)[0]
      if (!first) break
      now = first[1].at; timers.delete(first[0]); first[1].callback()
    }
    now = end
  }
  const requests: Array<{ route: string; args: string[]; pending: ReturnType<typeof deferred> }> = []
  const request = (route: string, ...args: string[]) => {
    const pending = deferred(); requests.push({ route, args, pending }); return pending.promise
  }
  const drawerCalls: any[] = []
  const modules: Record<string, any> = {}
  const load = (name: string): any => {
    if (modules[name]) return modules[name]
    const exports = modules[name] = {}
    runInNewContext(compiled.get(name)!, {
      exports, window: win, document: { querySelector: () => hovered ? {} : null },
      require(path: string) {
        if (path === 'vue') return vue
        if (path === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
        if (path === '@/utils/citationMarkdown') return citationMarkdown
        if (path === '@/utils/citationChunkCache') return load('../utils/citationChunkCache')
        if (path === '@/composables/useChatReferencesDrawer') return { useChatReferencesDrawer: () => options.drawer ? { open: (args: any) => drawerCalls.push(args) } : null }
        if (path === './useCitationPopover') return load('useCitationPopover')
        if (path === '@/api/knowledge-base') return { getChunkByIdOnly: (...args: string[]) => request('chat', ...args) }
        if (path === '@/api/embed') return { getEmbedChunkById: (...args: string[]) => request('embed', ...args) }
        throw new Error(`Unexpected import: ${path}`)
      },
    })
    return exports
  }
  const channel = vue.ref(mode === 'embed' ? 'channel' : ''), token = vue.ref(mode === 'embed' ? 'token' : '')
  const session = vue.ref('session-a'), refs = vue.ref(options.refs || [])
  const root = vue.shallowRef<HTMLElement | null>(new Element() as unknown as HTMLElement)
  let state!: ReturnType<typeof useChatCitationPopover>
  const renderer = vue.createRenderer<any, any>({
    patchProp() {}, insert() {}, remove() {}, setText() {}, setElementText() {},
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}), parentNode: () => null, nextSibling: () => null,
  })
  const app = renderer.createApp({ setup() {
    const common = { getKnowledgeReferences: () => refs.value }
    state = mode === 'chat'
      ? (load('useChatCitationPopover').useChatCitationPopover as typeof useChatCitationPopover)(root, {
        ...common, sessionId: () => session.value, embedChannelId: () => channel.value, embedToken: () => token.value,
      })
      : (load('useEmbedCitationPopover').useEmbedCitationPopover as typeof useEmbedCitationPopover)(root, channel, token, common)
    return () => null
  } })
  app.mount({})
  return {
    state, root, channel, token, session, refs, requests, drawerCalls, timers, win, tick,
    cache: load('../utils/citationChunkCache'), close: () => app.unmount(),
    setHovered: (value: boolean) => { hovered = value },
    fire: (name: string, target?: Element, related?: Element | null) => (root.value as unknown as Element).fire(name, target, related),
  }
}

for (const mode of ['chat', 'embed'] as const) {
  test(`${mode}: hover delay, positioning, cancellation and popup close share one timer`, async t => {
    const f = fixture(mode); t.after(f.close)
    f.fire('mouseover', kb()); f.tick(79)
    assert.equal(f.requests.length, 0)
    f.fire('mouseout', kb()); f.tick(200)
    assert.equal(f.requests.length, 0)
    f.fire('mouseover', kb()); f.tick(80)
    assert.equal(f.requests.length, 1)
    assert.equal(f.state.float.value.top, 60)
    assert.equal(f.state.float.value.left, 680)
    f.requests[0]!.pending.resolve('  body  '); await flush()
    assert.equal(f.state.float.value.content, 'body')
    f.fire('mouseout', kb()); f.state.cancelClose(); f.tick(120)
    assert.equal(f.state.float.value.visible, true, 'entering the popup cancels the trigger close')
    f.state.scheduleClose(); f.fire('mouseover', kb()); f.tick(120)
    assert.equal(f.state.float.value.visible, true, 'returning to a citation cancels the popup close')
    assert.equal(f.requests.length, 1, 'repeated hover reuses cache')
    f.fire('mouseout', kb(), new Element(`${mode}-citation-float`)); f.tick(120)
    assert.equal(f.state.float.value.visible, true)
    f.state.scheduleClose(); f.tick(120)
    assert.equal(f.state.float.value.visible, false)
    f.fire('mouseover', web()); f.tick(39)
    assert.equal(f.state.float.value.visible, false)
    f.tick(1)
    assert.equal(f.state.float.value.top, 56)
    assert.equal(f.state.float.value.title, 'Web title')
    assert.equal(f.state.float.value.url, 'https://example.com/source')
  })

  test(`${mode}: late responses cannot overwrite newer citations or cached results`, async t => {
    const f = fixture(mode); t.after(f.close)
    f.fire('click', kb('a')); f.fire('click', kb('b'))
    f.requests[0]!.pending.resolve('old'); await flush()
    assert.equal(f.state.float.value.loading, true)
    assert.equal(f.state.float.value.content, '')
    f.requests[1]!.pending.resolve('new'); await flush()
    assert.equal(f.state.float.value.content, 'new')
    f.fire('click', kb('c')); f.fire('click', web())
    f.requests[2]!.pending.reject(new Error('late failure')); await flush()
    assert.equal(f.state.float.value.type, 'web')
    assert.equal(f.state.float.value.error, '')
    assert.equal(f.state.float.value.loading, false)
    f.fire('click', kb('d')); f.fire('click', kb('b'))
    f.requests[3]!.pending.resolve('late success'); await flush()
    assert.equal(f.state.float.value.content, 'new')
  })

  test(`${mode}: root replacement detaches listeners and discards pending hovers and loads`, async t => {
    const f = fixture(mode); t.after(f.close)
    const oldRoot = f.root.value as unknown as Element
    f.fire('click', kb()); f.fire('mouseover', web())
    f.root.value = new Element() as unknown as HTMLElement
    await vue.nextTick()
    assert.equal(oldRoot.listenerCount, 0)
    assert.equal(f.state.float.value.visible, false)
    f.tick(200); f.requests[0]!.pending.resolve('old root'); await flush()
    assert.equal(f.state.float.value.content, '')
    oldRoot.fire('click', kb('old'))
    assert.equal(f.requests.length, 1)
    f.state.rebind(); f.state.rebind(); f.fire('click', web())
    assert.equal((f.root.value as unknown as Element).listenerCount, 3)
    f.state.rebind()
    assert.equal(f.state.float.value.visible, true, 'streaming rebind preserves the popup')
    f.root.value = null; await vue.nextTick()
    assert.equal(f.win.listenerCount, 0)
  })

  test(`${mode}: unmount clears timers and listeners and ignores late responses`, async () => {
    const f = fixture(mode), root = f.root.value as unknown as Element
    f.fire('click', kb()); f.fire('mouseover', web()); f.state.scheduleClose(); f.close()
    assert.equal(f.timers.size, 0)
    assert.equal(root.listenerCount, 0)
    assert.equal(f.win.listenerCount, 0)
    f.requests[0]!.pending.resolve('late'); await flush()
    assert.equal(f.state.float.value.content, '')
    assert.equal(f.state.float.value.visible, false)
    f.state.rebind()
    assert.equal(root.listenerCount, 0)
  })

  test(`${mode}: drawer resolves aliases and web highlights while preserving wiki policy`, t => {
    const f = fixture(mode, { drawer: true, refs: [{ id: 'resolved-chunk', knowledge_title: 'Document' }] }); t.after(f.close)
    const event = f.fire('click', kb('DOC-1'))
    assert.equal(event.prevented && event.stopped, true)
    assert.equal(f.drawerCalls[0].highlight.chunkId, 'resolved-chunk')
    assert.equal(f.drawerCalls[0].references[0].id, 'resolved-chunk')
    f.fire('click', web())
    assert.equal(f.drawerCalls[1].highlight.url, 'https://example.com/source')
    assert.equal(f.requests.length, 0)
    const wiki = f.fire('click', new Element('citation-wiki'))
    assert.equal(wiki.prevented, mode === 'embed')
    assert.equal(wiki.stopped, mode === 'embed')
    f.refs.value = []; f.fire('click', kb())
    assert.equal(f.requests.length, 1, 'empty reference lists fall back to the popup')
  })

  test(`${mode}: hover ID resolution and empty/error cache policies remain compatible`, async t => {
    const f = fixture(mode, { refs: [{ id: 'resolved-chunk', knowledge_title: 'Document' }] }); t.after(f.close)
    f.fire('mouseover', kb('DOC-1')); f.tick(80)
    assert.equal(f.requests[0]!.args.at(-1), mode === 'chat' ? 'resolved-chunk' : 'DOC-1')
    f.requests[0]!.pending.resolve('  '); await flush()
    assert.equal(f.state.float.value.error, mode === 'chat' ? 'agentStream.citation.notFound' : '')
    f.fire('click', kb('DOC-1'))
    assert.equal(f.requests.length, 1, 'empty results are cached')
    f.refs.value = []; f.fire('click', kb('failed'))
    f.requests[1]!.pending.reject(new Error('failure')); await flush()
    assert.equal(f.state.float.value.error, mode === 'chat' ? 'agentStream.citation.loadFailed' : 'Failed to load')
    f.fire('click', kb('failed'))
    assert.equal(f.requests.length, 2, 'failures remain cached')
  })
}

test('chat sessions isolate cached content and invalidate in-flight requests on scope change', async t => {
  const f = fixture('chat'); t.after(f.close)
  f.fire('click', kb()); f.requests[0]!.pending.resolve('session a'); await flush()
  assert.deepEqual(f.requests[0]!.args, ['chunk'])
  assert.equal(f.requests[0]!.route, 'chat')
  f.session.value = 'session-b'
  assert.equal(f.state.float.value.visible, false)
  assert.equal(f.state.float.value.content, '')
  f.fire('click', kb()); f.session.value = 'session-a'
  f.requests[1]!.pending.resolve('stale session b'); await flush()
  assert.equal(f.cache.getCitationChunkCache('session-b', 'chunk'), undefined)
  f.fire('click', kb())
  assert.equal(f.requests.length, 2)
  assert.equal(f.state.float.value.content, 'session a')
  f.session.value = ''; f.fire('click', kb()); f.requests[2]!.pending.resolve('default'); await flush()
  assert.equal(f.cache.getCitationChunkCache('default', 'chunk').content, 'default')
})

test('embed scopes include channel and token for both entry points and preserve API routing', async t => {
  for (const mode of ['chat', 'embed'] as const) {
    const f = fixture(mode); t.after(f.close)
    f.channel.value = 'channel-a'; f.token.value = 'token-a'
    f.fire('click', kb()); f.requests[0]!.pending.resolve('a'); await flush()
    assert.equal(f.requests[0]!.route, 'embed')
    assert.deepEqual(f.requests[0]!.args, ['channel-a', 'token-a', 'chunk'])
    f.token.value = 'token-b'; f.fire('click', kb())
    f.channel.value = 'channel-b'; f.fire('click', kb())
    f.requests[1]!.pending.resolve('old token'); await flush()
    assert.equal(f.state.float.value.loading, true)
    f.requests[2]!.pending.resolve('b'); await flush()
    assert.equal(f.cache.getCitationChunkCache('embed:channel-b:token-b', 'chunk').content, 'b')
    f.channel.value = 'channel-a'; f.token.value = 'token-a'; f.fire('click', kb())
    assert.equal(f.requests.length, 3)
    assert.equal(f.state.float.value.content, 'a')
    f.token.value = ''; f.fire('click', kb())
    assert.equal(f.requests[3]!.route, mode, 'standalone embed never falls back to the normal API')
    if (mode === 'embed') assert.deepEqual(f.requests[3]!.args, ['channel-a', '', 'chunk'])
  }
})

test('only chat observes viewport changes and keeps hovered popups open', t => {
  for (const mode of ['chat', 'embed'] as const) {
    const f = fixture(mode); t.after(f.close)
    f.fire('click', web()); f.win.fire('scroll'); f.tick(120)
    assert.equal(f.state.float.value.visible, mode === 'embed')
    f.fire('click', web()); f.setHovered(true); f.state.scheduleClose(); f.tick(120)
    assert.equal(f.state.float.value.visible, mode === 'chat')
    f.setHovered(false); f.win.fire('resize'); f.tick(120)
    assert.equal(f.state.float.value.visible, false)
  }
})
