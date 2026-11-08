import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { fixture, flush } from '../../../test-utils/vueFixture'

const stores = [
  { id: '__env_postgres__', name: 'PostgreSQL', engine_type: 'postgres', source: 'env' },
  { id: 'oracle-1', name: 'Oracle ADB', engine_type: 'oracle', source: 'user' },
]

function setup(t: TestContext, props: Record<string, unknown>, defaultStore = 'oracle-1') {
  const f = fixture(t)
  const emitted: string[] = []
  const { vm } = f.component(new URL('./KBVectorStoreSettings.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    '@/stores/ui': { useUIStore: () => ({ openSettings() {} }) },
    '@/api/vector-store': { listVectorStores: async () => ({ success: true, data: stores }) },
    '@/api/default-settings': {
      getTenantDefaultSettings: async () => ({ data: { vector_store_id: defaultStore } }),
    },
    '@/components/VectorStoreBadge.vue': { name: 'VectorStoreBadge', render: () => null },
  }, { ...props, 'onUpdate:vectorStoreId': (id: string) => emitted.push(id) })
  return { vm, emitted }
}

test('create mode pre-selects the workspace default store', async t => {
  const { vm, emitted } = setup(t, { mode: 'create', vectorStoreId: '' })
  await flush()
  assert.equal(vm.localVectorStoreId, 'oracle-1')
  assert.deepEqual(emitted, ['oracle-1'])
})

test('a default store that is no longer listed leaves the system default', async t => {
  const { vm, emitted } = setup(t, { mode: 'create', vectorStoreId: '' }, 'deleted')
  await flush()
  assert.equal(vm.localVectorStoreId, '')
  assert.deepEqual(emitted, [])
})

test('edit mode names the engine per retrieval type and marks disabled ones', async t => {
  const { vm } = setup(t, {
    mode: 'edit',
    keywordEnabled: false,
    boundRetrievers: [
      { type: 'vector', engine_type: 'oracle', label: 'AI Vector Search' },
      { type: 'keywords', engine_type: 'oracle', label: 'Oracle Text' },
    ],
  })
  await flush()
  assert.deepEqual(vm.retrievalRows.map((r: any) => [r.title, r.label, r.enabled]), [
    ['kbSettings.vectorStore.vectorRetrieval', 'AI Vector Search', true],
    ['kbSettings.vectorStore.keywordRetrieval', 'Oracle Text', false],
  ])
})
