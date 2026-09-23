import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive } from 'vue'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./ModelEditorDialog.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'model-editor-flow-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

type Result = { available: boolean; message?: string; dimension?: number }
async function fixture(options: {
  type?: string
  edit?: boolean
  check?: (payload: any) => Promise<Result>
  save?: (payload: any) => Promise<void>
  providers?: any[]
  resolve?: (params: any) => Promise<any>
} = {}) {
  const requests: any[] = []
  const saves: any[] = []
  const resolves: any[] = []
  const providers: any[] = options.providers || []
  const providersStore = {
    ensureLoaded: async () => providers,
    providersFor: () => providers,
    isLoading: () => false,
    providerById: (id: string) => providers.find((p) => p.value === id),
    iconFor: () => '',
    labelFor: (id: string) => id,
    descriptionFor: () => '',
  }
  const toasts: string[] = []
  const visibility: boolean[] = []
  const props = reactive<{ visible: boolean; modelType: string; modelData: any }>({
    visible: false,
    modelType: options.type || 'chat',
    modelData: options.edit ? {
      id: 'saved-model', modelName: 'old-model', name: '', source: 'remote',
      baseUrl: 'https://example.com/v1', provider: 'generic', isDefault: false,
    } : null,
  })
  const check = async (payload: any) => {
    requests.push(payload)
    return options.check ? options.check(payload) : { available: true }
  }
  const exports: any = {}
  runInNewContext(compiled, {
    exports, URL, setInterval, clearInterval, setTimeout, clearTimeout, JSON,
    window: { addEventListener() {}, removeEventListener() {} },
    console: { error() {}, debug() {}, log() {} },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key, te: () => false, locale: { value: 'en-US' } }) }
      if (name === '@/stores/ui') return { useUIStore: () => ({}) }
      if (name === 'tdesign-vue-next') return {
        MessagePlugin: Object.fromEntries(['success', 'error', 'warning', 'info'].map(key => [key, (text: string) => toasts.push(text)])),
      }
      if (name === '@/api/initialization') return {
        checkRemoteModel: check, testEmbeddingModel: check, checkRerankModel: check, checkASRModel: check,
        listModelProviders: async () => [], checkOllamaStatus: async () => ({ available: false }),
        resolveModelCatalog: async (params: any) => { resolves.push(params); return options.resolve ? options.resolve(params) : {
          provider: params.provider, api: 'openai-completions', base_url: params.base_url, remote_model: params.model,
          cataloged: false, model: {}, capabilities: { provider: params.provider, api: 'openai-completions', cataloged: false, reasoning: false, thinking_levels: [], thinking_format: 'none' },
        } },
      }
      if (name === '@/stores/modelProviders') return { useModelProvidersStore: () => providersStore }
      if (name === '@/api/model') return { getWeKnoraCloudStatus: async () => ({ has_models: true, needs_reinit: false }) }
      if (name === '@/utils/weknoraCloudModels') return require('../utils/weknoraCloudModels.ts')
      if (name === '@/stores/modelProvidersState') return require('../stores/modelProvidersState.ts')
      if (name === '@/utils/reasoningEffort') return require('../utils/reasoningEffort.ts')
      if (name === '@/utils/contextWindow') return require('../utils/contextWindow.ts')
      if (name === '@/components/modelEditorSourceState') return require('./modelEditorSourceState.ts')
      return { default: {} }
    },
  })
  exports.default.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  let vm: any
  const app = renderer.createApp({
    setup: () => () => h(exports.default, {
      ...props,
      ref: (value: any) => { vm = value },
      saveModel: async (payload: any) => { saves.push(payload); await options.save?.(payload) },
      'onUpdate:visible': (value: boolean) => { visibility.push(value); props.visible = value },
    }),
  })
  app.mount({})
  props.visible = true
  await nextTick()
  await nextTick()
  Object.assign(vm.formData, { modelName: 'draft-model', baseUrl: 'https://example.com/v1', apiKey: options.edit ? '' : 'draft-key' })
  vm.formRef = { validate: async () => true }
  await nextTick()
  return { vm, props, requests, saves, toasts, visibility, resolves, close: () => app.unmount() }
}

for (const type of ['chat', 'embedding', 'rerank', 'vllm', 'asr']) {
  test(`${type}: connection tests use unsaved form values without saving`, async () => {
    const f = await fixture({ type })
    try {
      f.vm.formData.customHeaders = [{ key: ' X-Test ', value: ' draft ' }]
      await f.vm.checkRemoteAPI()
      assert.equal(f.requests[0].modelName, 'draft-model')
      assert.equal(f.requests[0].apiKey, 'draft-key')
      assert.equal(f.requests[0].customHeaders['X-Test'], 'draft')
      assert.equal(f.saves.length, 0)
      assert.equal(f.props.visible, true)
      assert.equal(f.vm.remoteAvailable, true)
    } finally { f.close() }
  })
}

test('editing keeps the saved-key fallback and invalidates results after credential changes', async () => {
  const f = await fixture({ edit: true })
  try {
    await f.vm.checkRemoteAPI()
    assert.equal(f.requests[0].modelId, 'saved-model')
    assert.equal(f.requests[0].apiKey, '')
    f.vm.invalidateConnectionTest()
    assert.equal(f.vm.remoteChecked, false)
    assert.equal(f.vm.remoteStale, true)
  } finally { f.close() }
})

