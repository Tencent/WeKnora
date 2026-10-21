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
const filename = fileURLToPath(new URL('./ImagePipelineSettings.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'image-pipeline-settings-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

const violation = { message_key: 'imagePipeline.errors.noActionEnabled', field: 'enable_caption' }
const flushUpdates = async () => {
  await new Promise<void>((resolve) => setImmediate(resolve))
  await nextTick()
}

type ValidationRequest = {
  pipelineId: string
  params: Record<string, unknown>
  resolve: (value: { violations: typeof violation[] }) => void
  reject: (reason: Error) => void
}

async function fixture() {
  const requests: ValidationRequest[] = []
  const timers = new Map<number, () => void>()
  let timerID = 0
  const exports: any = {}
  runInNewContext(compiled, {
    exports, console: { ...console, error() {} },
    setTimeout(callback: () => void) { timers.set(++timerID, callback); return timerID },
    clearTimeout(id: number) { timers.delete(id) },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key, te: () => false }) }
      if (name === '@/api/knowledge-base') return {
        fetchImagePipelines: async () => [{ id: 'default', fields: [] }, { id: 'smartocr', fields: [] }],
        validateImagePipelines: (pipelineId: string, params: Record<string, unknown>) =>
          new Promise((resolve, reject) => requests.push({ pipelineId, params, resolve, reject })),
      }
      throw new Error(`Unexpected import: ${name}`)
    },
  })
  exports.default.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const props = reactive({ pipelineId: 'default', params: { enable_caption: false, enable_ocr: false } as Record<string, unknown> })
  const emitted: boolean[] = []
  let vm: any
  const app = renderer.createApp({ setup: () => () => h(exports.default, {
    ...props,
    ref: (value: any) => { vm = value },
    'onUpdate:params': (params: Record<string, unknown>) => { props.params = params },
    'onUpdate:pipelineId': (pipelineId: string) => { props.pipelineId = pipelineId },
    'onUpdate:invalid': (invalid: boolean) => { emitted.push(invalid) },
  }) })
  app.mount({})
  await flushUpdates()
  return {
    requests, timers, emitted,
    get invalid() { return emitted.at(-1) },
    get validationFailed() { return vm.validationFailed },
    async setCaption(enabled: boolean) { vm.onParamChange('enable_caption', enabled); await flushUpdates() },
    async setPipeline(id: string) { vm.onPipelineChange(id); await flushUpdates() },
    async validate() {
      for (const [id, callback] of [...timers]) { timers.delete(id); callback() }
      await flushUpdates()
    },
    dispose: () => app.unmount(),
  }
}

test('a late invalid response cannot block saving newer valid parameters', async () => {
  const f = await fixture()
  try {
    await f.validate()
    await f.setCaption(true)
    await f.validate()
    assert.equal(f.requests.length, 2)
    assert.equal(f.requests[0].params.enable_caption, false)
    assert.equal(f.requests[1].params.enable_caption, true)
    f.requests[1].resolve({ violations: [] })
    await flushUpdates()
    f.requests[0].resolve({ violations: [violation] })
    await flushUpdates()
    assert.equal(f.invalid, false)
  } finally { f.dispose() }
})

for (const response of ['valid', 'error'] as const) {
  test(`a stale ${response} response cannot erase the current invalid verdict`, async () => {
    const f = await fixture()
    try {
      await f.setCaption(true)
      await f.validate()
      await f.setCaption(false)
      await f.validate()
      f.requests[1].resolve({ violations: [violation] })
      await flushUpdates()
      assert.equal(f.invalid, true)
      if (response === 'valid') f.requests[0].resolve({ violations: [] })
      else f.requests[0].reject(new Error('old request failed'))
      await flushUpdates()
      assert.equal(f.invalid, true)
      assert.equal(f.validationFailed, false)
    } finally { f.dispose() }
  })
}

test('parameter changes invalidate responses even before the next debounce fires', async () => {
  const f = await fixture()
  try {
    await f.validate()
    await f.setCaption(true)
    assert.equal(f.requests.length, 1)
    f.requests[0].resolve({ violations: [violation] })
    await flushUpdates()
    assert.equal(f.invalid, false)
  } finally { f.dispose() }
})

test('switching pipelines clears the previous verdict and ignores its pending request', async () => {
  const f = await fixture()
  try {
    await f.validate()
    f.requests[0].resolve({ violations: [violation] })
    await flushUpdates()
    assert.equal(f.invalid, true)
    await f.setCaption(true)
    await f.validate()
    await f.setPipeline('smartocr')
    assert.equal(f.invalid, false)
    await f.validate()
    assert.equal(f.requests[2].pipelineId, 'smartocr')
    assert.deepEqual({ ...f.requests[2].params }, {})
    f.requests[2].resolve({ violations: [] })
    f.requests[1].resolve({ violations: [violation] })
    await flushUpdates()
    assert.equal(f.invalid, false)
  } finally { f.dispose() }
})

test('unmount cancels scheduled validation and ignores in-flight results', async () => {
  const f = await fixture()
  await f.validate()
  await f.setCaption(true)
  const emitted = [...f.emitted]
  f.dispose()
  assert.equal(f.timers.size, 0)
  f.requests[0].resolve({ violations: [violation] })
  await flushUpdates()
  assert.deepEqual(f.emitted, emitted)
  await f.validate()
  assert.equal(f.requests.length, 1)
})

test('a current validation failure still permits saving and is cleared by the next result', async () => {
  const f = await fixture()
  try {
    await f.validate()
    f.requests[0].reject(new Error('network unavailable'))
    await flushUpdates()
    assert.equal(f.invalid, false)
    assert.equal(f.validationFailed, true)
    await f.setCaption(true)
    await f.validate()
    f.requests[1].resolve({ violations: [] })
    await flushUpdates()
    assert.equal(f.validationFailed, false)
    assert.equal(f.invalid, false)
  } finally { f.dispose() }
})
