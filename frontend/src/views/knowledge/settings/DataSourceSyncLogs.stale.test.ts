import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { fixture, flush } from '../../../test-utils/vueFixture'

interface FakeLog {
  id: string
  status: string
  started_at: string
  finished_at: string
  items_created: number
  items_updated: number
  items_deleted: number
  items_skipped: number
  items_failed: number
  items_total: number
  error_message: string
}

function log(id: string, created: number): FakeLog {
  return {
    id,
    status: 'success',
    started_at: '2026-11-20T10:00:00Z',
    finished_at: '2026-11-20T10:00:05Z',
    items_created: created,
    items_updated: 0,
    items_deleted: 0,
    items_skipped: 0,
    items_failed: 0,
    items_total: created,
    error_message: '',
  }
}

type Handler = (offset: number, callIndex: number) => Promise<unknown>

function setup(t: TestContext) {
  const f = fixture(t)
  let handler: Handler = async () => ({ data: [] })
  const calls: { id: string; limit: number; offset: number }[] = []
  const api = {
    getSyncLogs: t.mock.fn((id: string, limit: number, offset: number) => {
      calls.push({ id, limit, offset })
      return handler(offset, calls.length)
    }),
  }
  const { vm } = f.component(
    new URL('./DataSourceSyncLogs.vue', import.meta.url),
    {
      'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
      '@/api/datasource': api,
    },
    { dataSourceId: 'ds-A', dataSourceName: 'Source A', visible: false },
  )
  return { vm, api, calls, setHandler: (h: Handler) => { handler = h } }
}

test('a stale response from a previously opened data source is discarded when the drawer reopens', async (t) => {
  const { vm, calls, setHandler } = setup(t)
  // 首次打开抽屉（数据源 A）的请求挂起不返回；抽屉关闭后重开（数据源 B）的请求立即返回
  let releaseStale!: (value: unknown) => void
  const stale = new Promise((resolve) => { releaseStale = resolve })
  setHandler(async (_offset, callIndex) => {
    if (callIndex === 1) return stale
    return { data: [log('b1', 1)] }
  })

  vm.visible = true
  await flush()
  assert.equal(calls.length, 1)
  assert.equal(vm.loading, true)

  // 关闭抽屉再打开（父组件改开另一个数据源）——真实触发序列
  vm.visible = false
  await flush()
  vm.visible = true
  await flush()
  assert.equal(calls.length, 2)
  assert.equal(vm.logs.length, 1)
  assert.equal(vm.logs[0].id, 'b1')
  assert.equal(vm.hasMore, false)
  assert.equal(vm.loading, false)

  // 数据源 A 的旧响应此时才落地：必须被丢弃，不得覆盖 B 的日志、不得污染 hasMore
  releaseStale({ data: Array.from({ length: 50 }, (_, i) => log(`a${i + 1}`, 60)) })
  await flush()
  assert.equal(vm.logs.length, 1)
  assert.equal(vm.logs[0].id, 'b1')
  assert.equal(vm.hasMore, false)
  assert.equal(vm.loading, false)
})

test('a stale load-more response after a refresh is discarded without sticking the loadingMore flag', async (t) => {
  const { vm, calls, setHandler } = setup(t)
  const page1 = Array.from({ length: 50 }, (_, i) => log(`f${i + 1}`, 1))
  let releaseAppend!: (value: unknown) => void
  const pendingAppend = new Promise((resolve) => { releaseAppend = resolve })
  setHandler(async (offset, callIndex) => {
    if (callIndex === 1) return { data: page1 }
    if (offset === 50) return pendingAppend
    return { data: [log('fresh1', 2)] }
  })

  vm.visible = true
  await flush()
  assert.equal(vm.logs.length, 50)
  assert.equal(vm.hasMore, true)

  vm.loadMore()
  await flush()
  assert.equal(calls.length, 2)
  assert.equal(vm.loadingMore, true)

  // 刷新触发重置加载：旧 append 在途被丢弃，loadingMore 不得卡死
  vm.fetchLogs(true)
  await flush()
  assert.equal(vm.loadingMore, false)
  assert.equal(vm.logs.length, 1)
  assert.equal(vm.logs[0].id, 'fresh1')

  releaseAppend({ data: [log('stale1', 1)] })
  await flush()
  assert.equal(vm.logs.length, 1)
  assert.equal(vm.logs[0].id, 'fresh1')
  assert.equal(vm.loadingMore, false)
})
