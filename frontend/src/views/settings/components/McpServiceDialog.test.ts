import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./McpServiceDialog.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'mcp-dialog-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText

function fixture(generate: () => Promise<string> = async () => 'Query logs by module and time.', initialStep: 0 | 1 = 0) {
  const updates: Array<{ id: string; data: Record<string, unknown> }> = []
  const warnings: string[] = []
  const props = reactive({ visible: true, service: { id: 'one', name: 'Logs', description: 'Legacy usage', transport_type: 'sse', url: 'https://example.com/mcp' }, mode: 'edit', initialStep })
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key, locale: ref('zh-CN') }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { warning: (text: string) => warnings.push(text), success() {}, error() {} } }
      if (name === '@/api/mcp-service') return {
        generateMCPUsageInstructions: generate,
        updateMCPService: async (id: string, data: Record<string, unknown>) => {
          updates.push({ id, data })
          return { ...props.service, ...data }
        },
      }
      return { default: {} }
    },
    URL, console,
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const instance = ref<any>()
  const app = renderer.createApp({ render: () => h(component, { ...props, ref: instance }) })
  app.mount({})
  return { vm: instance.value, props, updates, warnings, close: () => app.unmount() }
}

test('tool shortcut opens the saved service directly without resaving its connection', () => {
  const f = fixture(undefined, 1)
  try {
    assert.equal(f.vm.step, 1)
    assert.equal(f.vm.currentService.id, 'one')
    assert.equal(f.updates.length, 0)
  } finally { f.close() }
})

test('usage instructions are required for final save and legacy description is editable there', async () => {
  const f = fixture()
  try {
    assert.equal(f.vm.formData.usage_instructions, 'Legacy usage')
    assert.equal('description' in f.vm.formData, false)
    f.vm.toolsSynced = true
    f.vm.formData.usage_instructions = '  \n  '
    await f.vm.handleSubmit()
    assert.equal(f.updates.length, 0)
    assert.deepEqual(f.warnings, ['mcpMetadata.instructionsRequired'])
    f.vm.formData.usage_instructions = '  Query logs  '
    await f.vm.handleSubmit()
    assert.equal(f.updates.length, 1)
    assert.deepEqual({ ...f.updates[0]?.data }, { usage_instructions: 'Query logs' })
  } finally { f.close() }
})

test('connection save omits usage instructions so new services can sync tools first', () => {
  const f = fixture()
  try {
    f.vm.formData.usage_instructions = ''
    const body = f.vm.buildPayload(true)
    assert.equal('usage_instructions' in body, false)
    assert.equal('description' in body, false)
    assert.equal(body.url, 'https://example.com/mcp')
  } finally { f.close() }
})

test('AI generation needs synced tools and fills the editor without saving', async () => {
  let calls = 0
  const f = fixture(async () => { calls++; return 'Generated usage' })
  try {
    await f.vm.handleGenerateUsage()
    assert.equal(calls, 0)
    f.vm.toolsSynced = true
    await f.vm.handleGenerateUsage()
    assert.equal(calls, 1)
    assert.equal(f.vm.formData.usage_instructions, 'Generated usage')
    assert.equal(f.updates.length, 0)
    assert.equal(f.vm.generatingUsage, false)
  } finally { f.close() }
})

test('generation failure preserves existing instructions', async () => {
  const f = fixture(async () => { throw new Error('unavailable') })
  try {
    f.vm.toolsSynced = true
    await f.vm.handleGenerateUsage()
    assert.equal(f.vm.formData.usage_instructions, 'Legacy usage')
    assert.equal(f.vm.generatingUsage, false)
    assert.equal(f.updates.length, 0)
  } finally { f.close() }
})

test('closing or switching service prevents a late AI result from overwriting the editor', async () => {
  for (const action of ['close', 'switch']) {
    let resolve!: (value: string) => void
    const pending = new Promise<string>(done => { resolve = done })
    const f = fixture(() => pending)
    try {
      f.vm.toolsSynced = true
      const generation = f.vm.handleGenerateUsage()
      assert.equal(f.vm.generatingUsage, true)
      if (action === 'close') f.props.visible = false
      else f.props.service = { ...f.props.service, id: 'two', description: 'Other service' }
      await nextTick()
      resolve('Stale generated usage')
      await generation
      assert.equal(f.vm.formData.usage_instructions, action === 'close' ? 'Legacy usage' : 'Other service')
      assert.equal(f.vm.generatingUsage, false)
    } finally { f.close() }
  }
})
