import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import * as progress from './skillInstallProgress.ts'
import type { useConfigSkillInstallProgress } from './useConfigSkillInstallProgress.ts'

const compile = (source: string) => ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
const compiled = compile(readFileSync(new URL('./useConfigSkillInstallProgress.ts', import.meta.url), 'utf8'))
function deferred<T = void>() {
  let resolve!: (value: T) => void, reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const flush = async () => { await new Promise(resolve => setImmediate(resolve)); await vue.nextTick() }

function fixture() {
  const requests: Array<{ url: string; options: any; pending: ReturnType<typeof deferred<void>> }> = []
  const exports: { useConfigSkillInstallProgress?: typeof useConfigSkillInstallProgress } = {}
  runInNewContext(compiled, {
    exports, AbortController,
    localStorage: { getItem: (key: string) => ({ weknora_token: 'token', weknora_selected_tenant_id: 'tenant' })[key] },
    require(name: string) {
      if (name === 'vue') return vue
      if (name === './skillInstallProgress') return progress
      if (name === '@/i18n') return { __esModule: true, default: { global: { locale: { value: 'en-US' } } } }
      if (name === '@/utils') return { generateRandomString: () => 'request-id' }
      if (name === '@/utils/api-base') return { getApiBaseUrl: () => '/prefix' }
      if (name === '@/api/system') return { configSkillInstallEventsUrl: (config: string, skill: string) => `/configs/${config}/skills/${skill}/events` }
      if (name === '@microsoft/fetch-event-source') return {
        fetchEventSource: (url: string, options: any) => {
          const pending = deferred()
          requests.push({ url, options, pending })
          // Real fetch-event-source also resolves when the caller aborts.
          options.signal.addEventListener('abort', () => pending.resolve())
          return pending.promise
        },
      }
      throw new Error(`Unexpected import: ${name}`)
    },
  })
  const send = (index: number, event: unknown) => requests[index]!.options.onmessage({ data: JSON.stringify(event) })
  return { create: exports.useConfigSkillInstallProgress!, requests, send }
}

const running = { percent: 37.4, stage: 'installing', done: false }
const failed = { percent: 100, stage: 'failed', log: 'remove failed', done: true }

test('subscriptions deduplicate targets, isolate configurations and preserve request headers', () => {
  const f = fixture(), state = f.create()
  state.sync([{ configId: ' a ', skillId: 'same' }, { configId: 'a', skillId: 'same' }, { configId: 'b', skillId: 'same' }, { configId: '', skillId: 'x' }])
  assert.equal(f.requests.length, 2)
  assert.equal(f.requests[0]!.url, '/prefix/configs/a/skills/same/events')
  const request = f.requests[0]!.options
  assert.equal(request.headers.Authorization, 'Bearer token')
  assert.equal(request.headers['X-Tenant-ID'], 'tenant')
  assert.equal(request.headers['Accept-Language'], 'en-US')
  assert.equal(request.headers['X-Request-ID'], 'request-id')
  assert.equal(request.openWhenHidden, true)
  f.send(0, running)
  assert.equal(state.percentOf('a', 'same'), 37)
  assert.equal(state.eventOf('b', 'same'), undefined)
  state.stopAll()
  assert.ok(f.requests.every(request => request.options.signal.aborted))
})

test('completion fires once and a reused skill ID clears the catalog progress before retry', () => {
  const f = fixture(), done: unknown[] = []
  const state = f.create({ onDone: (target, event) => done.push([target, event]) })
  state.follow('a', 'skill')
  f.send(0, failed)
  f.send(0, failed)
  assert.equal(done.length, 1)
  assert.equal(state.eventOf('a', 'skill')?.log, 'remove failed')
  state.follow('a', 'skill')
  assert.equal(state.eventOf('a', 'skill'), undefined)
  assert.equal(f.requests.length, 2)
  state.sync([])
  assert.equal(f.requests[1]!.options.signal.aborted, true)
})

test('detail panels keep seeded and terminal progress until explicit forget/reset', () => {
  const f = fixture(), state = f.create({ retainProgress: true })
  state.setEvent('a', 'skill', { percent: 5, stage: 'accepted', done: false })
  state.follow('a', 'skill')
  assert.equal(state.eventOf('a', 'skill')?.percent, 5)
  f.send(0, failed)
  state.sync([])
  assert.equal(state.eventOf('a', 'skill')?.log, 'remove failed')
  state.forget('a', 'skill')
  state.follow('a', 'skill')
  assert.equal(state.eventOf('a', 'skill'), undefined)
  f.send(1, running)
  state.reset()
  assert.equal(f.requests[1]!.options.signal.aborted, true)
  assert.equal(state.eventOf('a', 'skill'), undefined)
})

test('retired callbacks and promise cleanup cannot overwrite or cancel a replacement stream', async () => {
  const f = fixture(), done: unknown[] = [], state = f.create({ onDone: event => done.push(event) })
  state.follow('a', 'skill')
  f.requests[0]!.pending.reject(new Error('old request failed'))
  state.forget('a', 'skill')
  state.follow('a', 'skill')
  f.send(1, running)
  f.send(0, failed)
  assert.throws(() => f.requests[0]!.options.onerror(new Error('late failure')), /stream closed/)
  await flush()
  assert.equal(f.requests[1]!.options.signal.aborted, false)
  assert.equal(state.eventOf('a', 'skill')?.percent, 37.4)
  assert.equal(done.length, 0)
  state.stopAll()
})

test('clean EOF and rejected requests release the subscription for later polling', async () => {
  for (const reject of [false, true]) {
    const f = fixture(), state = f.create()
    state.follow('a', 'skill')
    if (reject) f.requests[0]!.pending.reject(new Error('connection failed'))
    else f.requests[0]!.pending.resolve()
    await flush()
    state.follow('a', 'skill')
    assert.equal(f.requests.length, 2)
    state.stopAll()
  }
})

test('invalid events are ignored and catalog sync prunes inactive cached progress', () => {
  const f = fixture(), state = f.create()
  state.follow('a', 'skill')
  for (const data of ['', '{', 'null', '42']) f.requests[0]!.options.onmessage({ data })
  assert.equal(state.eventOf('a', 'skill'), undefined)
  f.send(0, running)
  state.sync([])
  assert.equal(state.eventOf('a', 'skill'), undefined)
  f.send(0, running)
  assert.equal(state.eventOf('a', 'skill'), undefined)
})

const { descriptor } = parse(readFileSync(new URL('../components/SandboxSkillsPanel.vue', import.meta.url), 'utf8'))
const componentScript = compileScript(descriptor, { id: 'skill-progress-test' }).content
  .replace(/__expose\(\{[\s\S]*?\}\);?/, '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const componentCompiled = compile(componentScript)
async function panelFixture(api: Record<string, (...args: any[]) => Promise<any>> = {}) {
  const f = fixture(), exports: any = {}, intervals = new Set<number>(), emitted: any[] = []
  let timer = 0
  const record = vue.ref<any>({ id: 'a' }), panel = vue.ref<any>()
  const row = { id: 'skill', name: 'Skill', status: 'installing', enabled: true }
  runInNewContext(componentCompiled, {
    exports,
    window: { setInterval: () => { intervals.add(++timer); return timer }, clearInterval: (id: number) => intervals.delete(id), clearTimeout() {} },
    require(name: string) {
      if (name === 'vue') return vue
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { success() {}, error() {}, warning() {} } }
      if (name === '@/composables/useConfigSkillInstallProgress') return { useConfigSkillInstallProgress: f.create }
      if (name === '@/composables/useSkillInstallerModel') return { useSkillInstallerModel: () => ({ resetInstallerModel() {} }) }
      if (name === '@/api/system') return {
        listConfigSkills: async () => ({ data: [{ ...row }] }), getSandboxConfigById: async (id: string) => ({ data: { id } }),
        ...api,
      }
      if (name === '@/views/settings/envVarState') return { skillHasDeclaredEnvs: () => false }
      if (name.endsWith('.vue') || ['@/types/mention', '@/utils/index', 'tdesign-icons-vue-next'].includes(name)) return { default: {}, SETTING_DRAWER_HEADER_ACTIONS_ID: 'header' }
      throw new Error(`Unexpected import: ${name}`)
    },
  })
  exports.default.render = () => null
  const renderer = vue.createRenderer<any, any>({
    patchProp() {}, insert() {}, remove() {}, setText() {}, setElementText() {},
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}), parentNode: () => null, nextSibling: () => null,
  })
  const app = renderer.createApp({ setup: () => () => vue.h(exports.default, { record: record.value, ref: panel, onUpdated: (event: any) => emitted.push(event) }) })
  app.mount({})
  await flush()
  return { ...f, record, panel, row, intervals, emitted, close: () => app.unmount() }
}

