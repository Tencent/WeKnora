import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test, type TestContext } from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import type { RetrievalConfig } from '../../api/retrieval'
import { fixture, flush } from '../../test-utils/vueFixture'

type SettingsState = {
  canEdit: boolean
  localConfig: RetrievalConfig
  handleParamChange: () => void
  handleModelChange: (modelId: string) => void
  handleRrfChange: (field: 'rrf_k' | 'rrf_vector_weight' | 'rrf_keyword_weight', value: number | string) => void
}

type ConfigResponse = { data?: RetrievalConfig }

const baseConfig: RetrievalConfig = {
  embedding_top_k: 50,
  vector_threshold: 0.15,
  keyword_threshold: 0.3,
  rerank_top_k: 10,
  rerank_threshold: 0.2,
  rerank_model_id: 'existing-model',
  rrf_k: 90,
  rrf_vector_weight: 0.8,
  rrf_keyword_weight: 0.2,
}
const SAVE_DELAY_MS = 500
const source = readFileSync(new URL('./RetrievalSettings.vue', import.meta.url), 'utf8')
const script = compileScript(parse(source).descriptor, { id: 'retrieval-autosave-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')

// 将请求的完成时机交给用例控制，覆盖真实网络中不同的响应顺序。
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

function settingsFixture(t: TestContext, options: { canEdit?: boolean; deferLoad?: boolean } = {}) {
  const f = fixture(t)
  const load = deferred<ConfigResponse>()
  const writes: { tenantId: number; body: RetrievalConfig; response: ReturnType<typeof deferred<ConfigResponse>> }[] = []
  const readTenants: number[] = []
  const isAdmin = vue.ref(options.canEdit ?? true)
  const auth = vue.reactive({ effectiveTenantId: 1, hasRole: () => isAdmin.value })
  const successMessages: string[] = []
  const errorMessages: string[] = []
  let activeWrites = 0
  let maxActiveWrites = 0
  const savedConfigs = new Map<number, RetrievalConfig>([
    [1, { ...baseConfig }],
    [2, { ...baseConfig, embedding_top_k: 35 }],
  ])
  // 同一模块的两个实例共享生产代码自己的状态，用于覆盖关闭后立即重开。
  const compiled = f.evaluate(script, {
    vue,
    '@/components/ModelSelector.vue': { default: {} },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': {
      MessagePlugin: {
        success: (message: string) => successMessages.push(message),
        error: (message: string) => errorMessages.push(message),
      },
    },
    '@/stores/auth': { useAuthStore: () => auth },
    '@/api/retrieval': {
      getTenantRetrievalConfig: () => {
        const tenantId = auth.effectiveTenantId
        readTenants.push(tenantId)
        if (options.deferLoad && readTenants.length === 1) return load.promise
        return Promise.resolve({ data: { ...savedConfigs.get(tenantId)! } })
      },
      updateTenantRetrievalConfig: (config: RetrievalConfig) => {
        // API 会序列化请求体，之后的界面编辑不能改变已经发出的内容。
        const body: RetrievalConfig = JSON.parse(JSON.stringify(config))
        const response = deferred<ConfigResponse>()
        writes.push({ tenantId: auth.effectiveTenantId, body, response })
        activeWrites += 1
        maxActiveWrites = Math.max(maxActiveWrites, activeWrites)
        return response.promise.finally(() => { activeWrites -= 1 })
      },
    },
  })
  const { vm, close } = f.mount(compiled.default)
  const state = vm as SettingsState
  const advance = async (milliseconds = SAVE_DELAY_MS) => {
    t.mock.timers.tick(milliseconds)
    await flush()
  }
  const ready = async () => {
    await flush()
    // 等待加载后的控件初始化结束，不将加载本身计作用户编辑。
    await advance(1000)
    assert.equal(writes.length, 0)
  }
  const editTopK = (value: number) => {
    state.localConfig.embedding_top_k = value
    state.handleParamChange()
  }
  const succeed = async (index: number) => {
    const request = writes[index]
    assert.ok(request, `Expected PUT request ${index}`)
    savedConfigs.set(request.tenantId, { ...request.body })
    request.response.resolve({ data: { ...request.body } })
    await flush()
  }
  const fail = async (index: number) => {
    const request = writes[index]
    assert.ok(request, `Expected PUT request ${index}`)
    request.response.reject(new Error('temporary save failure'))
    await flush()
  }
  return {
    state, writes, load, close, ready, advance, editTopK, succeed, fail, auth, readTenants,
    setAdmin: (allowed: boolean) => { isAdmin.value = allowed },
    open: () => {
      const next = f.mount(compiled.default)
      return { state: next.vm as SettingsState, close: next.close }
    },
    successMessages, errorMessages,
    topKRequests: () => writes.map(request => request.body.embedding_top_k),
    savedConfig: (tenantId = 1) => savedConfigs.get(tenantId)!,
    maxActiveWrites: () => maxActiveWrites,
  }
}

