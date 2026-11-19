import assert from 'node:assert/strict'
import test from 'node:test'
import {
  EMBED_HISTORY_LIMIT, embedHistoryStorageKey, readEmbedHistory, rememberEmbedSession, saveEmbedHistory,
} from './embedSessionHistory'

const scope = { channelId: 'channel', agentId: 'agent', visitorId: 'visitor' }
const entry = (id: string, updatedAt = 1) => ({ id, sig: `signature-${id}`, title: `Chat ${id}`, updatedAt })
function memoryStorage() {
  const values = new Map<string, string>()
  return { values, getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) }, removeItem: (key: string) => { values.delete(key) } }
}

test('history survives reload and isolates channels, agents, and visitors', () => {
  const storage = memoryStorage()
  saveEmbedHistory(scope, [entry('one')], storage)
  assert.deepEqual(readEmbedHistory(scope, storage), [entry('one')])
  for (const other of [{ ...scope, channelId: 'other' }, { ...scope, agentId: 'other' }, { ...scope, visitorId: 'other' }]) {
    assert.deepEqual(readEmbedHistory(other, storage), [])
    saveEmbedHistory(other, [entry('other')], storage)
    saveEmbedHistory(scope, [], storage)
    assert.deepEqual(readEmbedHistory(other, storage), [entry('other')])
  }
})

test('recent chats are bounded, deduplicated, renamed, and merged with another tab', () => {
  const storage = memoryStorage()
  const chats = Array.from({ length: EMBED_HISTORY_LIMIT + 5 }, (_, i) => entry(String(i), i))
  const saved = saveEmbedHistory(scope, chats, storage)
  assert.equal(saved.length, EMBED_HISTORY_LIMIT)
  assert.equal(saved[0]?.id, '34')
  assert.equal(saved.at(-1)?.id, '5')
  const updated = rememberEmbedSession(scope, { ...entry('10', 100), title: 'Renamed' }, [entry('in-memory', 99)], storage)
  assert.equal(updated.length, EMBED_HISTORY_LIMIT)
  assert.equal(updated.filter(item => item.id === '10').length, 1)
  assert.equal(updated[0]?.title, 'Renamed')
  assert.equal(updated[1]?.id, 'in-memory')
})

test('malformed storage and invalid entries do not break history or date rendering', () => {
  const storage = memoryStorage()
  for (const raw of ['invalid JSON', '{}', 'null', '42']) {
    storage.setItem(embedHistoryStorageKey(scope), raw)
    assert.deepEqual(readEmbedHistory(scope, storage), [])
  }
  storage.setItem(embedHistoryStorageKey(scope), JSON.stringify([
    null, {}, entry('', 1), entry('huge-date', 1e30), { ...entry('bad-signature'), sig: 42 },
    { ...entry('valid', 2), title: 'a'.repeat(1000), api_token: 'must-not-persist' }, entry('valid', 1),
  ]))
  const saved = saveEmbedHistory(scope, readEmbedHistory(scope, storage), storage)
  assert.equal(saved.length, 1)
  assert.equal(saved[0]?.title.length, 160)
  assert.doesNotMatch(storage.getItem(embedHistoryStorageKey(scope))!, /api_token|must-not-persist/)
})

test('blocked or full storage retains an in-memory history without interrupting chat', () => {
  const storage = {
    getItem() { throw new Error('blocked') }, setItem() { throw new Error('quota') }, removeItem() { throw new Error('blocked') },
  }
  assert.deepEqual(readEmbedHistory(scope, storage), [])
  const first = rememberEmbedSession(scope, entry('one'), [], storage)
  const next = rememberEmbedSession(scope, entry('two', 2), first, storage)
  assert.deepEqual(next.map(item => item.id), ['two', 'one'])
  assert.deepEqual(saveEmbedHistory(scope, [], storage), [])
})