test('panel uses scoped progress and aborts subscriptions on config changes and unmount', async (t) => {
  const f = await panelFixture()
  t.after(f.close)
  f.send(0, running)
  assert.equal(f.panel.value.progressOf(f.row), 37.4, 'panel retains its unrounded display rule')
  f.record.value = { id: 'b' }
  await flush()
  assert.equal(f.requests[0]!.options.signal.aborted, true)
  assert.equal(f.requests[1]!.url, '/prefix/configs/b/skills/skill/events')
  f.send(0, failed)
  assert.equal(f.panel.value.progressLog(f.row), '')
  f.close()
  assert.equal(f.requests[1]!.options.signal.aborted, true)
  assert.equal(f.intervals.size, 0)
})

test('panel retains uninstall failure details after list refresh stops following the skill', async (t) => {
  const row = { id: 'skill', name: 'Skill', status: 'ready', enabled: true }
  const f = await panelFixture({ listConfigSkills: async () => ({ data: [row] }), deleteConfigSkill: async () => {} })
  t.after(f.close)
  await f.panel.value.removeSkill(row)
  assert.equal(f.panel.value.progressOf(row), 5)
  f.send(0, failed)
  await flush()
  assert.equal(f.panel.value.removalErrorLines(row)[0], 'remove failed')
  assert.equal(f.panel.value.isRemoving(row), false)
  assert.equal(f.intervals.size, 0)
  assert.equal(f.requests.length, 1)
})

