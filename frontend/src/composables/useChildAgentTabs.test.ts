import assert from 'node:assert/strict'
import { test } from 'node:test'
import { ref } from 'vue'
import { createChildAgentTabs } from './useChildAgentTabs'
import type { CollaborationTaskView } from '../utils/collaborationDisplay'
const task = (id: string, status = 'running'): CollaborationTaskView => ({ id, sessionId: `session-${id}`, runId: 'r1', name: id, displayName: id, status, latestMessage: '' })

test('tabs deduplicate child entries, retain live status and return to main when the selected tab closes', () => {
  const state = createChildAgentTabs()
  const a = ref(task('a')), b = ref(task('b'))
  state.open(a.value, () => a.value)
  const aId = state.activeId.value
  state.open(b.value, () => b.value)
  const bId = state.activeId.value
  state.open(a.value, () => a.value)
  assert.equal(state.tabs.value.length, 2)
  assert.equal(state.activeId.value, aId)
  a.value = { ...a.value, status: 'waiting' }
  assert.equal(state.tabs.value[0].task.status, 'waiting')
  a.value = { ...a.value, runId: 'r2', status: 'running' }
  assert.equal(state.tabs.value[0].task.runId, 'r2')
  state.select('')
  assert.equal(state.tabs.value.length, 2)
  state.select(aId)
  state.close(bId)
  assert.equal(state.activeId.value, aId)
  state.close(aId)
  assert.equal(state.activeId.value, '')
  assert.deepEqual(state.tabs.value, [])
})

test('tab state is scoped to a chat and reset releases previous session entries', () => {
  const first = createChildAgentTabs(), second = createChildAgentTabs()
  first.open(task('a'), () => undefined)
  assert.equal(first.tabs.value[0].task.displayName, 'a')
  assert.equal(second.tabs.value.length, 0)
  first.select('missing')
  assert.notEqual(first.activeId.value, 'missing')
  first.reset()
  assert.equal(first.activeId.value, '')
  assert.equal(first.tabs.value.length, 0)
})