test('long backend errors remain complete inline without duplicate error toasts', async () => {
  const message = 'Upstream rejected request\n' + 'diagnostic '.repeat(1000)
  for (const rejects of [false, true]) {
    const f = await fixture({ check: async () => {
      if (rejects) throw new Error(message)
      return { available: false, message }
    } })
    try {
      await f.vm.checkRemoteAPI()
      assert.equal(f.vm.remoteChecked, true)
      assert.equal(f.vm.remoteAvailable, false)
      assert.equal(f.vm.remoteMessage, message)
      assert.deepEqual(f.toasts, [])
    } finally { f.close() }
  }
})

test('connection edits invalidate results; display-only edits preserve them', async () => {
  const f = await fixture()
  try {
    await f.vm.checkRemoteAPI()
    f.vm.formData.displayName = 'Display only'
    assert.equal(f.vm.remoteChecked, true)
    for (const patch of [
      { modelName: 'other' }, { baseUrl: 'https://other.example/v1' },
      { apiKey: 'another-key' }, { customHeaders: [{ key: 'X-Test', value: 'one' }] },
      { dimension: 1024 }, { supportsDimensionOverride: true },
    ]) {
      Object.assign(f.vm.formData, patch)
      assert.equal(f.vm.remoteChecked, false)
      assert.equal(f.vm.remoteStale, true)
      await f.vm.checkRemoteAPI()
    }
    f.vm.formData.customHeaders[0].value = 'two'
    assert.equal(f.vm.remoteChecked, false, 'nested header edits invalidate immediately')
  } finally { f.close() }
})

test('late responses cannot overwrite newer results, including after reverting a field', async () => {
  const old = deferred<Result>()
  let calls = 0
  const f = await fixture({ check: () => ++calls === 1 ? old.promise : Promise.resolve({ available: false, message: 'current failure' }) })
  try {
    const pending = f.vm.checkRemoteAPI()
    f.vm.formData.modelName = 'changed'
    f.vm.formData.modelName = 'draft-model'
    assert.equal(f.vm.checking, false)
    await f.vm.checkRemoteAPI()
    old.resolve({ available: true })
    await pending
    assert.equal(f.vm.remoteAvailable, false)
    assert.equal(f.vm.remoteMessage, 'current failure')
  } finally { f.close() }
})

test('a stale failure does not clear the loading state of a newer test', async () => {
  const old = deferred<Result>()
  const current = deferred<Result>()
  let calls = 0
  const f = await fixture({ check: () => ++calls === 1 ? old.promise : current.promise })
  try {
    const pending = f.vm.checkRemoteAPI()
    f.vm.formData.baseUrl = 'https://changed.example/v1'
    const newer = f.vm.checkRemoteAPI()
    old.reject(new Error('old failure'))
    await pending
    assert.equal(f.vm.checking, true)
    assert.equal(f.vm.remoteChecked, false)
    current.resolve({ available: true })
    await newer
    assert.equal(f.vm.remoteAvailable, true)
  } finally { f.close() }
})

test('closing and reopening the editor discards pending test responses', async () => {
  const response = deferred<Result>()
  const f = await fixture({ check: () => response.promise })
  try {
    const pending = f.vm.checkRemoteAPI()
    f.props.visible = false
    await nextTick()
    f.props.visible = true
    await nextTick()
    response.resolve({ available: true })
    await pending
    assert.equal(f.vm.remoteChecked, false)
    assert.equal(f.vm.remoteStale, false)
  } finally { f.close() }
})

for (const source of ['remote', 'local']) {
  test(`${source} embedding: detect dimensions without invalidating success or overwriting newer input`, async () => {
    const response = deferred<Result>()
    const f = await fixture({ type: 'embedding', check: () => response.promise })
    try {
      f.vm.formData.source = source
      await nextTick()
      const run = () => source === 'remote' ? f.vm.checkRemoteAPI() : f.vm.checkOllamaDimension()
      const pending = run()
      f.vm.formData.dimension = 2048
      response.resolve({ available: true, dimension: 1024 })
      await pending
      assert.equal(f.vm.formData.dimension, 2048)
      await run()
      assert.equal(f.vm.formData.dimension, 1024)
      assert.equal(source === 'remote' ? f.vm.remoteAvailable : f.vm.dimensionSuccess, true)
      assert.equal(f.vm.checking, false)
    } finally { f.close() }
  })
}

test('save waits for success, blocks duplicate saves and cancellation, then closes and clears the draft', async () => {
  const response = deferred<void>()
  const f = await fixture({ save: () => response.promise })
  try {
    const pending = f.vm.handleConfirm()
    await nextTick()
    assert.equal(f.vm.saving, true)
    assert.equal(f.props.visible, true)
    assert.equal(f.vm.formData.modelName, 'draft-model')
    await f.vm.handleConfirm()
    f.vm.handleCancel()
    f.vm.dialogVisible = false
    assert.equal(f.saves.length, 1)
    assert.equal(f.visibility.length, 0)
    let stopped = false
    f.vm.handleSaveEscape({ key: 'Escape', preventDefault() {}, stopImmediatePropagation() { stopped = true } })
    assert.equal(stopped, true, 'Escape must not bubble to the outer Settings modal while saving')
    response.resolve()
    await pending
    assert.equal(f.props.visible, false)
    assert.equal(f.vm.formData.modelName, '')
    assert.equal(f.vm.saving, false)
  } finally { f.close() }
})

