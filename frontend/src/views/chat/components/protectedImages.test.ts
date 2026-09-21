import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, effectScope, nextTick, reactive, ref, shallowRef, watch } from 'vue'
import { clearProtectedFileFailureCache, hydrateProtectedFileImages } from '../../../utils/security.ts'
import { setDefaultProtectedFileAccess } from '../../../utils/protectedFileAccess.ts'
import { persistedAssistantId } from '../../../utils/steerStreamFork.ts'

function section(source: string, start: string, end: string) {
  const a = source.indexOf(start)
  const b = source.indexOf(end, a)
  assert.ok(a >= 0 && b > a)
  return source.slice(a, b)
}

// Execute the component's actual access computation, lifecycle hooks and
// completion watcher with Vue reactivity; mock only the DOM and unrelated UI.
function setup(t: TestContext, agent = false) {
  const source = readFileSync(new URL(agent ? './AgentStreamDisplay.vue' : './botmsg.vue', import.meta.url), 'utf8')
  const effects = effectScope()
  const originalWindow = (globalThis as any).window
  const originalFetch = globalThis.fetch
  ;(globalThis as any).window = { location: { origin: 'http://localhost' }, addEventListener() {} }
  clearProtectedFileFailureCache()
  t.after(() => {
    effects.stop()
    globalThis.fetch = originalFetch
    ;(globalThis as any).window = originalWindow
    setDefaultProtectedFileAccess(null)
    clearProtectedFileFailureCache()
  })
  const attrs: Record<string, string> = { src: `local://2/exports/component-${agent}-${t.name}.png`.replaceAll(' ', '-') }
  const img: any = {
    dataset: {}, get src() { return attrs.src }, set src(value: string) { attrs.src = value },
    getAttribute: (key: string) => attrs[key] || '',
    setAttribute: (key: string, value: string) => { attrs[key] = value },
    removeAttribute: (key: string) => { delete attrs[key] },
  }
  const root: any = { querySelectorAll: () => [img], addEventListener() {} }
  const props = reactive({
    sessionId: 'session', embeddedMode: false, embedChannelId: '', embedToken: '',
    session: { id: 'synthetic-segment', assistant_message_id: 'persisted', isAgentMode: agent, is_completed: false },
  })
  const ready = ref(false)
  let updated = () => {}
  const scopeSource = agent
    ? section(source, 'const resolveAssistantMessageId =', '// -----------------------------------------------------------------------------\n// Skill artifact')
    : section(source, 'const protectedFileAccess =', 'const artifactRefLabels =')
  const completionSource = agent
    ? section(source, 'watch(answerFullyRendered,', '// Whether any currently visible step')
    : section(source, 'watch(\n    answerFullyRendered,', '// 单次渲染整个 Markdown')
  const updateSource = agent
    ? section(source, 'onUpdated(() => {', '// 自定义渲染器')
    : section(source, 'onUpdated(() => {', 'onMounted(async () => {')
  const tasks: Promise<void>[] = []
  const context = {
    props, computed, ref, watch, nextTick, persistedAssistantId,
    clearProtectedFileFailureCache,
    hydrateProtectedFileImages: (...args: Parameters<typeof hydrateProtectedFileImages>) => {
      const task = hydrateProtectedFileImages(...args)
      tasks.push(task)
      return task
    },
    parentMd: shallowRef(root), rootElement: shallowRef(root), answerFullyRendered: ready,
    emit() {}, rebindCitations() {}, refreshMarkdownEnhancements() {},
    enhanceMarkdownContainer: async () => {}, renderMermaidInContainer: async () => {},
    hydrateArtifactImages: async () => {}, artifactRefContext: { value: null },
    onUpdated: (callback: () => void) => { updated = callback },
  }
  effects.run(() => vm.runInNewContext(ts.transpile(`${scopeSource}\n${completionSource}\n${updateSource}`), context))
  const requests: Array<{ url: string; headers: any }> = []
  let status = 200
  globalThis.fetch = async (url, init) => {
    requests.push({ url: String(url), headers: init?.headers })
    return status === 200 ? new Response(new Blob(['png'], { type: 'image/png' })) : new Response(null, { status })
  }
  return {
    props, ready, img, requests, updated: () => updated(), status: (code: number) => { status = code },
    flush: async () => {
      await nextTick()
      await nextTick() // completion watchers enqueue their own DOM-update pass
      await Promise.all(tasks.splice(0))
      await nextTick()
    },
  }
}

for (const agent of [false, true]) {
  const mode = agent ? 'agent' : 'ordinary'
  test(`${mode} reply requests host images using the persisted assistant ID`, async t => {
    const h = setup(t, agent)
    h.updated()
    await h.flush()
    assert.equal(h.requests.length, 1)
    assert.ok(h.requests[0].url.startsWith('/api/v1/sessions/session/messages/persisted/files?'))
    assert.match(h.img.src, /^blob:/)
  })

  test(`${mode} completion retries a denied streaming image without new text`, async t => {
    const h = setup(t, agent)
    h.status(403)
    h.updated()
    await h.flush()
    assert.match(h.img.src, /^data:image\/gif/)
    h.status(200)
    h.ready.value = true
    await h.flush()
    assert.equal(h.requests.length, 2)
    assert.match(h.img.src, /^blob:/)
  })

  test(`${mode} reply retries when the persisted message ID becomes available`, async t => {
    const h = setup(t, agent)
    h.status(403)
    h.updated()
    await h.flush()
    h.status(200)
    h.props.session.assistant_message_id = 'corrected'
    await h.flush()
    assert.equal(h.requests.length, 2)
    assert.ok(h.requests[1].url.startsWith('/api/v1/sessions/session/messages/corrected/files?'))
    assert.match(h.img.src, /^blob:/)
  })

  test(`${mode} reply preserves the embed authentication plane`, async t => {
    const h = setup(t, agent)
    setDefaultProtectedFileAccess({ mode: 'embed', channelId: 'channel', token: 'embed-token' })
    h.updated()
    await h.flush()
    assert.equal(h.requests.length, 1)
    assert.ok(h.requests[0].url.startsWith('/api/v1/embed/channel/files?'))
    assert.deepEqual(h.requests[0].headers, { Authorization: 'Embed embed-token' })
    assert.match(h.img.src, /^blob:/)
  })
}
