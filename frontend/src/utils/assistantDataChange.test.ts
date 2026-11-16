import assert from 'node:assert/strict'
import test from 'node:test'
import { change, fixture } from '../test-utils/vueFixture.ts'

test('normalizes snake-case IDs, drops invalid IDs, and deduplicates', t => {
  const f = fixture(t)
  const listener = t.mock.fn()
  f.window.addEventListener(f.dataChange.ASSISTANT_DATA_CHANGE_EVENT, listener)
  f.emit({ operation: ' knowledge.move ', area: 'knowledge', ids: {
    knowledge_base_id: [' a ', 'a', '', 4], knowledge_id: ['doc'], agent_id: null,
  } })
  assert.deepEqual(structuredClone(listener.mock.calls[0].arguments[0].detail), {
    operation: 'knowledge.move', area: 'knowledge',
    ids: { knowledgeBaseIds: ['a'], knowledgeIds: ['doc'], agentIds: [] },
  })
})

for (const raw of [null, [], {}, { area: 'agent' }, { operation: 'agent.', area: 'agent' },
  { operation: 'wiki.revert', area: 'agent' }, { operation: 2, area: 'agent' },
  { ...change('agent.create'), ids: [] }]) {
  test(`ignores malformed payload ${JSON.stringify(raw)}`, t => {
    const f = fixture(t)
    const handler = t.mock.fn()
    f.mount({ setup() { f.dataChange.useAssistantDataChange(handler) } })
    f.emit(raw)
    t.mock.timers.tick(300)
    assert.equal(handler.mock.callCount(), 0)
  })
}

test('filters areas and coalesces bursts without losing operations or IDs', t => {
  const f = fixture(t)
  const handler = t.mock.fn()
  f.mount({ setup() { f.dataChange.useAssistantDataChange(handler, { areas: ['agent'] }) } })
  f.emit(change('agent.create', { agent_id: ['a'] }))
  t.mock.timers.tick(200)
  f.emit(change('wiki.revert'))
  f.emit(change('agent.update', { agent_id: ['b'] }))
  t.mock.timers.tick(299)
  assert.equal(handler.mock.callCount(), 0)
  t.mock.timers.tick(1)
  assert.equal(handler.mock.callCount(), 1)
  assert.deepEqual(structuredClone(handler.mock.calls[0].arguments[0].map((item: any) => item.ids.agentIds)), [['a'], ['b']])
})

test('cancels queued work and removes the listener on unmount', t => {
  const f = fixture(t)
  const handler = t.mock.fn()
  const { close } = f.mount({ setup() { f.dataChange.useAssistantDataChange(handler, { debounceMs: 10 }) } })
  f.emit(change('agent.delete'))
  close()
  f.emit(change('agent.create'))
  t.mock.timers.tick(300)
  assert.equal(handler.mock.callCount(), 0)
})

test('guards malformed window events and accepts omitted IDs', t => {
  const f = fixture(t)
  const handler = t.mock.fn()
  f.mount({ setup() { f.dataChange.useAssistantDataChange(handler) } })
  f.window.dispatchEvent(new CustomEvent(f.dataChange.ASSISTANT_DATA_CHANGE_EVENT, { detail: { ids: {} } }))
  f.emit({ operation: 'agent.create', area: 'agent' })
  t.mock.timers.tick(300)
  assert.equal(handler.mock.callCount(), 1)
  assert.deepEqual(structuredClone(handler.mock.calls[0].arguments[0][0].ids), {
    knowledgeBaseIds: [], knowledgeIds: [], agentIds: [],
  })
})