test('loading and unchanged input events do not save the configuration', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.state.handleParamChange()
  await h.advance()
  assert.deepEqual(h.writes, [])
  assert.deepEqual(h.successMessages, [])
})

test('rapid edits before the debounce expires save only the latest value', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance(300)
  h.editTopK(70)
  await h.advance(SAVE_DELAY_MS - 1)
  assert.equal(h.writes.length, 0)
  await h.advance(1)
  assert.deepEqual(h.topKRequests(), [70])
  await h.succeed(0)
  assert.equal(h.savedConfig().embedding_top_k, 70)
})

test('a response before the next debounce cannot acknowledge an unsent edit', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.editTopK(70)
  await h.advance(100)
  await h.succeed(0)
  assert.deepEqual(h.successMessages, [], 'unsaved edits must not be reported as saved')
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60, 70])
  await h.succeed(1)
  assert.equal(h.savedConfig().embedding_top_k, 70)
  assert.equal(h.successMessages.length, 1)
  assert.equal(h.maxActiveWrites(), 1)
})

test('a debounce expiring during a slow request does not start a concurrent save', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.editTopK(70)
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60])
  await h.succeed(0)
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60, 70])
  await h.succeed(1)
  assert.equal(h.savedConfig().embedding_top_k, 70)
  assert.equal(h.maxActiveWrites(), 1)
})

test('multiple edits during each request coalesce and eventually persist the latest value', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.editTopK(65)
  await h.advance()
  h.editTopK(70)
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60])
  await h.succeed(0)
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60, 70])
  h.editTopK(75)
  await h.advance()
  h.editTopK(80)
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60, 70])
  await h.succeed(1)
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60, 70, 80])
  await h.succeed(2)
  await h.advance()
  assert.equal(h.savedConfig().embedding_top_k, 80)
  assert.equal(h.writes.length, 3)
  assert.equal(h.maxActiveWrites(), 1)
})

test('returning to the original value while a save is pending still restores it on the server', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.editTopK(50)
  await h.advance()
  await h.succeed(0)
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60, 50])
  await h.succeed(1)
  assert.equal(h.savedConfig().embedding_top_k, 50)
})

test('returning to the in-flight value does not produce a redundant save', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.editTopK(70)
  h.editTopK(60)
  await h.advance()
  await h.succeed(0)
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60])
})

test('repeated change events during a request do not duplicate its write', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.state.handleParamChange()
  await h.advance()
  assert.equal(h.writes.length, 1)
  await h.succeed(0)
  await h.advance()
  h.state.handleParamChange()
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60])
})

test('queued model and RRF edits preserve the other fields and the submitted snapshot', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.state.handleModelChange('replacement-model')
  h.state.handleRrfChange('rrf_k', 0)
  h.state.handleRrfChange('rrf_vector_weight', 0.25)
  h.state.handleRrfChange('rrf_keyword_weight', 0.75)
  await h.advance()
  assert.deepEqual(h.writes[0]?.body, { ...baseConfig, embedding_top_k: 60 })
  await h.succeed(0)
  await h.advance()
  assert.equal(h.writes.length, 2)
  assert.deepEqual(h.writes[1]?.body, {
    ...baseConfig,
    embedding_top_k: 60,
    rerank_model_id: 'replacement-model',
    rrf_k: 0,
    rrf_vector_weight: 0.25,
    rrf_keyword_weight: 0.75,
  })
  await h.succeed(1)
  assert.equal(h.maxActiveWrites(), 1)
})

test('a failed save remains dirty but does not retry indefinitely without a new edit', async t => {
  t.mock.method(console, 'error', () => {})
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  await h.fail(0)
  await h.advance(60_000)
  assert.deepEqual(h.topKRequests(), [60])
  assert.equal(h.successMessages.length, 0)
  assert.equal(h.errorMessages.length, 1)
  h.state.handleParamChange()
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60, 60])
  await h.succeed(1)
  assert.equal(h.savedConfig().embedding_top_k, 60)
})

