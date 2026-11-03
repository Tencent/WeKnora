import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { effectScope, reactive, ref, watch } from 'vue'

// Exercise the production context watcher with Vue's actual synchronous
// scheduler, without mounting the unrelated model/attachment menus.
const source = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')
const start = source.indexOf('// A transcript belongs to the composer context')
const end = source.indexOf('const isAgentEnabled =', start)
assert.ok(start >= 0 && end > start, 'voice context watcher exists')
const watcher = source.slice(start, end)

for (const field of ['session', 'agent', 'sourceTenant', 'user', 'workspace', 'tenant', 'availability']) {
  test(`changing ${field} cancels voice input before another completion can edit the draft`, () => {
    const props = reactive({ sessionId: 'session-a' })
    const selectedAgentId = ref('agent-a')
    const settingsStore = reactive({ selectedAgentSourceTenantId: 'owner-a' })
    const mentionAuth = reactive({ user: { id: 'user-a' }, selectedTenantId: 'workspace-a', tenant: { id: 'tenant-a' } })
    const showVoiceButton = ref(true)
    const events: string[] = []
    const scope = effectScope()
    const context = {
      props, selectedAgentId, settingsStore, mentionAuth, showVoiceButton, watch,
      voiceInput: { cancel: () => events.push('cancel') },
      voiceHold: { release: () => events.push('release') },
      voiceInsertRange: { start: 2, end: 4 },
    }
    scope.run(() => runInNewContext(watcher, context))
    try {
      assert.deepEqual(events, [])
      switch (field) {
        case 'session': props.sessionId = 'session-b'; break
        case 'agent': selectedAgentId.value = 'agent-b'; break
        case 'sourceTenant': settingsStore.selectedAgentSourceTenantId = 'owner-b'; break
        case 'user': mentionAuth.user.id = 'user-b'; break
        case 'workspace': mentionAuth.selectedTenantId = 'workspace-b'; break
        case 'tenant': mentionAuth.tenant.id = 'tenant-b'; break
        case 'availability': showVoiceButton.value = false; break
      }
      assert.deepEqual(events, ['cancel', 'release'], 'cancellation is synchronous and precedes key release')
      assert.equal(context.voiceInsertRange, null, 'old caret range cannot be reused')
    } finally { scope.stop() }
  })
}
