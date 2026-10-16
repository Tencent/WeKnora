import assert from 'node:assert/strict'
import test from 'node:test'
import { reactive, ref } from 'vue'
import { change, fixture, flush } from '../../test-utils/vueFixture.ts'

test('assistant profile changes refresh the workspace page without discarding an unfinished edit', async t => {
  const f = fixture(t)
  let saved = { id: 7, name: 'Original', description: 'Original description' }
  const read = t.mock.fn(async () => ({ success: true, data: { tenant: { ...saved } } }))
  const auth = reactive({ currentTenantId: 7, currentTenantRole: 'owner', hasRole: () => true })
  const { vm, close } = f.component(new URL('./TenantInfo.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key, locale: ref('en-US') }) },
    'tdesign-vue-next': { MessagePlugin: { error: assert.fail }, DialogPlugin: {} },
    '@/api/auth': { getCurrentUser: read },
    '@/api/tenant': {},
    '@/api/tenant/members': { fetchAllTenantMembers: async () => [{ user_id: 'owner', role: 'owner' }] },
    '@/stores/auth': { useAuthStore: () => auth },
    '@/composables/useRoleLabel': { useRoleLabel: () => ({ formatRole: String }), useHomeTenant: () => ({ homeTenantId: ref(7) }) },
    '@/utils/tenantSwitch': {},
  })
  await flush()
  assert.equal(vm.tenantInfo.name, 'Original')
  vm.editing = true
  vm.editName = 'Unfinished input'
  saved = { id: 7, name: 'Updated by assistant', description: '' }
  f.emit(change('workspace.update'))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(vm.tenantInfo.name, 'Updated by assistant')
  assert.equal(vm.tenantInfo.description, '')
  assert.equal(vm.editName, 'Unfinished input')
  assert.equal(vm.editing, true)
  assert.equal(read.mock.callCount(), 2)
  close()
  f.emit(change('workspace.update'))
  t.mock.timers.tick(300)
  assert.equal(read.mock.callCount(), 2)
})