test('failed saves retain draft and drawer, show the error and allow a successful retry', async () => {
  let calls = 0
  const f = await fixture({ save: async () => { if (++calls === 1) throw new Error('save rejected') } })
  try {
    await f.vm.handleConfirm()
    assert.equal(f.props.visible, true)
    assert.equal(f.vm.formData.modelName, 'draft-model')
    assert.equal(f.vm.formData.apiKey, 'draft-key')
    assert.equal(f.vm.saveError, 'save rejected')
    assert.equal(f.vm.saving, false)
    await f.vm.handleConfirm()
    assert.equal(f.props.visible, false)
  } finally { f.close() }
})

test('failed form validation prevents persistence and leaves the drawer open', async () => {
  const f = await fixture()
  try {
    f.vm.formRef = { validate: async () => ({ modelName: [{ result: false }] }) }
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 0)
    assert.equal(f.props.visible, true)
    assert.equal(f.vm.saving, false)
  } finally { f.close() }
})

test('failed connection test does not prevent saving', async () => {
  const f = await fixture({ check: async () => ({ available: false, message: 'offline' }) })
  try {
    await f.vm.checkRemoteAPI()
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 1)
    assert.equal(f.props.visible, false)
  } finally { f.close() }
})

// Exercise the real parent persistence handler too: swallowed API or validation
// errors would otherwise look like a successful save to the editor.
const settingsSource = readFileSync(new URL('../views/settings/ModelSettings.vue', import.meta.url), 'utf8')
const saveStart = settingsSource.indexOf('const handleModelSave =')
const saveEnd = settingsSource.indexOf('// 删除模型', saveStart)
const saveScript = ts.transpileModule(settingsSource.slice(saveStart, saveEnd), {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText

for (const edit of [false, true]) {
  test(`${edit ? 'update' : 'create'}: parent propagates API and validation failures to the editor`, async () => {
    let calls = 0
    const api = async () => { calls++; throw new Error('API rejected') }
    const save = runInNewContext(saveScript + '\nhandleModelSave', {
      URL, console: { error() {} },
      currentModelType: { value: 'chat' }, editingModel: { value: edit ? { id: 'saved-model' } : null },
      getModelType: () => 'KnowledgeQA', t: (key: string) => key,
      createModel: api, updateModelAPI: api,
      MessagePlugin: { success() {}, error() {}, warning() {} }, loadModels: async () => {},
    })
    const valid = { modelName: 'model', source: 'remote', baseUrl: 'https://example.com/v1' }
    await assert.rejects(save(valid), /API rejected/)
    assert.equal(calls, 1)
    await assert.rejects(save({ ...valid, displayName: 'x'.repeat(101) }), /displayNameTooLong/)
    await assert.rejects(save({ ...valid, modelType: 'embedding', dimension: 0 }), /dimensionInvalid/)
    assert.equal(calls, 1, 'invalid drafts must not reach persistence')
  })
}

// --- Catalog-driven editor behaviour -------------------------------------

// Objects built inside the vm context carry that realm's Object.prototype, so
// strict deepEqual against literals fails; compare their JSON shape instead.
const plain = (value: unknown) => JSON.parse(JSON.stringify(value))

const catalogProviders = [{
  value: 'lkeap', label: 'Tencent Cloud LKEAP', labels: { 'zh-CN': '腾讯云 LKEAP' }, description: '', order: 1,
  icon: 'data:image/svg+xml;base64,PHN2Zy8+',
  defaultUrls: { chat: 'https://api.lkeap.cloud.tencent.com/v1', rerank: 'https://lkeap.tencentcloudapi.com' },
  modelTypes: ['chat', 'rerank'],
  extraFields: [
    { key: 'secret_key', label: 'Secret Key', type: 'password', required: true, model_types: ['Rerank'], secret: true },
    { key: 'region', label: 'Region', type: 'string', default: 'ap-guangzhou', model_types: ['Rerank'] },
    { key: 'api_version', label: 'API Version', type: 'string', default: '2024-10-21' },
  ],
  models: [
    { id: 'deepseek-v3.1', name: 'DeepSeek V3.1', type: 'chat', reasoning: true, input: ['text', 'image'], context_window: 128000, max_output_tokens: 8192, thinking_levels: ['off', 'auto'] },
  ],
  thinking: { format: 'thinking.type', levels: ['off', 'auto'] },
}]

test('vendor extra fields: defaults are pre-filled per model type, secrets go to app_secret, and both reach the connection test', async () => {
  const f = await fixture({ type: 'rerank', providers: catalogProviders })
  try {
    f.vm.formData.provider = 'lkeap'
    f.vm.handleProviderChange('lkeap')
    await nextTick()
    assert.equal(f.vm.formData.baseUrl, 'https://lkeap.tencentcloudapi.com')
    assert.deepEqual(f.vm.plainExtraFields.map((x: any) => x.key), ['region', 'api_version'])
    assert.equal(f.vm.secretExtraField.key, 'secret_key')
    assert.equal(f.vm.formData.extraConfig.region, 'ap-guangzhou')
    assert.equal(f.vm.formData.extraConfig.api_version, '2024-10-21')
    assert.equal(f.vm.credentialFields.some((x: any) => x.key === 'app_secret'), true)

    f.vm.formData.modelName = 'lke-reranker-base'
    f.vm.formData.appSecret = ' sk-secret '
    f.vm.setExtraConfig('region', ' ap-beijing ')
    await f.vm.checkRemoteAPI()
    assert.equal(f.requests[0].appSecret, 'sk-secret')
    assert.deepEqual(plain(f.requests[0].extraConfig), { region: 'ap-beijing', api_version: '2024-10-21' })

    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 1)
    assert.deepEqual(plain(f.saves[0].extraConfig), { region: 'ap-beijing', api_version: '2024-10-21' })
    assert.equal(f.saves[0].appSecret, ' sk-secret ')
  } finally { f.close() }
})

test('required vendor extra fields block saving until filled', async () => {
  const f = await fixture({ type: 'rerank', providers: catalogProviders })
  try {
    f.vm.formData.provider = 'lkeap'
    f.vm.handleProviderChange('lkeap')
    await nextTick()
    f.vm.formData.modelName = 'lke-reranker-base'
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 0, 'missing required secret_key must not save')
    assert.equal(f.toasts.length, 1)
    f.vm.formData.appSecret = 'sk'
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 1)
  } finally { f.close() }
})

