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
const filename = fileURLToPath(new URL('./ModelDebugDrawer.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'model-debug-images' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

async function fixture(type: string, images = true, legacyVision = false) {
  const requests: any[] = []
  const revoked: string[] = []
  const props = reactive({ visible: false, models: [
    { id: 'image-model', name: 'model', type, source: 'remote', parameters: { supports_vision: legacyVision }, capabilities: { input: images ? ['text', 'image'] : ['text'], thinking_levels: [] } },
    { id: 'text-model', name: 'text', type, source: 'remote', parameters: {}, capabilities: { input: ['text'], thinking_levels: [] } },
  ] })
  const exports: any = {}
  runInNewContext(compiled, {
    exports, console, document: { activeElement: null }, HTMLElement: class {},
    URL: { createObjectURL: () => 'blob:preview', revokeObjectURL: (url: string) => revoked.push(url) },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key, locale: { value: 'en-US' } }) }
      if (name === '@/api/model') return { debugModel: async (id: string, data: any) => { requests.push({ id, ...data }); return { ok: true, elapsed_ms: 1, raw_response: [], observations: {} } } }
      if (name === '@/stores/modelProviders') return { useModelProvidersStore: () => ({ ensureLoaded: async () => {}, labelFor: () => '' }) }
      if (name === '@/utils') return { fileSizeVerification: () => false }
      if (name === '@/utils/reasoningEffort') return require('../utils/reasoningEffort.ts')
      if (name === '@/utils/contextWindow') return require('../utils/contextWindow.ts')
      if (name === 'tdesign-vue-next') return { MessagePlugin: { error() {} } }
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
  const app = renderer.createApp({ setup: () => () => h(exports.default, { ...props, ref: (value: any) => { vm = value } }) })
  app.mount({})
  props.visible = true
  await nextTick()
  return { vm, requests, revoked, dispose: () => app.unmount() }
}
const file = { name: 'cat.png', size: 100, type: 'image/png' }

for (const type of ['Embedding', 'Rerank']) {
  test(`${type} image mode sends the upload without stale text documents`, async () => {
    const f = await fixture(type)
    try {
      assert.equal(f.vm.hasImageMode, true)
      assert.equal(f.vm.showFilePicker, false)
      f.vm.input = 'a cat'
      f.vm.documentsText = 'old candidate'
      f.vm.inputMode = 'image'
      assert.equal(f.vm.canRun, false)
      f.vm.onNativeFileChange({ target: { files: [file] } })
      await nextTick()
      assert.equal(f.vm.imagePreview, 'blob:preview')
      assert.equal(f.vm.canRun, true)
      await f.vm.runDebug()
      assert.equal(f.requests[0].file.name, file.name)
      assert.equal(f.requests[0].input, type === 'Embedding' ? '' : 'a cat')
      assert.equal(f.requests[0].documents.length, 0)
      f.vm.clearFile()
      await nextTick()
      assert.equal(f.vm.canRun, false)
      assert.deepEqual(f.revoked, ['blob:preview'])
    } finally { f.dispose() }
  })
}

test('embedding similarity sends the query, candidates and an optional image', async () => {
  const f = await fixture('Embedding')
  try {
    f.vm.inputMode = 'similarity'
    await nextTick()
    assert.equal(f.vm.useSimilarityMode, true)
    assert.equal(f.vm.showFilePicker, true)
    assert.equal(f.vm.canRun, false)
    f.vm.input = 'a dog'
    assert.equal(f.vm.canRun, false, 'a query alone has nothing to compare against')
    f.vm.documentsText = 'a puppy\n\na car'
    assert.equal(f.vm.canRun, true)
    await f.vm.runDebug()
    assert.equal(f.requests[0].input, 'a dog')
    assert.deepEqual([...f.requests[0].documents], ['a puppy', 'a car'])
    assert.equal(f.requests[0].options.similarity, true)
    assert.equal(f.requests[0].file, null)

    f.vm.documentsText = ''
    f.vm.onNativeFileChange({ target: { files: [file] } })
    assert.equal(f.vm.canRun, true, 'an image is a candidate on its own')
    await f.vm.runDebug()
    assert.equal(f.requests[1].file.name, file.name)
  } finally { f.dispose() }
})

test('embedding similarity is offered on text-only models without an image picker', async () => {
  const f = await fixture('Embedding', false)
  try {
    assert.equal(f.vm.hasImageMode, false)
    f.vm.inputMode = 'similarity'
    await nextTick()
    assert.equal(f.vm.showFilePicker, false)
    f.vm.input = 'a dog'
    f.vm.documentsText = 'a puppy'
    await f.vm.runDebug()
    assert.equal(f.requests[0].file, null)
    assert.equal(f.requests[0].options.similarity, true)
  } finally { f.dispose() }
})

test('image-capable chat sends text and an optional image, including image-only input', async () => {
  const f = await fixture('KnowledgeQA')
  try {
    assert.equal(f.vm.showFilePicker, true)
    assert.equal(f.vm.hasImageMode, false)
    f.vm.onNativeFileChange({ target: { files: [file] } })
    assert.equal(f.vm.canRun, true)
    await f.vm.runDebug()
    assert.equal(f.requests[0].input, '')
    assert.equal(f.requests[0].file.name, file.name)
    f.vm.input = 'describe'
    await f.vm.runDebug()
    assert.equal(f.requests[1].input, 'describe')
    assert.equal(f.requests[1].file.name, file.name)
  } finally { f.dispose() }
})

for (const type of ['KnowledgeQA', 'Embedding', 'Rerank']) {
  test(`${type} clears the file when switching to a text-only model of the same type`, async () => {
    const f = await fixture(type)
    try {
      f.vm.inputMode = 'image'
      f.vm.onNativeFileChange({ target: { files: [file] } })
      await nextTick()
      f.vm.selectedModelId = 'text-model'
      await nextTick()
      assert.equal(f.vm.file, null)
      assert.equal(f.vm.inputMode, 'text')
      assert.equal(f.vm.showFilePicker, false)
      assert.equal(f.vm.imagePreview, '')
    } finally { f.dispose() }
  })
}

test('legacy vision flag enables chat image uploads', async () => {
  const f = await fixture('KnowledgeQA', false, true)
  try { assert.equal(f.vm.showFilePicker, true) } finally { f.dispose() }
})

for (const type of ['VLLM', 'ASR']) {
  test(`${type} still requires its file`, async () => {
    const f = await fixture(type, false)
    try {
      assert.equal(f.vm.showFilePicker, true)
      assert.equal(f.vm.canRun, false)
      f.vm.onNativeFileChange({ target: { files: [file] } })
      assert.equal(f.vm.canRun, true)
      await f.vm.runDebug()
      assert.equal(f.requests[0].file.name, file.name)
    } finally { f.dispose() }
  })
}

// The video tab is its own entry in the debug drawer: it is a real model type,
// not a chat model with video input, and it always takes a file (the video is
// what gets read; the prompt only says what to look for).
test('video models get their own tab, accept a video file and send it with the prompt', async () => {
  const f = await fixture('Video', false)
  try {
    const types = Array.from(f.vm.allModelTypeOptions, (option: any) => option.value)
    assert.ok(types.includes('Video'), 'the drawer must offer a video tab')
    assert.equal(f.vm.selectedModelType, 'Video')

    const video = { name: 'lesson.mp4', size: 4096, type: 'video/mp4' }
    assert.equal(f.vm.showFilePicker, true)
    assert.equal(f.vm.canRun, false, 'a video model has nothing to send without a file')
    f.vm.onNativeFileChange({ target: { files: [video] } })
    await nextTick()
    assert.equal(f.vm.canRun, true, 'the file alone is enough: the prompt is optional')
    assert.equal(f.vm.imagePreview, '', 'a video must not be rendered as an <img> preview')

    f.vm.input = '这段视频讲了什么？'
    await f.vm.runDebug()
    assert.equal(f.requests[0].file.name, 'lesson.mp4')
    assert.equal(f.requests[0].input, '这段视频讲了什么？')
  } finally { f.dispose() }
})
