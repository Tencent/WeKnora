import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import { parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { cloneGraphSchema } from '../../types/graphSchema.ts'

function sourceFor(path: string) {
  const { descriptor } = parse(readFileSync(new URL(path, import.meta.url), 'utf8'))
  return ts.createSourceFile(path, descriptor.scriptSetup!.content, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
}

const editor = sourceFor('./KnowledgeBaseEditorModal.vue')
const upload = sourceFor('./components/UploadConfirmDialog.vue')
const plain = (value: unknown) => JSON.parse(JSON.stringify(value))

// Execute the parents' real load/save expressions, rather than a duplicate mapper in the test.
function evaluate(source: ts.SourceFile, select: (node: ts.Node) => boolean, context: Record<string, unknown>) {
  const matches: ts.Node[] = []
  function visit(node: ts.Node) {
    if (select(node)) matches.push(node)
    ts.forEachChild(node, visit)
  }
  visit(source)
  assert.equal(matches.length, 1, 'Expected one parent configuration expression')
  const selected = matches[0]!
  const expression = ts.isPropertyAssignment(selected) ? selected.initializer.getText(source) :
    ts.isBinaryExpression(selected) ? selected.right.getText(source) :
    ts.isVariableDeclaration(selected) ? selected.initializer!.getText(source) : selected.getText(source)
  const isFunction = ts.isFunctionDeclaration(selected)
  const script = ts.transpileModule(
    isFunction ? `${expression}; result = ${selected.name!.text}(input);` : `result = (${expression});`,
    { compilerOptions: { target: ts.ScriptTarget.ES2022 } },
  ).outputText
  const sandbox = { ...context, cloneGraphSchema, result: undefined }
  runInNewContext(script, sandbox)
  return sandbox.result as any
}

function property(name: string, marker: string) {
  return (node: ts.Node) => (
    ts.isPropertyAssignment(node) && node.name.getText() === name && node.initializer.getText().includes(marker)
  ) || (
    ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.EqualsToken &&
    ts.isPropertyAccessExpression(node.left) && node.left.name.text === name && node.right.getText().includes(marker)
  )
}

function sample() {
  return {
    enabled: true,
    schema: {
      enabled: true,
      entity_types: [{ name: 'Person' }, { name: 'Company' }],
      relation_types: [{ name: 'WORKS_AT', source_type: 'Person', target_type: 'Company' }],
    },
    text: 'Alex works at Alex.',
    tags: [],
    nodes: [
      { name: 'Alex', entity_type: 'Person', attributes: [] },
      { name: 'Alex', entity_type: 'Company', attributes: [] },
    ],
    relations: [{ node1: 'Alex', node2: 'Alex', type: 'WORKS_AT', source_type: 'Person', target_type: 'Company' }],
    custom_instructions: 'Only explicit employment.',
  }
}

for (const parent of ['editor', 'upload'] as const) {
  test(`${parent}: reopening and saving preserves typed example identities`, () => {
    const config = sample()
    const source = parent === 'editor' ? editor : upload
    const state = evaluate(source, property('nodeExtractConfig', 'kb.extract_config'), {
      kb: { extract_config: config, indexing_strategy: { graph_enabled: true } },
    })
    assert.deepEqual(plain(state.nodes), config.nodes)
    assert.deepEqual(plain(state.relations), config.relations)
    const saved = evaluate(source, property('extract_config', parent === 'editor' ? 'formData.value' : 'state.nodeExtractConfig'), {
      formData: { value: { nodeExtractConfig: state } },
      state: { nodeExtractConfig: state },
    })
    assert.deepEqual(plain(saved), config)
  })

  test(`${parent}: old untyped examples still default missing attributes`, () => {
    const state = evaluate(parent === 'editor' ? editor : upload, property('nodeExtractConfig', 'kb.extract_config'), {
      kb: { extract_config: { nodes: [{ name: 'Alex' }] } },
    })
    assert.deepEqual(plain(state.nodes), [{ name: 'Alex', attributes: [] }])
    assert.equal(state.schema.enabled, false)
  })
}

test('reparse overrides preserve typed examples through restore and resubmit', () => {
  const config = sample()
  const uiState = { value: { nodeExtractConfig: {}, graphEnabled: true } }
  evaluate(upload, node => ts.isFunctionDeclaration(node) && node.name?.text === 'applyOverridesToState', {
    uiState, input: { extract_config: config, graph_enabled: true },
  })
  const saved = evaluate(upload, property('extract_config', 'state.nodeExtractConfig'), { state: uiState.value })
  assert.deepEqual(plain(saved), config)
})

test('editor: enabling graph submits the completed schema before validating the saved draft', async () => {
  const config = sample()
  let savedConfig = { ...config, enabled: false, schema: { ...config.schema, entity_types: [] } }
  const calls: string[] = []
  const errors: string[] = []
  const data = {
    name: 'Schema draft', description: '', extract_config: config,
    chunking_config: { chunk_size: 512, chunk_overlap: 50, separators: [] },
  }
  const submit = evaluate(editor, node => ts.isVariableDeclaration(node) && node.name.getText() === 'doSubmit', {
    saving: { value: false },
    buildSubmitData: () => data,
    editorMode: { value: 'edit' },
    activeKbId: { value: 'kb' },
    formData: { value: {
      type: 'document', indexingStrategy: { vectorEnabled: false, keywordEnabled: false, graphEnabled: true },
      chunkingConfig: {},
    } },
    hasFiles: { value: false },
    updateKnowledgeBase: async (_id: string, request: any) => {
      calls.push('updateKnowledgeBase')
      const next = request.config.extract_config || savedConfig
      assert.equal(request.config.indexing_strategy.graph_enabled, true)
      assert.ok(next.schema.entity_types.length > 0, 'new schema must accompany the graph toggle')
      assert.deepEqual(plain(next), config, 'typed examples must accompany the schema')
      savedConfig = next
    },
    updateKBConfig: async (_id: string, request: any) => {
      calls.push('updateKBConfig')
      assert.deepEqual(plain(request.nodeExtract.schema), config.schema)
    },
    MessagePlugin: { success() {}, error(message: string) { errors.push(message) } },
    t: (key: string) => key,
    emit() {},
    handleClose() {},
    console: { error() {} },
  })
  await submit()
  assert.deepEqual(errors, [])
  assert.deepEqual(calls, ['updateKnowledgeBase', 'updateKBConfig'])
  assert.deepEqual(plain(savedConfig), config)
})