test('picking a catalog model fills blank capability fields only', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders })
  try {
    f.vm.formData.provider = 'lkeap'
    await nextTick()
    assert.deepEqual(f.vm.catalogModelOptions.map((o: any) => o.value), ['draft-model', 'deepseek-v3.1'])
    f.vm.formData.contextWindow = 32000
    f.vm.formData.modelName = 'deepseek-v3.1'
    f.vm.handleCatalogModelChange('deepseek-v3.1')
    assert.equal(f.vm.formData.contextWindow, 32000, 'user value wins')
    assert.equal(f.vm.formData.maxOutputTokens, 8192)
    assert.equal(f.vm.formData.supportsVision, true)
    f.vm.handleCatalogModelCreate(' my-custom-model ')
    assert.equal(f.vm.formData.modelName, 'my-custom-model')
    assert.equal(f.vm.catalogModelOptions[0].value, 'my-custom-model', 'free text stays selectable')
  } finally { f.close() }
})

test('resolve panel is refreshed once per 400ms burst of edits and ignored while hidden', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders })
  try {
    f.resolves.length = 0
    f.vm.formData.provider = 'lkeap'
    await nextTick()
    f.vm.formData.modelName = 'deepseek-v3'
    await nextTick()
    f.vm.formData.modelName = 'deepseek-v3.1'
    await nextTick()
    assert.equal(f.resolves.length, 0, 'debounced')
    await new Promise(resolve => setTimeout(resolve, 450))
    assert.equal(f.resolves.length, 1)
    assert.equal(f.resolves[0].model, 'deepseek-v3.1')
    assert.equal(f.resolves[0].model_type, 'chat')
    assert.equal(f.vm.resolved.api, 'openai-completions')
    f.props.visible = false
    await nextTick()
    f.vm.formData.modelName = 'other'
    await new Promise(resolve => setTimeout(resolve, 450))
    assert.equal(f.resolves.length, 1, 'no resolve while the drawer is closed')
  } finally { f.close() }
})

for (const type of ['embedding', 'rerank', 'asr']) {
  test(`${type}: the catalog diagnosis panel is hidden and never fetched`, async () => {
    const f = await fixture({ type, providers: catalogProviders })
    try {
      f.resolves.length = 0
      f.vm.formData.provider = 'lkeap'
      await nextTick()
      f.vm.formData.modelName = 'whatever'
      await new Promise(resolve => setTimeout(resolve, 450))
      assert.equal(f.vm.showResolvedPanel, false, 'protocol / thinking levels are meaningless here')
      assert.equal(f.vm.resolved, null)
      assert.equal(f.resolves.length, 0, 'no request for a panel that is not rendered')
    } finally { f.close() }
  })
}

test('vllm keeps the catalog diagnosis panel', async () => {
  const f = await fixture({ type: 'vllm', providers: catalogProviders })
  try {
    f.resolves.length = 0
    f.vm.formData.provider = 'lkeap'
    await nextTick()
    await new Promise(resolve => setTimeout(resolve, 450))
    assert.equal(f.vm.showResolvedPanel, true)
    assert.equal(f.resolves.length, 1)
    assert.equal(f.resolves[0].model_type, 'vllm')
  } finally { f.close() }
})

test('an out-of-order resolve response cannot overwrite a newer one', async () => {
  const pendings: Array<ReturnType<typeof deferred<any>>> = []
  const f = await fixture({
    type: 'chat', providers: catalogProviders,
    resolve: (params: any) => {
      const slot = deferred<any>()
      pendings.push(slot)
      return slot.promise.then((value: any) => ({ ...value, remote_model: params.model }))
    },
  })
  try {
    f.vm.formData.provider = 'lkeap'
    f.vm.formData.modelName = 'first-model'
    await new Promise(resolve => setTimeout(resolve, 450))
    f.vm.formData.modelName = 'second-model'
    await new Promise(resolve => setTimeout(resolve, 450))
    assert.equal(pendings.length, 2, 'both requests are in flight')
    pendings[1].resolve({ provider: 'lkeap', api: 'openai-completions', cataloged: true, capabilities: {} })
    await nextTick()
    pendings[0].resolve({ provider: 'lkeap', api: 'anthropic-messages', cataloged: false, capabilities: {} })
    await new Promise(resolve => setTimeout(resolve, 10))
    assert.equal(f.vm.resolved.remote_model, 'second-model', 'the stale answer must be dropped')
    assert.equal(f.vm.resolved.api, 'openai-completions')
    assert.equal(f.vm.resolving, false)
  } finally { f.close() }
})

