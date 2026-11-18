import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h } from 'vue'
import * as imageProcessingConfig from '../../../utils/imageProcessingConfig'
import * as graphExtractValidation from '../settings/graphExtractValidation'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./UploadConfirmDialog.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'upload-image-vector-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function fixture(kbInfo: any, mode: 'file' | 'reparse' = 'file') {
  const exports: any = {}
  const warnings: string[] = []
  runInNewContext(compiled, {
    exports, console,
    document: { querySelector: () => null },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key, te: () => false }) }
      if (name === '@/stores/chatResources') return { useChatResourcesStore: () => ({}) }
      if (name === '@/stores/editorResources') return { useEditorResourcesStore: () => ({ systemInfo: { graph_database_engine: 'Neo4j' } }) }
      if (name === '@/stores/ui') return { useUIStore: () => ({}) }
      if (name === '@/api/knowledge-base') return { mergeImageActions: () => ({ ocr: { on: [] } }) }
      if (name === '@/utils/imageProcessingConfig') return imageProcessingConfig
      if (name === '../settings/graphExtractValidation') return graphExtractValidation
      if (name === 'tdesign-vue-next') return { MessagePlugin: { warning: (key: string) => warnings.push(key) } }
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
  const app = renderer.createApp({ setup: () => () => h(exports.default, { visible: false, kbInfo, mode, ref: (value: any) => { vm = value } }) })
  app.mount({})
  vm.initFromKbInfo(kbInfo)
  return { vm, warnings, dispose: () => app.unmount() }
}

// 上传和重解析的空字段表示继承 KB，验证对话框实际的确认入口及提交数据。
const graphKb = () => ({
  indexing_strategy: { graph_enabled: true },
  extract_config: {
    enabled: true,
    text: 'A knows B.',
    tags: ['Knows'],
    nodes: [{ name: 'A', attributes: [] }, { name: 'B', attributes: [] }],
    relations: [{ node1: 'A', node2: 'B', type: 'Knows' }],
  },
})

