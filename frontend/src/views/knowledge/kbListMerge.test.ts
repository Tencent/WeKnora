import assert from 'node:assert/strict'
import test from 'node:test'

import {
  mergeAllScopeKnowledgeBases,
  isSharedKbEditable,
  type OwnedKnowledgeBase,
  type SharedKnowledgeBaseLike,
  type MergedKnowledgeBase,
} from './kbListMerge.ts'

const ME = 'user-me'

function owned(id: string, extra: Partial<OwnedKnowledgeBase> = {}): OwnedKnowledgeBase {
  return { id, creator_id: ME, ...extra }
}

function shared(
  kbId: string,
  permission: string,
  extra: Partial<SharedKnowledgeBaseLike> = {},
): SharedKnowledgeBaseLike {
  return {
    knowledge_base: { id: kbId },
    permission,
    shared_at: '2026-01-01T00:00:00Z',
    share_id: `share-${kbId}-${permission}`,
    org_name: 'Org',
    ...extra,
  }
}

// Render keys must be unique — duplicate `:key` values are exactly what
// blanks the list in #795.
function keys(list: MergedKnowledgeBase[]): string[] {
  return list.map((kb) => kb.id)
}

function hasUniqueKeys(list: MergedKnowledgeBase[]): boolean {
  return new Set(keys(list)).size === list.length
}

test('renders an empty list when there are no knowledge bases', () => {
  assert.deepEqual(mergeAllScopeKnowledgeBases([], [], ME), [])
})

test('keeps a single owned KB (the case that always worked)', () => {
  const result = mergeAllScopeKnowledgeBases([owned('a')], [], ME)
  assert.deepEqual(keys(result), ['a'])
  assert.equal(result[0].isMine, true)
})

test('renders every owned KB once when there are two or more (#795 regression)', () => {
  const result = mergeAllScopeKnowledgeBases([owned('a'), owned('b'), owned('c')], [], ME)
  assert.deepEqual(keys(result), ['a', 'b', 'c'])
  assert.ok(hasUniqueKeys(result))
})

test('does not emit a KB twice when it is both owned and shared back', () => {
  // Without de-dup this yields two entries keyed "a" -> duplicate Vue key
  // -> blank list. Owned must win.
  const result = mergeAllScopeKnowledgeBases([owned('a'), owned('b')], [shared('a', 'viewer')], ME)
  assert.deepEqual(keys(result), ['a', 'b'])
  assert.ok(hasUniqueKeys(result))
  const a = result.find((kb) => kb.id === 'a')!
  assert.equal(a.isMine, true)
})

test('renders exactly one card per KB when several owned KBs are also shared back', () => {
  // Pre-fix this returned 4 rows (2 owned + 2 shared dups) with colliding
  // keys, which is what blanked the page once there were ≥2 KBs.
  const result = mergeAllScopeKnowledgeBases(
    [owned('a'), owned('b')],
    [shared('a', 'viewer'), shared('b', 'editor')],
    ME,
  )
  assert.equal(result.length, 2)
  assert.deepEqual([...keys(result)].sort(), ['a', 'b'])
  assert.ok(hasUniqueKeys(result))
})

test('collapses the same KB shared through multiple orgs into one card', () => {
  // Two distinct shares (different share_id) but the same knowledge_base.id
  // — the real-world trigger for duplicate keys once there are ≥2 rows.
  const result = mergeAllScopeKnowledgeBases(
    [],
    [
      shared('x', 'viewer', { org_name: 'Org A', share_id: 'share-x-A' }),
      shared('x', 'editor', { org_name: 'Org B', share_id: 'share-x-B' }),
    ],
    ME,
  )
  assert.deepEqual(keys(result), ['x'])
  assert.ok(hasUniqueKeys(result))
})

test('keeps the most-privileged permission when collapsing duplicate shares', () => {
  const result = mergeAllScopeKnowledgeBases(
    [],
    [
      shared('x', 'viewer', { share_id: 's1' }),
      shared('x', 'admin', { share_id: 's2' }),
      shared('x', 'editor', { share_id: 's3' }),
    ],
    ME,
  )
  assert.equal(result.length, 1)
  assert.equal((result[0] as { permission: string }).permission, 'admin')
})

test('guarantees unique keys across a mixed owned + shared set', () => {
  const result = mergeAllScopeKnowledgeBases(
    [owned('a'), owned('b', { creator_id: 'someone-else' })],
    [
      shared('a', 'editor'), // overlaps owned -> dropped
      shared('c', 'viewer'),
      shared('c', 'admin', { share_id: 'share-c-2' }), // duplicate share -> collapsed
      shared('d', 'editor'),
    ],
    ME,
  )
  assert.ok(hasUniqueKeys(result))
  assert.deepEqual(new Set(keys(result)), new Set(['a', 'b', 'c', 'd']))
})

test('orders pinned (newest first) → own → teammate → shared(editable first)', () => {
  const result = mergeAllScopeKnowledgeBases(
    [
      owned('mine'),
      owned('teammate', { creator_id: 'someone-else' }),
      owned('pin-old', { is_pinned: true, pinned_at: '2026-01-01T00:00:00Z' }),
      owned('pin-new', { is_pinned: true, pinned_at: '2026-02-01T00:00:00Z' }),
    ],
    [shared('view-only', 'viewer'), shared('editable', 'editor')],
    ME,
  )
  assert.deepEqual(keys(result), [
    'pin-new',
    'pin-old',
    'mine',
    'teammate',
    'editable',
    'view-only',
  ])
})

test('drops shared rows whose knowledge_base is null', () => {
  const result = mergeAllScopeKnowledgeBases(
    [owned('a')],
    [{ knowledge_base: null, permission: 'viewer', shared_at: '', share_id: 's' }],
    ME,
  )
  assert.deepEqual(keys(result), ['a'])
})

test('isSharedKbEditable treats admin/editor as editable, others read-only', () => {
  assert.equal(isSharedKbEditable('admin'), true)
  assert.equal(isSharedKbEditable('editor'), true)
  assert.equal(isSharedKbEditable('viewer'), false)
  assert.equal(isSharedKbEditable(undefined), false)
})