test('a resolve failure without a readable message shows no [object Object]', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders, resolve: async () => { throw { code: 500 } } })
  try {
    f.vm.formData.provider = 'lkeap'
    await nextTick()
    await new Promise(resolve => setTimeout(resolve, 450))
    assert.equal(f.vm.resolveFailed, true)
    assert.equal(f.vm.resolveError, '', 'no detail beats a stringified object')
    assert.equal(f.vm.resolved, null)
  } finally { f.close() }
})

test('a provider id missing from the catalog still shows in the selector', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders })
  try {
    f.vm.formData.provider = 'retired-vendor'
    await nextTick()
    assert.equal(f.vm.selectedProvider, undefined)
    assert.equal(f.vm.selectedProviderDisplayLabel, 'retired-vendor')
    assert.deepEqual(f.vm.plainExtraFields, [], 'an unknown vendor declares no extra fields')
  } finally { f.close() }
})

test('editing preserves extra_config keys the current vendor does not declare', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders })
  try {
    f.props.visible = false
    await nextTick()
    f.props.modelData = {
      id: 'legacy-extra', modelName: 'deepseek-v3.1', name: '', source: 'remote',
      baseUrl: 'https://api.lkeap.cloud.tencent.com/v1', provider: 'lkeap', isDefault: false,
      extraConfig: { api_version: 'v1', retired_knob: 'keep-me' },
      spec: { reasoning: true, context_window: 64000, compat: { max_tokens_field: 'max_tokens' } },
    }
    f.props.visible = true
    await nextTick()
    await nextTick()
    assert.deepEqual(plain(f.vm.formData.extraConfig), { api_version: 'v1', retired_knob: 'keep-me' })
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 1)
    assert.deepEqual(plain(f.saves[0].extraConfig), { api_version: 'v1', retired_knob: 'keep-me' })
    assert.deepEqual(
      plain(f.saves[0].spec),
      { reasoning: true, context_window: 64000, compat: { max_tokens_field: 'max_tokens' } },
      'spec fields other than compat survive the round-trip',
    )
  } finally { f.close() }
})

test('advanced overrides: invalid compat JSON blocks save, valid JSON and protocol override are persisted', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders })
  try {
    f.vm.formData.provider = 'lkeap'
    f.vm.setExtraConfig('api', 'openai-responses')
    f.vm.setExtraConfig('remote_model_name', '')
    f.vm.formData.specCompat = '{ not json'
    await nextTick()
    assert.notEqual(f.vm.specCompatError, '')
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 0)
    assert.equal(f.vm.advancedOpen, true, 'the advanced section opens to show the error')
    f.vm.formData.specCompat = '["array"]'
    await nextTick()
    assert.equal(f.vm.specCompatError, 'model.editor.advanced.compat.mustBeObject')
    f.vm.formData.specCompat = '{"max_tokens_field": "max_tokens"}'
    await nextTick()
    assert.equal(f.vm.specCompatError, '')
    await f.vm.handleConfirm()
    assert.equal(f.saves.length, 1)
    assert.deepEqual(plain(f.saves[0].extraConfig), { api: 'openai-responses' })
    assert.equal(f.saves[0].specCompat, '{"max_tokens_field": "max_tokens"}')
  } finally { f.close() }
})

test('editing a legacy row keeps thinking_control until the user clears it', async () => {
  const f = await fixture({ type: 'chat', providers: catalogProviders })
  try {
    f.props.visible = false
    await nextTick()
    f.props.modelData = {
      id: 'legacy', modelName: 'qwen3-32b', name: '', source: 'remote', baseUrl: 'https://example.com/v1',
      provider: 'aliyun', isDefault: false, thinkingControl: 'enable_thinking',
      extraConfig: { thinking_control: 'enable_thinking', api_version: 'v1' },
      spec: { compat: { max_tokens_field: 'max_tokens' }, context_window: 1 },
    }
    f.props.visible = true
    await nextTick()
    await nextTick()
    assert.equal(f.vm.showLegacyThinkingControl, true)
    assert.equal(f.vm.formData.thinkingControl, 'enable_thinking')
    assert.deepEqual(plain(f.vm.formData.extraConfig), { api_version: 'v1' })
    assert.equal(f.vm.formData.specCompat, JSON.stringify({ max_tokens_field: 'max_tokens' }, null, 2))
    assert.deepEqual(plain(f.vm.buildExtraConfig()), { api_version: 'v1', thinking_control: 'enable_thinking' })
    f.vm.formData.thinkingControl = ''
    assert.equal(f.vm.showLegacyThinkingControl, true, 'select stays visible after clearing')
    assert.deepEqual(plain(f.vm.buildExtraConfig()), { api_version: 'v1' })
  } finally { f.close() }
})