for (const mode of ['file', 'reparse'] as const) {
  test(`${mode}: removing the last relation in place preserves KB defaults for inheritance`, () => {
    const kb = graphKb()
    const snapshot = structuredClone(kb)
    const f = fixture(kb, mode)
    try {
      // 对应 GraphSettings.removeRelation，而不是用新数组替换整个字段。
      f.vm.uiState.nodeExtractConfig.relations.splice(0, 1)
      f.vm.handleNodeExtractUpdate(f.vm.uiState.nodeExtractConfig)
      assert.deepEqual(kb, snapshot)
      assert.equal(f.vm.validateBeforeConfirm(), true)
      assert.deepEqual(f.warnings, [])
      assert.deepEqual(JSON.parse(JSON.stringify(f.vm.buildProcessOverrides().extract_config.relations)), [])
    } finally { f.dispose() }
  })

  test(`${mode}: editing a relation object does not change the KB example`, () => {
    const kb = graphKb()
    const snapshot = structuredClone(kb)
    const f = fixture(kb, mode)
    try {
      const relation = f.vm.uiState.nodeExtractConfig.relations[0]
      relation.node1 = 'B'
      relation.node2 = 'A'
      f.vm.handleNodeExtractUpdate(f.vm.uiState.nodeExtractConfig)
      assert.deepEqual(kb, snapshot)
      assert.equal(f.vm.validateBeforeConfirm(), true)
      assert.deepEqual(JSON.parse(JSON.stringify(f.vm.buildProcessOverrides().extract_config.relations)),
        [{ node1: 'B', node2: 'A', type: 'Knows' }])
    } finally { f.dispose() }
  })

  for (const field of ['text', 'tags', 'nodes', 'relations', 'all'] as const) {
    test(`${mode}: cleared extraction ${field} inherits the KB example without rewriting the override`, () => {
      const kb = graphKb()
      const snapshot = structuredClone(kb)
      const f = fixture(kb, mode)
      try {
        const cleared = { text: '', tags: [], nodes: [], relations: [] }
        const override = { enabled: true, ...(field === 'all' ? cleared : { [field]: cleared[field] }) }
        if (mode === 'reparse') {
          f.vm.applyOverridesToState({ extract_config: override })
        } else {
          f.vm.handleNodeExtractUpdate({ ...f.vm.uiState.nodeExtractConfig, ...override })
        }
        assert.equal(f.vm.validateBeforeConfirm(), true)
        assert.deepEqual(f.warnings, [])
        const submitted = JSON.parse(JSON.stringify(f.vm.buildProcessOverrides().extract_config))
        for (const key of (field === 'all' ? ['text', 'tags', 'nodes', 'relations'] : [field])) {
          assert.deepEqual(submitted[key], cleared[key as keyof typeof cleared])
        }
        assert.deepEqual(kb, snapshot)
      } finally { f.dispose() }
    })
  }

  test(`${mode}: inherited extraction data must still be complete`, () => {
    const kb = graphKb()
    kb.extract_config.text = ''
    const f = fixture(kb, mode)
    try {
      assert.equal(f.vm.validateBeforeConfirm(), false)
      assert.deepEqual(f.warnings, ['graphSettings.extractIncompleteWarning'])
      assert.equal(f.vm.activeSection, 'graph')
    } finally { f.dispose() }
  })

  test(`${mode}: a nonempty invalid override must not fall back to the valid KB example`, () => {
    const f = fixture(graphKb(), mode)
    try {
      f.vm.applyOverridesToState({ extract_config: { enabled: true, nodes: [{ name: '' }] } })
      assert.equal(f.vm.validateBeforeConfirm(), false)
      assert.deepEqual(f.warnings, ['graphSettings.extractIncompleteWarning'])
    } finally { f.dispose() }
  })
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

for (const pipelineId of ['default', 'smartocr', 'caption_ocr', 'ob_cap_ocr']) {
  for (const params of [undefined, {}, { 'default.caption.thinking_enabled': false }]) {
    test(`reparse replaces KB parameters with document ${pipelineId} parameters ${JSON.stringify(params)}`, () => {
      const kb = {
        vlm_config: { enabled: true, model_id: 'vlm' },
        image_processing_config: { image_pipeline: 'default', image_pipeline_params: { 'default.caption_enabled': false, 'default.caption.thinking_enabled': true } },
      }
      const f = fixture(kb)
      try {
        f.vm.applyOverridesToState({ image_pipeline: pipelineId, image_pipeline_params: params })
        const result = JSON.parse(JSON.stringify(f.vm.buildProcessOverrides()))
        assert.equal(result.image_pipeline, imageProcessingConfig.normalizeImagePipelineId(pipelineId))
        assert.deepEqual(result.image_pipeline_params ?? {}, params ?? {})
        assert.deepEqual(kb.image_processing_config.image_pipeline_params, { 'default.caption_enabled': false, 'default.caption.thinking_enabled': true })
      } finally { f.dispose() }
    })
  }
}

for (const kbPipeline of ['default', 'smartocr']) {
  for (const attrsEnabled of [false, true]) {
    test(`legacy reparse override ${attrsEnabled} only retains parameters from ${kbPipeline} when the pipeline matches`, () => {
      const params = { 'default.caption.thinking_enabled': true }
      const f = fixture({
        vlm_config: { enabled: true, model_id: 'vlm' },
        image_processing_config: { image_pipeline: kbPipeline, image_pipeline_params: params },
      })
      try {
        f.vm.applyOverridesToState({ image_attrs_enabled: attrsEnabled })
        const result = JSON.parse(JSON.stringify(f.vm.buildProcessOverrides()))
        const pipeline = attrsEnabled ? 'smartocr' : 'default'
        assert.equal(result.image_pipeline, pipeline)
        assert.deepEqual(result.image_pipeline_params ?? {}, kbPipeline === pipeline ? params : {})
      } finally { f.dispose() }
    })
  }
}

test('reparse inherits KB parameters when the document has no pipeline selection', () => {
  const params = { 'default.caption_enabled': false }
  const f = fixture({
    vlm_config: { enabled: true, model_id: 'vlm' },
    image_processing_config: { image_pipeline: 'default', image_pipeline_params: params },
  })
  try {
    for (const overrides of [undefined, { summary_enabled: false }, { image_pipeline_params: { 'default.caption_enabled': true } }]) {
      f.vm.applyOverridesToState(overrides)
      assert.deepEqual(JSON.parse(JSON.stringify(f.vm.buildProcessOverrides().image_pipeline_params)), params)
    }
  } finally { f.dispose() }
})