test('a new edit queued during a failed request can still be saved', async t => {
  t.mock.method(console, 'error', () => {})
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.editTopK(70)
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60])
  await h.fail(0)
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60, 70])
  await h.succeed(1)
  assert.equal(h.savedConfig().embedding_top_k, 70)
  assert.equal(h.maxActiveWrites(), 1)
})

test('a later failed save does not acknowledge changes after an earlier successful save', async t => {
  t.mock.method(console, 'error', () => {})
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  await h.succeed(0)
  h.editTopK(70)
  await h.advance()
  await h.fail(1)
  await h.advance(60_000)
  assert.deepEqual(h.topKRequests(), [60, 70])
  assert.equal(h.savedConfig().embedding_top_k, 60)
  h.state.handleParamChange()
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60, 70, 70])
  await h.succeed(2)
  assert.equal(h.savedConfig().embedding_top_k, 70)
})

test('unmounting before the debounce expires cancels the pending save', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  h.close()
  await h.advance(60_000)
  assert.deepEqual(h.writes, [])
  assert.deepEqual(h.successMessages, [])
})

test('unmounting during a request prevents a queued save and late success notification', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.editTopK(70)
  h.close()
  await h.succeed(0)
  await h.advance(60_000)
  assert.deepEqual(h.topKRequests(), [60])
  assert.deepEqual(h.successMessages, [])
  assert.deepEqual(h.errorMessages, [])
})

test('unmounting during a request suppresses its late failure notification', async t => {
  t.mock.method(console, 'error', () => {})
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.close()
  await h.fail(0)
  await h.advance(60_000)
  assert.deepEqual(h.topKRequests(), [60])
  assert.deepEqual(h.successMessages, [])
  assert.deepEqual(h.errorMessages, [])
})

test('a load completing after unmount does not replace the abandoned view state', async t => {
  const h = settingsFixture(t, { deferLoad: true })
  const beforeLoad = JSON.parse(JSON.stringify(h.state.localConfig))
  h.close()
  h.load.resolve({ data: { ...baseConfig, embedding_top_k: 85 } })
  await flush()
  await h.advance(60_000)
  assert.deepEqual(JSON.parse(JSON.stringify(h.state.localConfig)), beforeLoad)
  assert.deepEqual(h.writes, [])
  assert.deepEqual(h.successMessages, [])
})

test('non-admin input events cannot enqueue any configuration writes', async t => {
  const h = settingsFixture(t, { canEdit: false })
  await h.ready()
  h.editTopK(60)
  h.state.handleModelChange('replacement-model')
  h.state.handleRrfChange('rrf_k', 80)
  await h.advance()
  assert.deepEqual(h.writes, [])
})

test('changing the workspace before the debounce expires cancels the old save', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  h.auth.effectiveTenantId = 2
  await flush()
  await h.advance()
  assert.deepEqual(h.writes, [])
  assert.deepEqual(h.successMessages, [])
})

test('switching away and back does not revive an old view or its queued save', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.editTopK(70)
  h.auth.effectiveTenantId = 2
  h.auth.effectiveTenantId = 1
  await flush()
  await h.succeed(0)
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60])
  assert.deepEqual(h.successMessages, [])
})

test('a load completing after a workspace switch cannot populate the old view', async t => {
  const h = settingsFixture(t, { deferLoad: true })
  const beforeLoad = JSON.parse(JSON.stringify(h.state.localConfig))
  h.auth.effectiveTenantId = 2
  await flush()
  h.load.resolve({ data: { ...baseConfig, embedding_top_k: 85 } })
  await flush()
  await h.advance()
  assert.deepEqual(JSON.parse(JSON.stringify(h.state.localConfig)), beforeLoad)
  assert.deepEqual(h.writes, [])
})

test('reopening the same workspace waits for its previous save before loading', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.editTopK(70)
  h.close()
  const next = h.open()
  await flush()
  assert.deepEqual(h.readTenants, [1], 'the new GET must not race the previous PUT')
  await h.succeed(0)
  await h.advance(1000)
  assert.deepEqual(h.readTenants, [1, 1])
  assert.equal(next.state.localConfig.embedding_top_k, 60)
  assert.deepEqual(h.successMessages, [])
  next.state.localConfig.embedding_top_k = 80
  next.state.handleParamChange()
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60, 80])
  await h.succeed(1)
  assert.equal(h.savedConfig().embedding_top_k, 80)
})

