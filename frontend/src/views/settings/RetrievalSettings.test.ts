import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { runInNewContext } from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as vue from 'vue'

const source = readFileSync(new URL('./RetrievalSettings.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const script = compileScript(descriptor, { id: 'retrieval-settings-test' }).content
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

const baseConfig = {
  embedding_top_k: 50,
  vector_threshold: 0.15,
  keyword_threshold: 0.3,
  rerank_top_k: 10,
  rerank_threshold: 0.2,
  rerank_model_id: 'existing-model',
}

type RrfField = 'rrf_k' | 'rrf_vector_weight' | 'rrf_keyword_weight'
type SettingsState = {
  localConfig: Record<string, unknown>
  handleParamChange: () => void
  handleRrfChange: (field: RrfField, value: number | string) => void
}

async function editConfig(
  config: Record<string, unknown>,
  edit: (state: SettingsState) => void,
  canEdit = true,
) {
  let mounted: (() => Promise<void>) | undefined
  const timers = new Map<number, () => void>()
  let timerID = 0
  const setTimeout = (callback: () => void) => {
    timers.set(++timerID, callback)
    return timerID
  }
  const clearTimeout = (id: number) => timers.delete(id)
  const writes: Record<string, unknown>[] = []
  const mocks: Record<string, unknown> = {
    // 这组序列化测试直接执行 setup；卸载行为由真实挂载的自动保存测试覆盖。
    vue: { ...vue, onMounted: (callback: () => Promise<void>) => { mounted = callback }, onUnmounted() {} },
    'tdesign-vue-next': { MessagePlugin: { success() {}, error() {} } },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    '@/components/ModelSelector.vue': {},
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => canEdit }) },
    '@/api/retrieval': {
      getTenantRetrievalConfig: async () => ({ data: config }),
      updateTenantRetrievalConfig: async (body: Record<string, unknown>) => {
        // Exercise the JSON serialization used for the PUT request.
        writes.push(JSON.parse(JSON.stringify(body)))
        return { data: body }
      },
    },
  }
  const sandbox = {
    exports: {} as { default: { setup: (props: object, ctx: object) => SettingsState } },
    require: (name: string) => {
      assert.ok(name in mocks, `Unexpected component import: ${name}`)
      return mocks[name]
    },
    console, setTimeout, clearTimeout, window: { setTimeout, clearTimeout },
  }
  runInNewContext(compiled, sandbox)
  const state = sandbox.exports.default.setup({}, { expose() {} })
  assert.ok(mounted)
  await mounted()
  assert.equal(writes.length, 0, 'loading the config must not save it')
  for (const [id, callback] of timers) {
    timers.delete(id)
    callback()
  }
  edit(state)
  for (const [id, callback] of timers) {
    timers.delete(id)
    callback()
  }
  await Promise.resolve()
  return writes
}

for (const [name, rrf] of [
  ['custom RRF parameters', { rrf_k: 90, rrf_vector_weight: 0.2, rrf_keyword_weight: 0.8 }],
  ['omitted RRF parameters', {}],
  ['explicit zero RRF parameters', { rrf_k: 0, rrf_vector_weight: 0, rrf_keyword_weight: 0 }],
] as const) {
  test(`editing Top K preserves ${name} in the serialized PUT body`, async () => {
    const config = { ...baseConfig, ...rrf }
    const writes = await editConfig(config, state => {
      state.localConfig.embedding_top_k = 25
      state.handleParamChange()
    })
    assert.equal(writes.length, 1)
    assert.deepEqual(writes[0], { ...config, embedding_top_k: 25 })
  })
}

for (const [field, value] of [
  ['rrf_k', 90],
  ['rrf_vector_weight', 1.25],
  ['rrf_keyword_weight', 0.45],
] as const) {
  test(`editing ${field} saves the new value and preserves the other settings`, async () => {
    const config = { ...baseConfig, rrf_k: 75, rrf_vector_weight: 0.85, rrf_keyword_weight: 0.15 }
    const writes = await editConfig(config, state => state.handleRrfChange(field, value))
    assert.deepEqual(writes, [{ ...config, [field]: value }])
  })
}

test('RRF edits coalesce into one save and zero selects the server default', async () => {
  const writes = await editConfig(baseConfig, state => {
    state.handleRrfChange('rrf_k', 80)
    state.handleRrfChange('rrf_k', 0)
    state.handleRrfChange('rrf_vector_weight', 0)
    state.handleRrfChange('rrf_keyword_weight', 0)
  })
  assert.deepEqual(writes, [{ ...baseConfig, rrf_k: 0, rrf_vector_weight: 0, rrf_keyword_weight: 0 }])
})

test('cleared, invalid and fractional K inputs do not overwrite saved values', async () => {
  const writes = await editConfig({ ...baseConfig, rrf_k: 75, rrf_vector_weight: 0.8 }, state => {
    for (const value of ['', -1, NaN, Infinity]) state.handleRrfChange('rrf_vector_weight', value)
    state.handleRrfChange('rrf_k', 1.5)
  })
  assert.deepEqual(writes, [])
})

test('non-admins cannot change RRF configuration', async () => {
  const writes = await editConfig(baseConfig, state => state.handleRrfChange('rrf_k', 90), false)
  assert.deepEqual(writes, [])
})