test('parent save serializes extra_config, spec.compat and max_output_tokens from the editor payload', async () => {
  const payloads: any[] = []
  const save = runInNewContext(saveScript + '\nhandleModelSave', {
    URL, console: { error() {} },
    currentModelType: { value: 'chat' }, editingModel: { value: null },
    getModelType: () => 'KnowledgeQA', t: (key: string) => key,
    createModel: async (data: any) => { payloads.push(data) }, updateModelAPI: async () => {},
    MessagePlugin: { success() {}, error() {}, warning() {} }, loadModels: async () => {},
  })
  await save({
    modelName: 'gpt-5', source: 'remote', baseUrl: 'https://api.openai.com/v1', provider: 'openai',
    extraConfig: { api: 'openai-responses', remote_model_name: ' gpt-5-2025 ', empty: '   ' },
    thinkingControl: '', specCompat: '{"max_tokens_field":"max_completion_tokens"}',
    spec: { compat: { old: true }, reasoning: true }, contextWindow: 400000, maxOutputTokens: 16384,
  })
  assert.equal(payloads.length, 1)
  const params = payloads[0].parameters
  assert.deepEqual(plain(params.extra_config), { api: 'openai-responses', remote_model_name: 'gpt-5-2025' })
  assert.deepEqual(plain(params.spec), { reasoning: true, compat: { max_tokens_field: 'max_completion_tokens' } })
  assert.equal(params.max_output_tokens, 16384)
  assert.equal(params.context_window, 400000)
  await assert.rejects(save({
    modelName: 'gpt-5', source: 'remote', baseUrl: 'https://api.openai.com/v1', specCompat: '[1]',
  }), /compat\.invalid/)
})

// PUT /models/:id restores the stored extra_config when the field is absent
// (internal/handler/model.go), so clearing the last key has to be sent as {}.
test('parent save sends an empty extra_config so cleared vendor fields really clear', async () => {
  const updates: any[] = []
  const creates: any[] = []
  const save = runInNewContext(saveScript + '\nhandleModelSave', {
    URL, console: { error() {} },
    currentModelType: { value: 'chat' }, editingModel: { value: { id: 'saved-model' } },
    getModelType: () => 'KnowledgeQA', t: (key: string) => key,
    createModel: async (data: any) => { creates.push(data) },
    updateModelAPI: async (_id: string, data: any) => { updates.push(data) },
    MessagePlugin: { success() {}, error() {}, warning() {} }, loadModels: async () => {},
  })
  await save({
    modelName: 'gpt-5', source: 'remote', baseUrl: 'https://api.openai.com/v1', provider: 'openai',
    extraConfig: {}, thinkingControl: '',
  })
  assert.deepEqual(plain(updates[0].parameters.extra_config), {})
  // Local rows never edit extra_config here, so they keep the omit-and-preserve path.
  await save({ modelName: 'llama3', source: 'local', extraConfig: {}, thinkingControl: '' })
  assert.equal('extra_config' in updates[1].parameters, false)
  assert.equal(creates.length, 0)
})

// Two vendors that share one model id (deepseek-v4-pro is sold by DeepSeek,
// Aliyun, Volcengine and the gateways alike) and one that does not.
const switchProviders = [
  {
    value: 'vendor-a', label: 'Vendor A', description: '', order: 1, modelTypes: ['chat'],
    defaultUrls: { chat: 'https://a.example.com/v1' },
    models: [
      { id: 'shared-model', name: 'Shared', type: 'chat', context_window: 128000, max_output_tokens: 8192 },
      { id: 'only-on-a', name: 'Only on A', type: 'chat', context_window: 1000000, input: ['text', 'image'] },
    ],
  },
  {
    value: 'vendor-b', label: 'Vendor B', description: '', order: 2, modelTypes: ['chat'],
    defaultUrls: { chat: 'https://b.example.com/v1' },
    models: [
      { id: 'shared-model', name: 'Shared', type: 'chat', context_window: 128000 },
    ],
  },
]

test('switching vendor drops a model the new vendor does not serve, and what the catalog filled for it', async () => {
  const f = await fixture({ providers: switchProviders })
  try {
    f.vm.formData.provider = 'vendor-a'
    f.vm.handleProviderChange('vendor-a')
    await nextTick()
    // The select's v-model writes the name; the change handler fills the
    // capability fields from the catalog entry.
    f.vm.formData.modelName = 'only-on-a'
    f.vm.handleCatalogModelChange('only-on-a')
    await nextTick()
    assert.equal(f.vm.formData.modelName, 'only-on-a')
    // The catalog filled these, so they belong to that model.
    assert.equal(f.vm.formData.contextWindow, 1000000)
    assert.equal(f.vm.formData.supportsVision, true)

    f.vm.formData.provider = 'vendor-b'
    f.vm.handleProviderChange('vendor-b')
    await nextTick()
    // Left in place the name would be saved verbatim and fail at first call.
    assert.equal(f.vm.formData.modelName, '')
    // A 1M window from another vendor's model is the exact mis-configuration
    // the field warns about, so it goes with the name.
    assert.equal(f.vm.formData.contextWindow, undefined)
    assert.equal(f.vm.formData.supportsVision, false)
    assert.equal(f.vm.formData.baseUrl, 'https://b.example.com/v1')
  } finally { f.close() }
})

test('switching vendor keeps a model both vendors serve, and never discards typed values', async () => {
  const f = await fixture({ providers: switchProviders })
  try {
    f.vm.formData.provider = 'vendor-a'
    f.vm.handleProviderChange('vendor-a')
    await nextTick()
    f.vm.formData.modelName = 'shared-model'
    f.vm.handleCatalogModelChange('shared-model')
    await nextTick()
    // The operator overrides the catalog's number by hand.
    f.vm.formData.contextWindow = 64000
    f.vm.formData.maxOutputTokens = 4096

    f.vm.formData.provider = 'vendor-b'
    f.vm.handleProviderChange('vendor-b')
    await nextTick()
    assert.equal(f.vm.formData.modelName, 'shared-model', 'both vendors serve it')
    assert.equal(f.vm.formData.contextWindow, 64000, 'a typed value is the operator‘s')
    assert.equal(f.vm.formData.maxOutputTokens, 4096)
  } finally { f.close() }
})

