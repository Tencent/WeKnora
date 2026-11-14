import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { computed, ref } from 'vue'
import { ScriptTarget, transpileModule } from 'typescript'
import { createEmptyBucket, removeSessionFromBuckets } from './sessionSidebarBuckets'

// Execute the sidebar handlers with deferred API responses and real bucket reconciliation.
// Mock only the dialog and API boundaries so targets and local effects remain observable.
const source = readFileSync(new URL('./menu.vue', import.meta.url), 'utf8')
const start = source.indexOf('const enterBatchMode = '),
  end = source.indexOf('const handleSessionMenuClick = ', start)
assert(start >= 0 && end > start)
interface Confirmation {
  onConfirm(): Promise<void>
  onClose?(): void
}
function fixture() {
  const selected = ref(['A']),
    deleting = ref(false),
    buckets = ref({
      web: {
        ...createEmptyBucket({ key: 'web', apiSource: '', label: 'Chats', kind: 'web' }),
        items: [
          { id: 'A', title: 'A', created_at: '2026-11-04T00:00:00Z' },
          { id: 'B', title: 'B', created_at: '2026-11-04T00:00:00Z' },
        ],
        total: 2,
      },
    }),
    calls: string[][] = [],
    navigation: string[] = [],
    notices: string[] = [],
    updates: Record<string, unknown>[] = []
  let confirm!: Confirmation
  let settle!: (result: { success: boolean }) => void
  let reject!: (error: Error) => void
  let destroyed = 0
  const response = new Promise<{ success: boolean }>((yes, no) => {
    settle = yes
    reject = no
  })
  const context = {
    batchMode: ref(true),
    batchSelectedIds: selected,
    batchDeleting: deleting,
    allSessionIds: ref(['A', 'B']),
    isAllBatchSelected: computed(() => selected.value.length === 2),
    batchDisplayCount: computed(() => selected.value.length),
    sessionBuckets: buckets,
    total: ref(2),
    route: { params: { chatid: 'A' } },
    router: { push: (path: string) => navigation.push(path) },
    usemenuStore: { clearMenuArr() {} },
    t: (key: string) => key,
    DialogPlugin: {
      confirm: (options: Confirmation) => {
        confirm = options
        return {
          destroy: () => destroyed++,
          update: (options: Record<string, unknown>) => updates.push(options),
        }
      },
    },
    MessagePlugin: {
      success: () => notices.push('success'),
      error: () => notices.push('error'),
    },
    batchDelSessions: (ids: string[]) => {
      calls.push(ids)
      return response
    },
    deleteAllSessions: () => {
      calls.push(['ALL'])
      return response
    },
    removeSessionFromBuckets,
    syncMenuStoreFromBuckets() {},
    getMessageList: async () => {
      buckets.value.web = { ...buckets.value.web, items: [], total: 0 }
    },
  }
  const code = transpileModule(
    source.slice(start, end) +
      '\nreturn {handleInlineBatchDelete, toggleBatchSelect, toggleBatchSelectAll, enterBatchMode, exitBatchMode};',
    { compilerOptions: { target: ScriptTarget.ES2022 } },
  ).outputText
  const handlers = new Function(...Object.keys(context), code)(...Object.values(context)) as {
    handleInlineBatchDelete(): void
    toggleBatchSelect(id: string): void
    toggleBatchSelectAll(checked: boolean): void
    enterBatchMode(): void
    exitBatchMode(): void
  }
  return {
    handlers,
    batchMode: context.batchMode,
    route: context.route,
    selected,
    deleting,
    buckets,
    remaining: () => buckets.value.web.items.map((row) => row.id),
    calls,
    navigation,
    notices,
    updates,
    settle,
    reject,
    confirm: () => confirm,
    destroyed: () => destroyed,
  }
}

test('batch deletion freezes confirmed IDs and reconciles only those conversations', async () => {
  const f = fixture()
  f.handlers.handleInlineBatchDelete()
  const pending = f.confirm().onConfirm()
  assert.deepEqual(f.calls, [['A']])
  // A late selection update must never change the acknowledged target set.
  f.selected.value = ['B']
  f.settle({ success: true })
  await pending
  assert.deepEqual(f.remaining(), ['B'])
  assert.deepEqual(f.navigation, ['/platform/creatChat'])
  assert.deepEqual(f.notices, ['success'])
})

