import assert from 'node:assert/strict'
import test from 'node:test'
import { effectScope, nextTick, ref } from 'vue'
import { useChunkFeedbackGovernance } from './useChunkFeedbackGovernance'

const page = (data: any[]) => ({ data: { total: data.length, page: 1, page_size: 20, data } })

test('governance list/detail/history/reset form one scoped loop', async () => {
  const kbId = ref('kb-a')
  const calls: string[] = []
  const item = {
    chunk_id: 'chunk-a',
    knowledge_id: 'knowledge-a',
    knowledge_title: 'Document A',
    chunk_index: 1,
    chunk_type: 'text',
    content_preview: 'preview',
    like_count: 2,
    dislike_count: 1,
    session_count: 2,
    positive_rate: 2 / 3,
    stored_recall_weight: 1.2,
    effective_recall_weight: 1,
    needs_optimization: false,
    updated_at: '2026-07-30T00:00:00Z',
  }
  const detail = { ...item, content: 'full content', reason_counts: {}, audits: [] }
  const resetDetail = {
    ...detail,
    like_count: 0,
    dislike_count: 0,
    positive_rate: null,
    stored_recall_weight: 1,
    effective_recall_weight: 1,
  }
  const api = {
    list: async (targetKB: string) => {
      calls.push(`list:${targetKB}`)
      return page([calls.some((call) => call === 'reset:kb-a:chunk-a') ? resetDetail : item])
    },
    detail: async (targetKB: string, chunkId: string) => {
      calls.push(`detail:${targetKB}:${chunkId}`)
      return { data: detail }
    },
    history: async (targetKB: string, chunkId: string) => {
      calls.push(`history:${targetKB}:${chunkId}`)
      return page([{ id: 1, action: 'feedback_weight_changed' }])
    },
    reset: async (targetKB: string, chunkId: string) => {
      calls.push(`reset:${targetKB}:${chunkId}`)
      return { data: resetDetail }
    },
  } as any

  const scope = effectScope()
  const governance = scope.run(() => useChunkFeedbackGovernance({
    kbId,
    api,
    autoLoad: false,
  }))!

  assert.equal(await governance.load(true), true)
  assert.equal(governance.items.value[0]?.effective_recall_weight, 1)
  assert.equal(await governance.open('chunk-a'), true)
  assert.equal(governance.selected.value?.content, 'full content')
  assert.equal(governance.history.value.length, 1)
  assert.equal(await governance.reset(), true)
  assert.equal(governance.selected.value?.like_count, 0)
  assert.ok(calls.includes('reset:kb-a:chunk-a'))
  assert.equal(calls.filter((call) => call === 'history:kb-a:chunk-a').length, 2)

  kbId.value = 'kb-b'
  await nextTick()
  assert.equal(governance.selected.value, null)
  assert.deepEqual(governance.items.value, [])
  scope.stop()
})

test('stale list response cannot cross a knowledge-base boundary', async () => {
  const kbId = ref('kb-a')
  let resolveList: ((value: any) => void) | undefined
  const api = {
    list: () => new Promise((resolve) => { resolveList = resolve }),
    detail: async () => ({ data: null }),
    history: async () => page([]),
    reset: async () => ({ data: null }),
  } as any
  const scope = effectScope()
  const governance = scope.run(() => useChunkFeedbackGovernance({
    kbId,
    api,
    autoLoad: false,
  }))!

  const pending = governance.load()
  kbId.value = 'kb-b'
  await nextTick()
  resolveList?.(page([{ chunk_id: 'wrong-scope' }]))
  await pending
  assert.deepEqual(governance.items.value, [])
  scope.stop()
})
