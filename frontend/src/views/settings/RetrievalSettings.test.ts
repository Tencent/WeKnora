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

async function editTopK(config: Record<string, unknown>) {
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
    vue: { ...vue, onMounted: (callback: () => Promise<void>) => { mounted = callback } },
    'tdesign-vue-next': { MessagePlugin: { success() {}, error() {} } },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    '@/components/ModelSelector.vue': {},
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
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
    exports: {} as { default: { setup: (props: object, ctx: object) => {
      localConfig: Record<string, unknown>
      handleParamChange: () => void
    } } },
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
  state.localConfig.embedding_top_k = 25
  state.handleParamChange()
  for (const [id, callback] of timers) {
    timers.delete(id)
    callback()
  }
  await Promise.resolve()
  assert.equal(writes.length, 1)
  return writes[0]
}

for (const [name, rrf] of [
  ['custom RRF parameters', { rrf_k: 90, rrf_vector_weight: 0.2, rrf_keyword_weight: 0.8 }],
  ['omitted RRF parameters', {}],
  ['explicit zero RRF parameters', { rrf_k: 0, rrf_vector_weight: 0, rrf_keyword_weight: 0 }],
] as const) {
  test(`editing Top K preserves ${name} in the serialized PUT body`, async () => {
    const config = { ...baseConfig, ...rrf }
    const body = await editTopK(config)
    assert.deepEqual(body, { ...config, embedding_top_k: 25 })
  })
}