test('batch confirmation suppresses duplicate writes and locks selection while pending', async () => {
  const f = fixture()
  f.handlers.handleInlineBatchDelete()
  const first = f.confirm().onConfirm()
  const repeated = f.confirm().onConfirm()
  f.handlers.toggleBatchSelect('B')
  f.handlers.toggleBatchSelectAll(false)
  f.handlers.exitBatchMode()
  f.handlers.enterBatchMode()
  f.handlers.handleInlineBatchDelete()
  assert.equal(f.batchMode.value, true)
  const snapshot = [...f.selected.value]
  f.settle({ success: true })
  await Promise.all([first, repeated])
  assert.deepEqual(f.calls, [['A']])
  assert.deepEqual(snapshot, ['A'])
  assert.equal(f.deleting.value, false)
  assert.equal(f.destroyed(), 1)
  assert.equal(f.updates[0]?.confirmLoading, true)
})

test('batch failures release the guard without claiming success or discarding local targets', async () => {
  const f = fixture()
  f.handlers.handleInlineBatchDelete()
  const pending = f.confirm().onConfirm()
  f.reject(new Error('Synthetic interrupted response'))
  await pending
  assert.deepEqual(f.calls, [['A']])
  assert.deepEqual(f.remaining(), ['A', 'B'])
  assert.deepEqual(f.notices, ['error'])
  assert.equal(f.deleting.value, false)
  assert.equal(f.destroyed(), 1)
})

test('cancelling a batch confirmation never sends a mutation', () => {
  const f = fixture()
  f.handlers.handleInlineBatchDelete()
  f.confirm().onClose?.()
  assert.deepEqual(f.calls, [])
  assert.deepEqual(f.remaining(), ['A', 'B'])
})

test('the all-conversations action remains fixed while transport is pending', async () => {
  const f = fixture()
  f.selected.value = ['A', 'B']
  f.handlers.handleInlineBatchDelete()
  const pending = f.confirm().onConfirm()
  f.confirm().onClose?.()
  assert.equal(f.destroyed(), 0, 'A pending mutation cannot be dismissed as cancelled')
  f.selected.value = ['B']
  f.settle({ success: true })
  await pending
  await f.confirm().onConfirm()
  assert.deepEqual(f.calls, [['ALL']])
  assert.deepEqual(f.navigation, ['/platform/creatChat'])
  assert.deepEqual(f.remaining(), [])
  assert.equal(f.batchMode.value, false)
  assert.equal(f.destroyed(), 1)
})

test('the request uses the IDs acknowledged when the confirmation opened', async () => {
  const f = fixture()
  f.handlers.handleInlineBatchDelete()
  f.selected.value = ['B']
  const pending = f.confirm().onConfirm()
  f.settle({ success: true })
  await pending
  assert.deepEqual(f.calls, [['A']])
  assert.deepEqual(f.remaining(), ['B'])
})

test('an unsuccessful response preserves targets and allows a new explicit attempt', async () => {
  const f = fixture()
  f.handlers.handleInlineBatchDelete()
  const pending = f.confirm().onConfirm()
  f.settle({ success: false })
  await pending
  assert.deepEqual(f.remaining(), ['A', 'B'])
  assert.deepEqual(f.selected.value, ['A'])
  assert.deepEqual(f.notices, ['error'])
  assert.equal(f.deleting.value, false)
  f.handlers.handleInlineBatchDelete()
  await f.confirm().onConfirm()
  assert.deepEqual(f.calls, [['A'], ['A']])
})

test('deleting another conversation preserves the active route', async () => {
  const f = fixture()
  f.route.params.chatid = 'B'
  f.handlers.handleInlineBatchDelete()
  const pending = f.confirm().onConfirm()
  f.settle({ success: true })
  await pending
  assert.deepEqual(f.remaining(), ['B'])
  assert.deepEqual(f.navigation, [])
})

test('an empty selection never opens a confirmation or sends a request', () => {
  const f = fixture()
  f.selected.value = []
  f.handlers.handleInlineBatchDelete()
  assert.equal(f.confirm(), undefined)
  assert.deepEqual(f.calls, [])
})

test('a failed delete-all request preserves conversations and releases the lock', async () => {
  const f = fixture()
  f.selected.value = ['A', 'B']
  f.handlers.handleInlineBatchDelete()
  const pending = f.confirm().onConfirm()
  f.reject(new Error('Synthetic delete-all failure'))
  await pending
  assert.deepEqual(f.calls, [['ALL']])
  assert.deepEqual(f.remaining(), ['A', 'B'])
  assert.deepEqual(f.selected.value, ['A', 'B'])
  assert.deepEqual(f.navigation, [])
  assert.deepEqual(f.notices, ['error'])
  assert.equal(f.deleting.value, false)
})
