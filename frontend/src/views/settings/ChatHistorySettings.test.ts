import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { fixture, flush } from '../../test-utils/vueFixture'

async function settings(t: TestContext, options: {
  canEdit?: boolean
  pendingSave?: boolean
  clear?: 'success' | 'failure' | 'pending'
  failStats?: boolean
} = {}) {
  let clears = 0
  let clearMode = options.clear || 'success'
  const writes: unknown[] = []
  const notifications: string[] = []
  let resolveSave!: (value: unknown) => void
  let resolveClear!: (value: unknown) => void
  let config = { enabled: true, embedding_model_id: 'embedding', knowledge_base_id: 'history' }
  let stats = { ...config, indexed_message_count: 3, has_indexed_messages: true }
  const empty = { ...stats, enabled: false, knowledge_base_id: '', indexed_message_count: 0, has_indexed_messages: false }
  const { vm, close } = fixture(t).component(new URL('./ChatHistorySettings.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: {
      success: (message: string) => notifications.push(message),
      error: (message: string) => notifications.push(message),
    } },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => options.canEdit !== false, effectiveTenantId: 1 }) },
    '@/api/chat-history': {
      getTenantChatHistoryConfig: async () => ({ data: config }),
      getChatHistoryKBStats: async () => {
        if (options.failStats) throw new Error('statistics unavailable')
        return { data: stats }
      },
      updateTenantChatHistoryConfig: async (body: any) => {
        writes.push(JSON.parse(JSON.stringify(body)))
        config = { ...config, ...body }
        if (options.pendingSave) return new Promise(resolve => { resolveSave = resolve })
        return { data: config }
      },
      clearChatHistoryIndex: async () => {
        clears++
        if (clearMode === 'pending') return new Promise(resolve => { resolveClear = resolve })
        if (clearMode === 'failure') {
          config = { ...config, enabled: false }
          stats = { ...stats, enabled: false, indexed_message_count: 2 }
          throw new Error('cleanup interrupted')
        }
        return { data: empty }
      },
    },
  })
  await flush()
  t.mock.timers.tick(100)
  await flush()
  return {
    vm, close, writes, notifications,
    clears: () => clears,
    succeed: () => { clearMode = 'success' },
    finishSave: () => resolveSave({ data: config }),
    finishClear: () => resolveClear({ data: empty }),
  }
}

test('clearing requires confirmation, pauses indexing and unlocks model selection', async (t) => {
  const f = await settings(t)
  f.vm.requestClearIndex()
  assert.equal(f.vm.clearDialogVisible, true)
  assert.equal(f.clears(), 0, 'opening the confirmation must not delete anything')
  await f.vm.clearIndex()
  assert.equal(f.clears(), 1)
  assert.equal(f.vm.localEnabled, false)
  assert.equal(f.vm.modelLocked, false)
  assert.equal(f.vm.stats.indexed_message_count, 0)
  assert.deepEqual(f.writes, [], 'clearing must not send a stale configuration update')
  assert.deepEqual(f.notifications, ['chatHistorySettings.toasts.clearSuccess'])
})

test('canceling confirmation never sends a destructive request', async (t) => {
  const f = await settings(t)
  f.vm.requestClearIndex()
  f.vm.closeClearDialog()
  await f.vm.clearIndex()
  assert.equal(f.clears(), 0)
  assert.equal(f.vm.modelLocked, true)
  assert.equal(f.vm.localEnabled, true)
})

test('a pending debounced save cannot re-enable indexing after clearing', async (t) => {
  const f = await settings(t)
  f.vm.localEnabled = false
  f.vm.handleEnabledChange()
  f.vm.requestClearIndex()
  await f.vm.clearIndex()
  t.mock.timers.tick(5000)
  await flush()
  assert.deepEqual(f.writes, [])
  assert.equal(f.vm.localEnabled, false)
})

test('an in-flight configuration save blocks the clear confirmation', async (t) => {
  const f = await settings(t, { pendingSave: true })
  f.vm.localEnabled = false
  f.vm.handleEnabledChange()
  t.mock.timers.tick(500)
  await flush()
  assert.equal(f.vm.isSaving, true)
  f.vm.requestClearIndex()
  assert.equal(f.vm.clearDialogVisible, false)
  f.finishSave()
  await flush()
  assert.equal(f.vm.isSaving, false)
  f.vm.requestClearIndex()
  assert.equal(f.vm.clearDialogVisible, true)
})

test('duplicate confirmations and cancellation are blocked during cleanup', async (t) => {
  const f = await settings(t, { clear: 'pending' })
  f.vm.requestClearIndex()
  const pending = f.vm.clearIndex()
  await flush()
  await f.vm.clearIndex()
  f.vm.closeClearDialog()
  assert.equal(f.clears(), 1)
  assert.equal(f.vm.clearDialogVisible, true)
  assert.equal(f.vm.isClearing, true)
  f.finishClear()
  await pending
  assert.equal(f.vm.clearDialogVisible, false)
  assert.equal(f.vm.isClearing, false)
})

test('partial failure reloads the paused state, keeps the model locked, and permits retry', async (t) => {
  const f = await settings(t, { clear: 'failure' })
  f.vm.requestClearIndex()
  await f.vm.clearIndex()
  assert.equal(f.vm.localEnabled, false)
  assert.equal(f.vm.stats.indexed_message_count, 2)
  assert.equal(f.vm.modelLocked, true)
  assert.equal(f.vm.clearDialogVisible, true)
  assert.deepEqual(f.notifications, ['chatHistorySettings.toasts.clearFailed'])
  f.succeed()
  await f.vm.clearIndex()
  assert.equal(f.clears(), 2)
  assert.equal(f.vm.modelLocked, false)
  assert.equal(f.vm.stats.indexed_message_count, 0)
})

test('non-admins cannot save settings or clear the workspace index', async (t) => {
  const f = await settings(t, { canEdit: false })
  f.vm.requestClearIndex()
  assert.equal(f.vm.clearDialogVisible, false)
  f.vm.clearDialogVisible = true
  await f.vm.clearIndex()
  f.vm.clearDialogVisible = false
  f.vm.localEnabled = false
  f.vm.handleEnabledChange()
  t.mock.timers.tick(5000)
  await flush()
  assert.equal(f.clears(), 0)
  assert.deepEqual(f.writes, [])
})

test('unavailable statistics never unlock the model or allow clearing an unknown index', async (t) => {
  const f = await settings(t, { failStats: true })
  assert.equal(f.vm.modelLocked, true)
  f.vm.handleModelChange('different')
  f.vm.requestClearIndex()
  assert.equal(f.vm.clearDialogVisible, false)
  assert.equal(f.vm.localEmbeddingModelId, 'embedding')
  assert.equal(f.clears(), 0)
})

test('leaving the page ignores a late clear response', async (t) => {
  const f = await settings(t, { clear: 'pending' })
  f.vm.requestClearIndex()
  const pending = f.vm.clearIndex()
  f.close()
  f.finishClear()
  await pending
  assert.deepEqual(f.notifications, [])
  assert.equal(f.vm.stats.indexed_message_count, 3)
})
