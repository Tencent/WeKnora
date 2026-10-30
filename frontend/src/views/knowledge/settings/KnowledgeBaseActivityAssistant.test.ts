import assert from 'node:assert/strict'
import test from 'node:test'
import { fixture } from '../../../test-utils/vueFixture.ts'

test('activity summaries preserve the exact loaded IDs and server-side filters', t => {
  const { vm } = fixture(t).component(new URL('./KnowledgeBaseActivitySettings.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (key: string, params: unknown) => JSON.stringify({ key, params }), te: () => false, tm: () => ({}) }) },
    '@/stores/auth': { useAuthStore: () => ({}) },
    '@/i18n/auditActionRegistry': { AUDIT_ACTION_I18N_ROOTS: { kbActivity: 'activity' } },
    '@/i18n/auditActionLabel': { auditActionLabel: () => '' },
    '@/api/knowledge-base': {},
  }, { kbId: 'kb-1', active: false })
  vm.action = 'knowledge.update'
  vm.outcome = 'success'
  vm.entries = [{ id: 20, created_at: '2026-10-26T12:00:00Z' }, { id: 12, created_at: '2026-10-25T12:00:00Z' }]
  vm.hasMore = true
  const scope = JSON.parse(JSON.parse(vm.activityAssistantQuery).params.scope)
  assert.deepEqual(scope.loaded_entry_ids, [20, 12])
  assert.equal(scope.knowledge_base_id, 'kb-1')
  assert.equal(scope.action, 'knowledge.update')
  assert.equal(scope.outcome, 'success')
  assert.equal(scope.has_more, true)
  vm.entries = [{ id: 7, created_at: '2026-10-24T12:00:00Z' }]
  vm.action = undefined
  vm.outcome = 'failed'
  const changed = JSON.parse(JSON.parse(vm.activityAssistantQuery).params.scope)
  assert.deepEqual(changed.loaded_entry_ids, [7])
  assert.equal(changed.action, undefined)
  assert.equal(changed.outcome, 'failed')
  assert.deepEqual(scope.loaded_entry_ids, [20, 12])
})
