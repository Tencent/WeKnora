import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive } from 'vue'

const require = createRequire(import.meta.url)
const { descriptor } = parse(readFileSync(new URL('./knowledge-processing-timeline.vue', import.meta.url), 'utf8'))
const compiled = ts.transpileModule(compileScript(descriptor, {
  id: 'timeline-requests-test',
}).content, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
const flush = async () => { await new Promise(resolve => setImmediate(resolve)); await nextTick() }

function deferred() {
  let resolve!: (value: any) => void
  let reject!: (error: Error) => void
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

function spans(id: string, attempt = 1, latest = attempt) {
  return { success: true, data: {
    knowledge_id: id, attempt, latest_attempt: latest, parse_status: 'completed',
    trace: { span_id: `${id}-${attempt}`, status: 'done', duration_ms: attempt * 1000, children: [] },
  } }
}

async function fixture(options: { autoPoll?: boolean; compact?: boolean } = {}) {
  const props = reactive({ knowledgeId: 'A', parseStatus: 'completed', gracePoll: false, autoPoll: false, compact: false, ...options })
  const requests: Array<ReturnType<typeof deferred> & { id: string; attempt?: number }> = []
  const details: Array<ReturnType<typeof deferred> & { id: string }> = []
  const events: boolean[] = [], summaries: any[] = [], warnings: unknown[][] = []
  const timers = new Map<number, { callback: () => void; delay: number }>()
  let timerID = 0
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    console: { ...console, warn: (...args: unknown[]) => warnings.push(args) },
    setInterval: (callback: () => void, delay: number) => {
      const id = ++timerID; timers.set(id, { callback, delay }); return id
    },
    clearInterval: (id: number) => timers.delete(id),
    window: { addEventListener() {}, removeEventListener() {} },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { success() {}, error() {} } }
      if (name === '@/api/knowledge-base/index') return {
        getKnowledgeSpans: (id: string, attempt?: number) => {
          const request = { ...deferred(), id, attempt }; requests.push(request); return request.promise
        },
        getKnowledgeDetails: (id: string) => {
          const request = { ...deferred(), id }; details.push(request); return request.promise
        },
      }
      if (name === '@/utils/clipboard') return { copyWithToast() {} }
      if (name.startsWith('@/utils/')) return require(`../utils/${name.slice('@/utils/'.length)}.ts`)
      if (name.endsWith('.vue')) return { render: () => null }
      throw new Error(`Unexpected import: ${name}`)
    },
  })
  // Exercise actual setup, watchers and lifecycle with a headless Vue host.
  // Observe emitted badge/summary state without rendering the template.
  exports.default.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  let instance: any
  const app = renderer.createApp({ setup: () => () => h(exports.default, {
    ...props, ref: (value: any) => { if (value) instance = value },
    'onUpdate:hasSpans': (value: boolean) => events.push(value),
    'onUpdate:summary': (value: any) => summaries.push(value),
  }) })
  app.mount({})
  await flush()
  return {
    props, requests, details, events, summaries, warnings, timers,
    state: instance.$.setupState,
    switchTo: async (id: string) => { props.knowledgeId = id; await flush() },
    poll: () => { for (const timer of timers.values()) if (timer.delay === 2000) timer.callback() },
    dispose: () => app.unmount(),
  }
}

for (const outcome of ['success', 'failure', 'empty'] as const) {
  test(`switching completed documents fetches B immediately and ignores A's late ${outcome}`, async t => {
    const f = await fixture(); t.after(f.dispose)
    await f.switchTo('B')
    assert.deepEqual(f.requests.map(r => r.id), ['A', 'B'])
    f.requests[1]!.resolve(spans('B', 2)); await flush()
    const summary = JSON.stringify(f.summaries)
    const events = [...f.events]
    if (outcome === 'failure') f.requests[0]!.reject(new Error('old request failed'))
    else f.requests[0]!.resolve(outcome === 'success' ? spans('A') : { success: false })
    await flush()
    assert.equal(f.state.data.knowledge_id, 'B')
    assert.equal(f.state.selectedAttempt, 2)
    assert.deepEqual(f.events, events)
    assert.equal(JSON.stringify(f.summaries), summary)
    assert.equal(f.state.failedAttempts, 0)
    assert.equal(f.warnings.length, 0)
    assert.equal(f.events.at(-1), true)
  })
}

test('a stale finally cannot clear B loading or allow overlapping B requests', async t => {
  const f = await fixture(); t.after(f.dispose)
  await f.switchTo('B')
  f.requests[0]!.resolve(spans('A')); await flush()
  assert.equal(f.state.data, null)
  assert.equal(f.state.loading, true)
  await f.state.fetchSpans()
  assert.equal(f.requests.length, 2)
  f.requests[1]!.resolve(spans('B')); await flush()
  assert.equal(f.state.loading, false)
})