test('reopening after an earlier save fails still loads and allows editing', async t => {
  t.mock.method(console, 'error', () => {})
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.close()
  const next = h.open()
  await flush()
  assert.deepEqual(h.readTenants, [1])
  await h.fail(0)
  await h.advance(1000)
  assert.deepEqual(h.readTenants, [1, 1])
  assert.equal(next.state.localConfig.embedding_top_k, 50)
  assert.deepEqual(h.errorMessages, [])
  next.state.localConfig.embedding_top_k = 70
  next.state.handleParamChange()
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60, 70])
  await h.succeed(1)
})

test('a pending save in another workspace does not block the new workspace load', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.close()
  h.auth.effectiveTenantId = 2
  const next = h.open()
  await flush()
  await h.advance(1000)
  assert.deepEqual(h.readTenants, [1, 2])
  assert.equal(next.state.localConfig.embedding_top_k, 35)
  next.state.localConfig.embedding_top_k = 45
  next.state.handleParamChange()
  await h.advance()
  assert.deepEqual(h.writes.map(request => [request.tenantId, request.body.embedding_top_k]), [[1, 60], [2, 45]])
  await h.succeed(1)
  await h.succeed(0)
  assert.equal(h.savedConfig(2).embedding_top_k, 45)
  assert.equal(next.state.localConfig.embedding_top_k, 45)
  assert.equal(h.successMessages.length, 1)
})

test('editing stays disabled until loading and control initialization finish', async t => {
  const h = settingsFixture(t, { deferLoad: true })
  assert.equal(h.state.canEdit, false)
  h.load.resolve({ data: { ...baseConfig } })
  await flush()
  assert.equal(h.state.canEdit, false)
  await h.advance(99)
  assert.equal(h.state.canEdit, false)
  await h.advance(1)
  assert.equal(h.state.canEdit, true)
  assert.deepEqual(h.writes, [])
})

test('a failed initial load cannot overwrite server settings with local defaults', async t => {
  t.mock.method(console, 'error', () => {})
  const h = settingsFixture(t, { deferLoad: true })
  h.load.reject(new Error('temporary load failure'))
  await flush()
  await h.advance(1000)
  assert.equal(h.state.canEdit, false)
  h.editTopK(60)
  h.state.handleModelChange('replacement-model')
  await h.advance()
  assert.deepEqual(h.writes, [])
  assert.deepEqual(h.successMessages, [])
})

test('an older request completing cannot shorten a new debounce after another edit', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.editTopK(70)
  await h.advance()
  h.editTopK(80)
  await h.advance(100)
  await h.succeed(0)
  assert.deepEqual(h.topKRequests(), [60])
  await h.advance(SAVE_DELAY_MS - 101)
  assert.deepEqual(h.topKRequests(), [60])
  await h.advance(1)
  assert.deepEqual(h.topKRequests(), [60, 80])
  await h.succeed(1)
  assert.equal(h.savedConfig().embedding_top_k, 80)
})

test('losing edit permission during a request prevents its queued follow-up write', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.editTopK(70)
  await h.advance()
  h.setAdmin(false)
  assert.equal(h.state.canEdit, false)
  await h.succeed(0)
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60])
  h.setAdmin(true)
  h.state.handleParamChange()
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60, 70])
  await h.succeed(1)
})

test('closing a reopened view while it waits for an old save prevents its later GET', async t => {
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.close()
  const waitingView = h.open()
  await flush()
  assert.deepEqual(h.readTenants, [1])
  waitingView.close()
  await h.succeed(0)
  await h.advance(1000)
  assert.deepEqual(h.readTenants, [1])
  assert.deepEqual(h.successMessages, [])
  const next = h.open()
  await flush()
  await h.advance(1000)
  assert.deepEqual(h.readTenants, [1, 1])
  assert.equal(next.state.localConfig.embedding_top_k, 60)
})

test('a response without configuration data is not treated as a successful save', async t => {
  t.mock.method(console, 'error', () => {})
  const h = settingsFixture(t)
  await h.ready()
  h.editTopK(60)
  await h.advance()
  h.writes[0]!.response.resolve({})
  await flush()
  await h.advance(60_000)
  assert.deepEqual(h.topKRequests(), [60])
  assert.deepEqual(h.successMessages, [])
  assert.equal(h.errorMessages.length, 1)
  h.state.handleParamChange()
  await h.advance()
  assert.deepEqual(h.topKRequests(), [60, 60])
  await h.succeed(1)
})
