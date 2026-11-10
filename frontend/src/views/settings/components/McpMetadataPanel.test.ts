import assert from 'node:assert/strict'
import test from 'node:test'
import { change, fixture, flush } from '../../../test-utils/vueFixture.ts'

const panel = new URL('./McpMetadataPanel.vue', import.meta.url)
const snapshot = (name: string) => ({ service_id: 'svc', stale: false, tools: [{ name }] })

test('assistant sync reloads the open directory once without reconnecting upstream', async t => {
  const f = fixture(t)
  let saved: ReturnType<typeof snapshot> | null = snapshot('old')
  const getMCPMetadata = t.mock.fn(async (_id: string) => saved)
  const refreshMCPMetadata = t.mock.fn(async () => assert.fail('change events must not synchronize'))
  const { vm, close } = f.component(panel, {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    '@/api/mcp-service': { getMCPMetadata, refreshMCPMetadata },
  }, { serviceId: 'svc' })
  await flush()
  assert.equal(vm.snapshot.tools[0].name, 'old')
  saved = snapshot('new')
  f.emit(change('mcp_service.refresh_metadata'))
  f.emit(change('mcp_service.refresh_metadata'))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(getMCPMetadata.mock.callCount(), 2)
  assert.equal(vm.snapshot.tools[0].name, 'new')
  assert.equal(refreshMCPMetadata.mock.callCount(), 0)

  // An unsynchronized/deleted directory stays empty; receiving an event must
  // not reuse the mount-time auto-sync behavior and bypass assistant policy.
  saved = null
  f.emit(change('mcp_service.update'))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(vm.snapshot, null)
  assert.equal(refreshMCPMetadata.mock.callCount(), 0)
  close()
  f.emit(change('mcp_service.refresh_metadata'))
  t.mock.timers.tick(300)
  assert.equal(getMCPMetadata.mock.callCount(), 3)
})

test('a late directory read cannot overwrite a newer sync or update an unmounted panel', async t => {
  const f = fixture(t)
  let complete!: (value: ReturnType<typeof snapshot>) => void
  let deferRead = false
  const getMCPMetadata = async () => deferRead
    ? new Promise<ReturnType<typeof snapshot>>(resolve => { complete = resolve })
    : snapshot('initial')
  const { vm, close } = f.component(panel, {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    '@/api/mcp-service': { getMCPMetadata, refreshMCPMetadata: async () => snapshot('fresh') },
  }, { serviceId: 'svc' })
  await flush()
  deferRead = true
  f.emit(change('mcp_service.refresh_metadata'))
  t.mock.timers.tick(300)
  vm.refresh()
  await flush()
  assert.equal(vm.snapshot.tools[0].name, 'fresh')
  complete(snapshot('stale'))
  await flush()
  assert.equal(vm.snapshot.tools[0].name, 'fresh')

  f.emit(change('mcp_service.refresh_metadata'))
  t.mock.timers.tick(300)
  close()
  complete(snapshot('late'))
  await flush()
  assert.equal(vm.snapshot.tools[0].name, 'fresh')
})

test('a slow initial read cannot replace the directory loaded after an assistant sync', async t => {
  const f = fixture(t)
  let initial!: (value: ReturnType<typeof snapshot>) => void
  let reads = 0
  const { vm } = f.component(panel, {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    '@/api/mcp-service': {
      getMCPMetadata: async () => ++reads === 1
        ? new Promise<ReturnType<typeof snapshot>>(resolve => { initial = resolve })
        : snapshot('synced'),
      refreshMCPMetadata: async () => assert.fail('must not synchronize'),
    },
  }, { serviceId: 'svc' })
  f.emit(change('mcp_service.refresh_metadata'))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(vm.snapshot.tools[0].name, 'synced')
  initial(snapshot('old'))
  await flush()
  assert.equal(vm.snapshot.tools[0].name, 'synced')
  assert.equal(vm.loading, false)
})
