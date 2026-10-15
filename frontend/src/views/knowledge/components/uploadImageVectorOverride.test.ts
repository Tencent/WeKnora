import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h } from 'vue'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./UploadConfirmDialog.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'upload-image-vector-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function fixture(kbInfo: any) {
  const exports: any = {}
  runInNewContext(compiled, {
    exports, console,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key, te: () => false }) }
      if (name === '@/stores/chatResources') return { useChatResourcesStore: () => ({}) }
      if (name === '@/stores/editorResources') return { useEditorResourcesStore: () => ({}) }
      if (name === '@/stores/ui') return { useUIStore: () => ({}) }
      if (name === '@/api/knowledge-base') return { mergeImageActions: () => ({ ocr: { on: [], on_unobserved: true } }) }
      // UploadConfirmDialog also imports the image-pipeline helpers (merged from
      // the image-pipeline PR). The real module is TypeScript, which node:vm
      // cannot require, so mirror its behaviour with these minimal stubs.
      if (name === '@/utils/imageProcessingConfig') {
        return {
          IMAGE_PIPELINE_DEFAULT: 'default',
          IMAGE_PIPELINE_SMARTOCR: 'smartocr',
          normalizeImagePipelineId: (id: string) => id,
          buildPipelineFields: (pipelineId: string, pipelineParams: Record<string, unknown>) =>
            pipelineId
              ? {
                  image_pipeline: pipelineId,
                  ...(pipelineParams && Object.keys(pipelineParams).length > 0 ? { image_pipeline_params: pipelineParams } : {}),
                }
              : {},
          resolveImagePipelineFromKb: () => 'default',
        }
      }
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
  const app = renderer.createApp({ setup: () => () => h(exports.default, { visible: false, kbInfo, ref: (value: any) => { vm = value } }) })
  app.mount({})
  vm.initFromKbInfo(kbInfo)
  return { vm, dispose: () => app.unmount() }
}

for (const enabled of [false, true]) {
  test(`upload inherits ${enabled} and submits the explicit opposite without changing the KB`, () => {
    const kb = { embedding_model_id: 'shared-embedding', image_processing_config: { image_vector_enabled: enabled } }
    const f = fixture(kb)
    try {
      assert.equal(f.vm.uiState.imageVectorEnabled, enabled)
      f.vm.uiState.imageVectorEnabled = !enabled
      const payload = JSON.parse(JSON.stringify(f.vm.buildProcessOverrides()))
      assert.equal(payload.image_vector_enabled, !enabled)
      assert.equal(kb.image_processing_config.image_vector_enabled, enabled)
      assert.equal('embedding_model_id' in payload, false)
    } finally { f.dispose() }
  })
  test(`reparse restores an explicit ${enabled} over the KB default`, () => {
    const f = fixture({ image_processing_config: { image_vector_enabled: !enabled } })
    try {
      f.vm.applyOverridesToState({ image_vector_enabled: enabled })
      assert.equal(f.vm.uiState.imageVectorEnabled, enabled)
      assert.equal(f.vm.buildProcessOverrides().image_vector_enabled, enabled)
    } finally { f.dispose() }
  })
}

test('old documents inherit the KB value and old KBs default off', () => {
  const f = fixture({ image_processing_config: { image_vector_enabled: true } })
  try {
    f.vm.applyOverridesToState({ summary_enabled: false })
    assert.equal(f.vm.uiState.imageVectorEnabled, true)
    f.vm.initFromKbInfo({})
    assert.equal(f.vm.uiState.imageVectorEnabled, false)
  } finally { f.dispose() }
})

test('image capability warning uses the KB embedding model and tolerates shared models absent from the list', () => {
  const f = fixture({ embedding_model_id: 'embedding' })
  try {
    assert.equal(f.vm.embeddingTakesImages, true)
    f.vm.allModels = [{ id: 'embedding', capabilities: { input: ['text'] } }]
    assert.equal(f.vm.embeddingTakesImages, false)
    f.vm.allModels = [{ id: 'embedding', capabilities: { input: ['text', 'image'] } }]
    assert.equal(f.vm.embeddingTakesImages, true)
  } finally { f.dispose() }
})