test('switching vendor clears a connection result that described the old one', async () => {
  const f = await fixture({ providers: switchProviders })
  try {
    f.vm.formData.provider = 'vendor-a'
    f.vm.handleProviderChange('vendor-a')
    await nextTick()
    f.vm.formData.modelName = 'shared-model'
    await f.vm.checkRemoteAPI()
    assert.equal(f.vm.remoteChecked, true)

    f.vm.formData.provider = 'vendor-b'
    f.vm.handleProviderChange('vendor-b')
    await nextTick()
    assert.equal(f.vm.remoteChecked, false)
    assert.equal(f.vm.remoteMessage, '')
  } finally { f.close() }
})

// A VLM entry is a chat model that accepts images, so the backend — which
// already scopes the provider list to the requested model type — returns it
// typed "chat". Re-filtering on that type in the editor emptied the picker
// for every vendor under 视觉.
const vlmProviders = [{
  value: 'vendor-v', label: 'Vendor V', description: '', order: 1, modelTypes: ['chat', 'vllm'],
  defaultUrls: { chat: 'https://v.example.com/v1', vllm: 'https://v.example.com/v1' },
  models: [
    { id: 'sees-images', name: 'Sees Images', type: 'chat', input: ['text', 'image'], context_window: 128000 },
  ],
}]

test('the vision picker lists the vendor models the backend scoped to it', async () => {
  const f = await fixture({ type: 'vllm', providers: vlmProviders })
  try {
    f.vm.formData.provider = 'vendor-v'
    f.vm.handleProviderChange('vendor-v')
    await nextTick()
    assert.deepEqual(f.vm.catalogModelOptions.map((o: any) => o.value), ['sees-images'])
    assert.equal(f.vm.catalogModelOptions[0].vision, true)
  } finally { f.close() }
})

const cloudProvider = {
  value: 'weknoracloud', label: 'WeKnora Cloud', description: '', order: 1,
  modelTypes: ['chat', 'embedding', 'rerank', 'vllm'],
  defaultUrls: Object.fromEntries(['chat', 'embedding', 'rerank', 'vllm'].map(type => [type, 'https://weknora.weixin.qq.com'])),
  models: [],
}

for (const [type, modelName] of [['chat', 'chat'], ['embedding', 'embedding'], ['rerank', 'rerank'], ['vllm', 'vlm']]) {
  test(`WeKnora Cloud ${type}: an empty catalog still offers the managed model`, async () => {
    const f = await fixture({ type, providers: [cloudProvider] })
    try {
      f.vm.formData.provider = 'weknoracloud'
      f.vm.handleProviderChange('weknoracloud')
      await nextTick()
      await f.vm.checkWkcCredentialStatus()
      assert.equal(f.vm.wkcCredentialState, 'configured')
      assert.deepEqual(Array.from(f.vm.catalogModelOptions, (o: any) => o.value), [modelName])
      assert.equal(f.vm.catalogModelOptions[0].vision, type === 'vllm')
      f.vm.formData.modelName = modelName
      f.vm.handleCatalogModelChange(modelName)
      await nextTick()
      await f.vm.checkRemoteAPI()
      assert.equal(f.requests[0].modelName, modelName)
      assert.equal(f.requests[0].provider, 'weknoracloud')
      assert.equal(f.vm.formData.contextWindow, undefined)
      assert.equal(f.vm.formData.dimension, undefined)
    } finally { f.close() }
  })
}

test('switching WeKnora Cloud model types updates the managed choices', async () => {
  const f = await fixture({ providers: [cloudProvider] })
  try {
    f.vm.formData.provider = 'weknoracloud'
    f.vm.handleProviderChange('weknoracloud')
    await nextTick()
    f.vm.formData.modelName = 'chat'
    await f.vm.selectModelType('vllm')
    assert.equal(f.vm.formData.modelName, '')
    assert.deepEqual(Array.from(f.vm.catalogModelOptions, (o: any) => o.value), ['vlm'])
  } finally { f.close() }
})

test('rerank and asr pickers list their own entries', async () => {
  for (const [type, id] of [['rerank', 'the-reranker'], ['asr', 'the-transcriber']]) {
    const providers = [{
      value: 'vendor-t', label: 'Vendor T', description: '', order: 1, modelTypes: [type],
      defaultUrls: { [type]: 'https://t.example.com/v1' },
      models: [{ id, name: id, type }],
    }]
    const f = await fixture({ type, providers })
    try {
      f.vm.formData.provider = 'vendor-t'
      f.vm.handleProviderChange('vendor-t')
      await nextTick()
      assert.deepEqual(f.vm.catalogModelOptions.map((o: any) => o.value), [id], type)
    } finally { f.close() }
  }
})

