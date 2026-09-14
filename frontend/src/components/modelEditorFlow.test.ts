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
} = {}) {
  const requests: any[] = []
  const saves: any[] = []
  const toasts: string[] = []
  const visibility: boolean[] = []
  const props = reactive({
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
    exports, URL, setInterval, clearInterval,
    window: { addEventListener() {}, removeEventListener() {} },
    console: { error() {}, debug() {}, log() {} },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key, te: () => false }) }
      if (name === '@/stores/ui') return { useUIStore: () => ({}) }
      if (name === 'tdesign-vue-next') return {
        MessagePlugin: Object.fromEntries(['success', 'error', 'warning', 'info'].map(key => [key, (text: string) => toasts.push(text)])),
      }
      if (name === '@/api/initialization') return {
        checkRemoteModel: check, testEmbeddingModel: check, checkRerankModel: check, checkASRModel: check,
        listModelProviders: async () => [], checkOllamaStatus: async () => ({ available: false }),
      }
      if (name === '@/utils/thinkingControl') return require('../utils/thinkingControl.ts')
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
  return { vm, props, requests, saves, toasts, visibility, close: () => app.unmount() }
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