test('panel retry drops a previous run error and attaches fresh progress for the reused skill ID', async (t) => {
  const row = { id: 'skill', name: 'Skill', status: 'failed', enabled: true }
  const f = await panelFixture({
    listConfigSkills: async () => ({ data: [{ ...row }] }),
    reinstallConfigSkill: async () => { row.status = 'installing' },
  })
  t.after(f.close)
  f.panel.value.setInstallEvent('a', 'skill', failed)
  assert.equal(f.panel.value.progressLog(row), 'remove failed')
  await f.panel.value.retrySkill(row)
  assert.equal(f.panel.value.progressLog(row), '')
  assert.equal(f.panel.value.progressOf(row), 0)
  assert.equal(f.requests.length, 1)
  f.send(0, running)
  assert.equal(f.panel.value.progressOf(row), 37.4)
})

test('late list/image results cannot revive subscriptions after config replacement or unmount', async (t) => {
  for (const unmount of [false, true]) {
    const list = deferred<any>(), image = deferred<any>()
    const f = await panelFixture({
      listConfigSkills: (id: string) => id === 'a' ? list.promise : Promise.resolve({ data: [] }),
      getSandboxConfigById: (id: string) => id === 'a' ? image.promise : Promise.resolve({ data: { id } }),
    })
    t.after(f.close)
    if (unmount) f.close()
    else { f.record.value = { id: 'b' }; await flush() }
    list.resolve({ data: [f.row] })
    image.resolve({ data: { id: 'a' } })
    await flush()
    assert.equal(f.requests.length, 0)
    assert.equal(f.intervals.size, 0)
    assert.ok(f.emitted.every(event => event.id !== 'a'))
  }
})

test('late retry/stop/remove actions cannot attach the old skill to the new config', async (t) => {
  for (const [action, method] of [['retrySkill', 'reinstallConfigSkill'], ['stopSkill', 'stopConfigSkill'], ['removeSkill', 'deleteConfigSkill']]) {
    const pending = deferred<any>()
    const row = { id: 'skill', name: 'Skill', status: 'failed', enabled: true }
    const f = await panelFixture({ listConfigSkills: async () => ({ data: [row] }), [method!]: () => pending.promise })
    t.after(f.close)
    const runningAction = f.panel.value[action!](row)
    f.record.value = { id: 'b' }
    await flush()
    pending.resolve({ data: row })
    await runningAction
    assert.equal(f.requests.length, 0)
    assert.equal(f.panel.value.uninstallingId, '')
    assert.equal(f.intervals.size, 0)
  }
})