// TDesign hides a select's popup when it has no options, which takes the
// creatable "create" row with it: the field accepts keystrokes but offers no
// way to commit them, and a blur throws the text away. That is every vendor
// with no catalog for the current type — 自定义 (OpenAI 兼容接口) above all.
test('a vendor with no catalog falls back to a plain text field', async () => {
  const f = await fixture({ providers: switchProviders })
  try {
    f.vm.formData.provider = 'vendor-a'
    f.vm.handleProviderChange('vendor-a')
    await nextTick()
    assert.ok(f.vm.catalogModelOptions.length > 0, 'a catalogued vendor keeps the picker')

    const f2 = await fixture({ providers: [{
      value: 'bare', label: 'Bare', description: '', order: 1, modelTypes: ['chat'],
      defaultUrls: { chat: 'https://bare.example.com/v1' }, models: [],
    }] })
    try {
      f2.vm.formData.provider = 'bare'
      f2.vm.handleProviderChange('bare')
      await nextTick()
      assert.equal(f2.vm.catalogModelOptions.length, 0)
      // Typing is the only way in, and it must survive as the model name.
      f2.vm.formData.modelName = 'my-own-model'
      await nextTick()
      assert.equal(f2.vm.formData.modelName, 'my-own-model')
    } finally { f2.close() }
  } finally { f.close() }
})

// --- Vendor strings reach the form in the reader's language ---------------

// The editor renders whatever the catalog declares, so every operator-facing
// string a vendor ships has to go through the locale resolvers. Binding a raw
// `field.placeholder` or `opt.label` is silent: the form still works, it is
// just English in a Chinese product, and a test that only checks the catalog
// for a zh-CN entry passes while the editor ignores it. This reads the
// template instead.
test('every vendor-declared string in the form goes through a locale resolver', () => {
  const template = descriptor.template?.content ?? ''
  assert.ok(template.length > 0, 'the editor template should have been parsed')

  // `field` is the extra-field loop variable, so any read of its placeholder
  // is a vendor string rendered without resolving the locale. One input
  // branch per field type plus the secret field means it is easy to bind four
  // and miss the fifth, which is exactly what happened.
  for (const pattern of [
    /:placeholder="field\.placeholder/,
    /\{\{\s*field\.placeholder\s*\}\}/,
    /v-if="field\.placeholder"/,
  ]) {
    assert.equal(
      pattern.test(template), false,
      `template reads field.placeholder directly (${pattern}); use extraFieldDisplayPlaceholder`,
    )
  }

  // Options: scope to the extra-field select, because `opt` is also the loop
  // variable of the model-type list and the catalog model list, whose labels
  // come from i18n and from the catalog's own names.
  const optionLoop = /v-for="opt in \(field\.options \|\| \[\]\)"[^>]*/.exec(template)
  assert.ok(optionLoop, 'the extra-field select should loop over field.options')
  assert.match(
    optionLoop[0], /:label="extraFieldDisplayOptionLabel\(opt\)"/,
    'extra-field option labels must resolve the locale',
  )

  // And the placeholder resolver is referenced, so the checks above cannot be
  // satisfied by dropping the placeholders altogether.
  assert.ok(template.includes('extraFieldDisplayPlaceholder('), 'placeholders must use the resolver')
})

test('the chat protocol override is offered only on chat and vision rows', () => {
  const template = descriptor.template?.content ?? ''
  // extra_config.api names a chat protocol. Embedding and rerank rows never
  // read it, so offering it there is a control that silently does nothing.
  const select = /<div([^>]*)>\s*<label[^>]*>\{\{ \$t\('model\.editor\.advanced\.api\.label'\) \}\}/.exec(template)
  assert.ok(select, 'the protocol override should still be in the template')
  assert.match(select[1], /v-if="isChatLike"/, 'the protocol override must be scoped to chat-like rows')
})

test('extraFieldDisplayPlaceholder resolves the vendor placeholder for the active locale', async () => {
  const f = await fixture({ type: 'rerank', providers: catalogProviders })
  try {
    const field = {
      key: 'score_scale',
      label: 'Rerank score scale',
      type: 'select',
      placeholder: 'match the reranker actually deployed behind this endpoint',
      placeholders: { 'zh-CN': '按这个端点后面实际部署的重排模型选择' },
    }
    const rendered = f.vm.extraFieldDisplayPlaceholder(field)
    assert.ok(
      rendered === field.placeholder || rendered === field.placeholders['zh-CN'],
      `expected one of the declared variants, got ${rendered}`,
    )
    assert.equal(f.vm.extraFieldDisplayPlaceholder({ key: 'k', label: 'k', type: 'string' }), '')
  } finally {
    f.close()
  }
})

for (const type of ['chat', 'embedding', 'rerank', 'vllm', 'asr']) {
  test(`${type}: connection tests include edited spec and clearing compat`, async () => {
    const f = await fixture({ type })
    try {
      f.vm.formData.spec = { context_window: 64000, compat: { old: true } }
      f.vm.formData.specCompat = '{"custom_option": true}'
      await f.vm.checkRemoteAPI()
      assert.deepEqual(plain(f.requests[0].spec), { context_window: 64000, compat: { custom_option: true } })
      f.vm.formData.specCompat = ''
      await nextTick()
      assert.equal(f.vm.remoteChecked, false, 'editing spec invalidates the old connection result')
      await f.vm.checkRemoteAPI()
      assert.deepEqual(plain(f.requests[1].spec), { context_window: 64000 })
    } finally { f.close() }
  })
}

test('capability preview includes the same edited spec as connection tests', async () => {
  const f = await fixture({ type: 'chat' })
  try {
    f.vm.formData.provider = 'openai'
    f.vm.formData.spec = { api: 'openai-completions' }
    f.vm.formData.specCompat = '{"supports_temperature": false}'
    await f.vm.runResolve()
    assert.deepEqual(plain(f.resolves.at(-1).spec), { api: 'openai-completions', compat: { supports_temperature: false } })
  } finally { f.close() }
})