test('A to B to A rejects the first A even though its document ID matches again', async t => {
  const f = await fixture(); t.after(f.dispose)
  await f.switchTo('B'); await f.switchTo('A')
  assert.deepEqual(f.requests.map(r => r.id), ['A', 'B', 'A'])
  f.requests[2]!.resolve(spans('A', 3)); await flush()
  f.requests[0]!.resolve(spans('A')); f.requests[1]!.resolve(spans('B', 2)); await flush()
  assert.equal(f.state.data.attempt, 3)
  assert.equal(f.state.selectedAttempt, 3)
})

test('changing attempt during a request fetches the selection and ignores the earlier attempt', async t => {
  const f = await fixture(); t.after(f.dispose)
  f.state.onAttemptChange(2)
  assert.equal(f.requests[1]!.attempt, 2)
  f.requests[1]!.resolve(spans('A', 2)); await flush()
  f.requests[0]!.resolve(spans('A')); await flush()
  assert.equal(f.state.selectedAttempt, 2)
  assert.equal(f.state.data.attempt, 2)
})

for (const outcome of ['success', 'failure'] as const) {
  test(`old document details ${outcome} cannot replace B's processing settings`, async t => {
    const f = await fixture(); t.after(f.dispose)
    await f.switchTo('B')
    f.details[1]!.resolve({ success: true, data: { file_type: 'pdf', metadata: { process_overrides: { source: 'B' } } } })
    await flush()
    if (outcome === 'failure') f.details[0]!.reject(new Error('old details failed'))
    else f.details[0]!.resolve({ success: true, data: { file_type: 'docx', metadata: { process_overrides: { source: 'A' } } } })
    await flush()
    assert.equal(f.state.currentKnowledgeFileType, 'pdf')
    assert.equal(f.state.processOverrides.source, 'B')
  })
}

test('historical attempt status responses cannot populate another document', async t => {
  const f = await fixture(); t.after(f.dispose)
  f.requests[0]!.resolve(spans('A', 2)); await flush()
  const historical = f.requests.find(r => r.id === 'A' && r.attempt === 1)!
  assert.ok(historical)
  await f.switchTo('B')
  f.requests.find(r => r.id === 'B')!.resolve(spans('B')); await flush()
  historical.resolve({ success: true, data: { trace: { status: 'failed' }, parse_status: 'failed' } }); await flush()
  assert.equal(f.state.attemptStatuses.get(1), 'done')
  assert.equal(f.state.attemptStatuses.size, 1)
})

test('unmount ignores pending spans, historical statuses and document details', async () => {
  const f = await fixture()
  f.requests[0]!.resolve(spans('A', 2)); await flush()
  const refresh = f.state.fetchSpans()
  f.dispose()
  const events = [...f.events], summaries = JSON.stringify(f.summaries)
  for (const request of f.requests.slice(1)) request.resolve(spans('A', request.attempt || 9))
  f.details[0]!.resolve({ success: true, data: { file_type: 'pdf' } })
  await refresh; await flush()
  assert.equal(f.state.data.attempt, 2)
  assert.equal(f.state.currentKnowledgeFileType, '')
  assert.equal(f.state.attemptStatuses.has(1), false)
  assert.deepEqual(f.events, events)
  assert.equal(JSON.stringify(f.summaries), summaries)
  assert.equal(f.timers.size, 0)
})

test('polling suppresses overlapping reads and resumes after a current request completes', async t => {
  const f = await fixture({ autoPoll: true, compact: true }); t.after(f.dispose)
  f.props.parseStatus = 'processing'; await flush()
  f.poll(); assert.equal(f.requests.length, 1)
  f.requests[0]!.resolve({ success: true, data: { ...spans('A').data, parse_status: 'processing' } }); await flush()
  f.poll(); assert.equal(f.requests.length, 2)
  f.poll(); assert.equal(f.requests.length, 2)
  f.requests[1]!.resolve(spans('A')); await flush()
  f.poll(); assert.equal(f.requests.length, 2)
})

test('current errors are reported but failure counters reset when switching documents', async t => {
  const f = await fixture(); t.after(f.dispose)
  f.requests[0]!.reject(new Error('current failure')); await flush()
  assert.equal(f.state.failedAttempts, 1)
  assert.equal(f.state.lastFetchOk, false)
  assert.equal(f.events.at(-1), false)
  assert.equal(f.warnings.length, 1)
  await f.switchTo('B')
  assert.equal(f.state.failedAttempts, 0)
  assert.equal(f.state.lastFetchOk, true)
  assert.equal(f.state.lastFetchedAt, 0)
  f.requests[1]!.resolve(spans('B')); await flush()
  assert.equal(f.events.at(-1), true)
})
