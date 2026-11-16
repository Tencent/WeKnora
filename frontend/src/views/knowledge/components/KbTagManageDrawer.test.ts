import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import { fixture, flush } from '../../../test-utils/vueFixture.ts'

const SEARCH_DELAY_MS = 300
const DELETE_REFRESH_DELAY_MS = 800

type Tag = { id: string; seq_id: number; name: string; chunk_count?: number; knowledge_count?: number }
type TagResponse = { data: { data: Tag[]; total: number } }
type ReadRequest = {
  kb: string
  params: { page: number; page_size: number; keyword?: string }
  resolve: (response: TagResponse) => void
  reject: (error: Error) => void
}
type WriteRequest = {
  args: unknown[]
  resolve: (response?: unknown) => void
  reject: (error: Error) => void
}

const { descriptor } = parse(readFileSync(new URL('./KbTagManageDrawer.vue', import.meta.url), 'utf8'))
const script = compileScript(descriptor, { id: 'tag-manage-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const plain = <T>(value: T): T => JSON.parse(JSON.stringify(value))
const tag = (id: string, seq = 1): Tag => ({ id, seq_id: seq, name: id, chunk_count: 3, knowledge_count: 2 })

/** 使用生产脚本和真实 Vue 生命周期；只替换接口及提示边界，返回顺序由测试控制。 */
async function drawerFixture(t: TestContext, initial: Partial<{ visible: boolean; kbId: string; isFaq: boolean }> = {}, beforeFlush?: (vm: any) => void) {
  const f = fixture(t)
  const requests: ReadRequest[] = []
  const writes = { create: [] as WriteRequest[], edit: [] as WriteRequest[], remove: [] as WriteRequest[] }
  const changes: { kb: string; payload: unknown }[] = []
  const messages: { kind: string; message: unknown }[] = []
  const props = vue.reactive({ visible: false, kbId: 'kb-a', isFaq: true, ...initial })
  const captureWrite = (queue: WriteRequest[]) => (...args: unknown[]) => new Promise((resolve, reject) => {
    queue.push({ args: plain(args), resolve, reject })
  })
  const compiled = f.evaluate(script, {
    vue,
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: Object.fromEntries(['success', 'error', 'warning'].map(kind => [
      kind, (message: unknown) => messages.push({ kind, message }),
    ])) },
    '@/components/settings/SettingDrawer.vue': { default: {} },
    '@/api/knowledge-base/index': {
      listKnowledgeTags: (kb: string, params: ReadRequest['params']) => new Promise<TagResponse>((resolve, reject) => {
        requests.push({ kb, params: { ...params }, resolve, reject })
      }),
      createKnowledgeBaseTag: captureWrite(writes.create),
      updateKnowledgeBaseTag: captureWrite(writes.edit),
      deleteKnowledgeBaseTag: captureWrite(writes.remove),
    },
  })
  compiled.default.render = () => null
  const child = vue.ref<any>()
  const { close } = f.mount({ setup: () => () => vue.h(compiled.default, {
    ...props,
    ref: child,
    onChanged: (payload: unknown) => changes.push({ kb: props.kbId, payload: payload === undefined ? undefined : plain(payload) }),
    'onUpdate:visible': (value: boolean) => { props.visible = value },
  }) })
  beforeFlush?.(child.value)
  await flush()
  const vm = child.value
  return {
    vm, props, requests, writes, changes, messages, close,
    async update(next: Partial<typeof props>) { Object.assign(props, next); await flush() },
    async input(value: string) { vm.searchQuery = value; await flush() },
    async tick(ms = SEARCH_DELAY_MS) { t.mock.timers.tick(ms); await flush() },
    async search(value: string) { vm.searchQuery = value; await flush(); t.mock.timers.tick(SEARCH_DELAY_MS); await flush() },
  }
}
type DrawerFixture = Awaited<ReturnType<typeof drawerFixture>>

async function resolveRead(request: ReadRequest, rows: Tag[] = [tag('current')], total = rows.length) {
  assert.ok(request, '应已发出预期的标签查询')
  request.resolve({ data: { data: rows, total } })
  await flush()
}
async function rejectRead(request: ReadRequest) {
  assert.ok(request)
  request.reject(new Error('synthetic query failure'))
  await flush()
}
async function resolveWrite(request: WriteRequest) {
  assert.ok(request, '应已发出预期的标签写请求')
  request.resolve({})
  await flush()
}
async function ready(t: TestContext, rows = [tag('seed')], total = 70) {
  const f = await drawerFixture(t)
  await f.update({ visible: true })
  await resolveRead(f.requests[0], rows, total)
  return f
}
/** 只比较用户可见列表及分页、加载状态，不绑定内部请求序号的实现。 */
function listState(f: DrawerFixture) {
  return plain({ tags: f.vm.tags, total: f.vm.total, page: f.vm.page, hasMore: f.vm.hasMore,
    loading: f.vm.loading, loadingMore: f.vm.loadingMore })
}
async function settleRefresh(f: DrawerFixture, previousCount: number) {
  for (const request of f.requests.slice(previousCount)) await resolveRead(request, [tag('refreshed')])
  await flush()
}
function create(f: DrawerFixture, name = 'new tag') {
  f.vm.startCreateTag()
  f.vm.newTagName = name
  void f.vm.submitCreateTag()
}
function edit(f: DrawerFixture, row = tag('seed'), name = 'renamed') {
  f.vm.startEditTag(row)
  f.vm.editingTagName = name
  void f.vm.submitEditTag()
}

for (const visible of [false, true]) {
  test(`初始 visible=${visible} 时只在可见状态查询一次`, async t => {
    const f = await drawerFixture(t, { visible })
    assert.equal(f.requests.length, visible ? 1 : 0)
    if (visible) {
      assert.deepEqual(f.requests[0].params, { page: 1, page_size: 50, keyword: undefined })
      await resolveRead(f.requests[0])
      assert.equal(f.vm.loading, false)
    }
  })
}

test('空知识库不查询，打开状态补齐知识库后读取第一页', async t => {
  const f = await drawerFixture(t, { visible: true, kbId: '' })
  assert.equal(f.requests.length, 0)
  await f.update({ kbId: 'kb-a' })
  assert.equal(f.requests.length, 1)
  assert.equal(f.requests[0].kb, 'kb-a')
})

test('连续输入只在最后一次输入满 300ms 后请求最终关键词', async t => {
  const f = await ready(t)
  await f.input('a'); await f.tick(150)
  await f.input('alpha'); await f.tick(299)
  assert.equal(f.requests.length, 1)
  await f.tick(1)
  assert.equal(f.requests.length, 2)
  assert.equal(f.requests[1].params.keyword, 'alpha')
  assert.equal(f.requests[1].params.page, 1)
})

for (const outcome of ['success', 'error'] as const) {
  test(`防抖期间旧查询 ${outcome} 立即失效，不能提前结束新 loading`, async t => {
    const f = await ready(t)
    await f.search('alpha')
    await f.input('beta')
    const before = listState(f)
    if (outcome === 'success') await resolveRead(f.requests[1], [tag('old')], 500)
    else await rejectRead(f.requests[1])
    assert.deepEqual(listState(f), before)
    await f.tick()
    assert.equal(f.requests[2].params.keyword, 'beta')
    await resolveRead(f.requests[2], [tag('beta')])
    assert.equal(f.vm.tags[0].id, 'beta')
    assert.equal(f.vm.loading, false)
  })
  test(`新结果完成后，旧查询 ${outcome} 不回写列表或分页`, async t => {
    const f = await ready(t)
    await f.search('alpha'); await f.search('beta')
    await resolveRead(f.requests[2], [tag('beta')], 60)
    const before = listState(f)
    if (outcome === 'success') await resolveRead(f.requests[1], [tag('alpha')], 300)
    else await rejectRead(f.requests[1])
    assert.deepEqual(listState(f), before)
  })
}

test('旧请求结束时不能关闭仍在请求中的新 loading', async t => {
  const f = await ready(t)
  await f.search('alpha'); await f.search('beta')
  const before = listState(f)
  assert.equal(before.loading, true)
  await resolveRead(f.requests[1], [tag('old')])
  assert.deepEqual(listState(f), before)
  await resolveRead(f.requests[2])
  assert.equal(f.vm.loading, false)
})

test('清空后重新输入同词，第一轮同词响应仍然失效', async t => {
  const f = await ready(t)
  await f.search('same')
  await f.input(''); await f.search('same')
  const before = listState(f)
  await resolveRead(f.requests[1], [tag('first same')])
  assert.deepEqual(listState(f), before)
  await resolveRead(f.requests[2], [tag('second same')])
  assert.equal(f.vm.tags[0].id, 'second same')
})

test('同一 tick 的 A→B→A 输入也使旧 A 请求失效', async t => {
  const f = await ready(t)
  await f.search('alpha')
  f.vm.searchQuery = 'beta'; f.vm.searchQuery = 'alpha'
  await flush()
  const before = listState(f)
  await resolveRead(f.requests[1], [tag('stale alpha')])
  assert.deepEqual(listState(f), before)
  await f.tick()
  assert.equal(f.requests.length, 3)
  assert.equal(f.requests[2].params.keyword, 'alpha')
})

test('清空输入查询第一页且不发送 keyword 参数', async t => {
  const f = await ready(t)
  await f.search('alpha'); await resolveRead(f.requests[1])
  await f.search('')
  assert.deepEqual(f.requests[2].params, { page: 1, page_size: 50, keyword: undefined })
  await resolveRead(f.requests[2], [])
  assert.deepEqual(plain(f.vm.tags), [])
  assert.equal(f.vm.total, 0)
  assert.equal(f.vm.hasMore, false)
})

test('正常分页追加一次并在无更多结果后停止推进', async t => {
  const f = await ready(t, [tag('first')], 2)
  void f.vm.loadTags(false); void f.vm.loadTags(false)
  assert.equal(f.requests.length, 2)
  assert.equal(f.requests[1].params.page, 2)
  assert.equal(f.vm.loadingMore, true)
  await resolveRead(f.requests[1], [tag('second')], 2)
  assert.deepEqual(plain(f.vm.tags.map((row: Tag) => row.id)), ['first', 'second'])
  assert.equal(f.vm.hasMore, false)
  assert.equal(f.vm.loadingMore, false)
})

test('分页失败保留已有结果和游标，重试仍请求同一页', async t => {
  const f = await ready(t)
  void f.vm.loadTags(false)
  await rejectRead(f.requests[1])
  assert.equal(f.vm.tags[0].id, 'seed')
  assert.equal(f.vm.page, 2)
  assert.equal(f.vm.hasMore, true)
  assert.equal(f.vm.loadingMore, false)
  void f.vm.loadTags(false)
  assert.equal(f.requests[2].params.page, 2)
  await resolveRead(f.requests[2], [tag('recovered')], 2)
  assert.deepEqual(plain(f.vm.tags.map((row: Tag) => row.id)), ['seed', 'recovered'])
})

test('旧第 2 页不能追加到新搜索或更改新页码', async t => {
  const f = await ready(t)
  void f.vm.loadTags(false)
  await f.search('beta')
  await resolveRead(f.requests[2], [tag('beta')], 51)
  const before = listState(f)
  await resolveRead(f.requests[1], [tag('old page 2')], 500)
  assert.deepEqual(listState(f), before)
})

test('新搜索防抖等待期间不能继续查询旧分页', async t => {
  const f = await ready(t)
  await f.input('beta')
  void f.vm.loadTags(false)
  assert.equal(f.requests.length, 1)
  await f.tick()
  assert.equal(f.requests.length, 2)
  assert.equal(f.requests[1].params.page, 1)
  assert.equal(f.requests[1].params.keyword, 'beta')
})

test('关闭取消防抖，隐藏状态不再发标签请求', async t => {
  const f = await ready(t)
  await f.input('pending'); await f.tick(100)
  await f.update({ visible: false }); await f.tick(600)
  assert.equal(f.requests.length, 1)
  assert.equal(f.vm.searchQuery, '')
})

for (const outcome of ['success', 'error'] as const) {
  test(`关闭重开后旧会话 ${outcome} 不影响新请求`, async t => {
    const f = await ready(t)
    await f.search('old')
    await f.update({ visible: false }); await f.update({ visible: true })
    assert.equal(f.requests[2].params.keyword, undefined)
    const before = listState(f)
    if (outcome === 'success') await resolveRead(f.requests[1], [tag('stale')])
    else await rejectRead(f.requests[1])
    assert.deepEqual(listState(f), before)
    await resolveRead(f.requests[2], [tag('reopened')])
    assert.equal(f.vm.tags[0].id, 'reopened')
  })
}

test('打开时切知识库清查询和旧行，并读取新知识库', async t => {
  const f = await ready(t)
  await f.search('alpha')
  await f.update({ kbId: 'kb-b' })
  assert.equal(f.vm.searchQuery, '')
  assert.deepEqual(plain(f.vm.tags), [])
  assert.equal(f.requests[2].kb, 'kb-b')
  assert.equal(f.requests[2].params.keyword, undefined)
  const before = listState(f)
  await resolveRead(f.requests[1], [tag('from a')])
  assert.deepEqual(listState(f), before)
  await resolveRead(f.requests[2], [tag('from b')])
  assert.equal(f.vm.tags[0].id, 'from b')
})

test('知识库 A→B→A 后不接受第一轮 A 响应', async t => {
  const f = await ready(t)
  await f.search('old a')
  await f.update({ kbId: 'kb-b' }); await f.update({ kbId: 'kb-a' })
  assert.equal(f.requests.length, 4)
  await resolveRead(f.requests[3], [tag('new a')])
  const before = listState(f)
  await resolveRead(f.requests[1], [tag('old a')]); await resolveRead(f.requests[2], [tag('b')])
  assert.deepEqual(listState(f), before)
})

test('知识库变空后清理状态并忽略迟到响应', async t => {
  const f = await ready(t)
  await f.search('alpha')
  await f.update({ kbId: '' })
  const before = listState(f)
  assert.deepEqual(before.tags, [])
  assert.equal(before.loading, false)
  await resolveRead(f.requests[1], [tag('old')])
  assert.deepEqual(listState(f), before)
  await f.tick(600)
  assert.equal(f.requests.length, 2)
})

test('关闭期间切知识库不请求，重开仅加载当前知识库', async t => {
  const f = await ready(t)
  await f.update({ visible: false }); await f.update({ kbId: 'kb-b' })
  await f.tick(600)
  assert.equal(f.requests.length, 1)
  await f.update({ visible: true })
  assert.equal(f.requests.length, 2)
  assert.equal(f.requests[1].kb, 'kb-b')
})

test('同时改变可见性与知识库只读取最终知识库一次', async t => {
  const f = await drawerFixture(t)
  await f.update({ kbId: 'kb-b', visible: true })
  assert.equal(f.requests.length, 1)
  assert.equal(f.requests[0].kb, 'kb-b')
})

test('展示模式变化清理旧编辑会话，旧响应不回写', async t => {
  const f = await ready(t)
  f.vm.startCreateTag(); f.vm.newTagName = 'old draft'
  await f.search('old query')
  await f.update({ isFaq: false })
  assert.equal(f.vm.creatingTag, false)
  assert.equal(f.vm.newTagName, '')
  const before = listState(f)
  await resolveRead(f.requests[1], [tag('stale mode')])
  assert.deepEqual(listState(f), before)
})

test('真实卸载取消尚未发出的防抖查询', async t => {
  const f = await ready(t)
  await f.input('pending')
  f.close(); await f.tick(600)
  assert.equal(f.requests.length, 1)
})

for (const outcome of ['success', 'error'] as const) {
  test(`卸载后的查询 ${outcome} 不再写组件状态或 emit`, async t => {
    const f = await ready(t)
    await f.search('pending'); f.close()
    const before = listState(f)
    if (outcome === 'success') await resolveRead(f.requests[1], [tag('late')])
    else await rejectRead(f.requests[1])
    assert.deepEqual(listState(f), before)
    assert.equal(f.changes.length, 0)
  })
}

for (const kind of ['create', 'edit', 'remove'] as const) {
  test(`${kind} 成功按当前关键词刷新，并取消重复的搜索防抖`, async t => {
    const f = await ready(t)
    if (kind === 'create') create(f)
    else if (kind === 'edit') edit(f)
    else void f.vm.deleteTag(tag('seed'))
    await f.input('current query')
    const count = f.requests.length
    await resolveWrite(f.writes[kind][0])
    assert.equal(f.requests.length, count + 1)
    assert.equal(f.requests.at(-1)!.params.keyword, 'current query')
    await settleRefresh(f, count)
    await f.tick(350)
    assert.equal(f.requests.length, count + 1)
    assert.equal(f.changes.length, 1)
  })
  test(`${kind} 在同知识库关闭后成功仍通知父列表，但不查询隐藏抽屉`, async t => {
    const f = await ready(t)
    if (kind === 'create') create(f)
    else if (kind === 'edit') edit(f)
    else void f.vm.deleteTag(tag('seed'))
    await f.update({ visible: false })
    const count = f.requests.length
    await resolveWrite(f.writes[kind][0])
    assert.equal(f.requests.length, count)
    assert.equal(f.changes.length, 1)
    assert.equal(f.changes[0].kb, 'kb-a')
    assert.equal(f.messages.length, 0, '旧抽屉会话不再弹出成功提示')
  })
  test(`${kind} 在切知识库后成功不刷新新列表或发送旧 changed`, async t => {
    const f = await ready(t)
    if (kind === 'create') create(f)
    else if (kind === 'edit') edit(f)
    else void f.vm.deleteTag(tag('seed'))
    await f.update({ kbId: 'kb-b' })
    const count = f.requests.length, before = listState(f)
    await resolveWrite(f.writes[kind][0]); await f.tick(1000)
    assert.equal(f.requests.length, count)
    assert.equal(f.changes.length, 0)
    assert.equal(f.messages.length, 0)
    assert.deepEqual(listState(f), before)
  })
}

for (const kind of ['create', 'edit'] as const) {
  test(`旧 ${kind} 完成不能清掉重开后的新草稿或关闭新编辑行`, async t => {
    const f = await ready(t)
    if (kind === 'create') create(f, 'old name')
    else edit(f, tag('seed'), 'old name')
    await f.update({ visible: false }); await f.update({ visible: true })
    await resolveRead(f.requests.at(-1)!, [tag('seed')])
    if (kind === 'create') { f.vm.startCreateTag(); f.vm.newTagName = 'new draft' }
    else { f.vm.startEditTag(tag('seed')); f.vm.editingTagName = 'new draft' }
    const count = f.requests.length
    await resolveWrite(f.writes[kind][0]); await settleRefresh(f, count)
    assert.equal(kind === 'create' ? f.vm.newTagName : f.vm.editingTagName, 'new draft')
    assert.equal(kind === 'create' ? f.vm.creatingTag : f.vm.editingTagId === 'seed', true)
    assert.equal(f.messages.length, 0)
    assert.equal(f.changes.length, 1)
  })
  test(`旧 ${kind} 的 finally 不结束新会话的提交 loading`, async t => {
    const f = await ready(t)
    if (kind === 'create') create(f, 'old name')
    else edit(f, tag('seed'), 'old name')
    await f.update({ visible: false }); await f.update({ visible: true })
    await resolveRead(f.requests.at(-1)!, [tag('seed')])
    if (kind === 'create') create(f, 'new name')
    else edit(f, tag('seed'), 'new name')
    assert.equal(f.writes[kind].length, 2)
    const count = f.requests.length
    await resolveWrite(f.writes[kind][0]); await settleRefresh(f, count)
    assert.equal(kind === 'create' ? f.vm.creatingTagLoading : f.vm.editingTagSubmitting, true)
    const nextCount = f.requests.length
    await resolveWrite(f.writes[kind][1]); await settleRefresh(f, nextCount)
    assert.equal(kind === 'create' ? f.vm.creatingTagLoading : f.vm.editingTagSubmitting, false)
  })
  test(`旧 ${kind} 失败不提示错误或清理新会话`, async t => {
    const f = await ready(t)
    if (kind === 'create') create(f)
    else edit(f)
    await f.update({ visible: false }); await f.update({ visible: true })
    if (kind === 'create') { f.vm.startCreateTag(); f.vm.newTagName = 'new draft' }
    else { f.vm.startEditTag(tag('seed')); f.vm.editingTagName = 'new draft' }
    f.writes[kind][0].reject(new Error('old operation failed')); await flush()
    assert.equal(f.messages.length, 0)
    assert.equal(kind === 'create' ? f.vm.newTagName : f.vm.editingTagName, 'new draft')
  })
}

test('取消后同一抽屉重新创建，旧完成也不能清掉新草稿', async t => {
  const f = await ready(t)
  create(f, 'old name')
  f.vm.cancelCreateTag(); f.vm.startCreateTag(); f.vm.newTagName = 'new draft'
  const count = f.requests.length
  await resolveWrite(f.writes.create[0]); await settleRefresh(f, count)
  assert.equal(f.vm.newTagName, 'new draft')
  assert.equal(f.vm.creatingTag, true)
})

test('编辑 A 未完成时改为编辑 B，旧完成不取消 B 编辑', async t => {
  const f = await ready(t, [tag('a'), tag('b', 2)])
  edit(f, tag('a'), 'renamed a')
  f.vm.startEditTag(tag('b', 2)); f.vm.editingTagName = 'draft b'
  const count = f.requests.length
  await resolveWrite(f.writes.edit[0]); await settleRefresh(f, count)
  assert.equal(f.vm.editingTagId, 'b')
  assert.equal(f.vm.editingTagName, 'draft b')
  assert.deepEqual(f.writes.edit[0].args, ['kb-a', 'a', { name: 'renamed a' }])
})

test('知识库 A→B→A 后旧写完成不能通知新一轮 A', async t => {
  const f = await ready(t)
  create(f)
  await f.update({ kbId: 'kb-b' }); await f.update({ kbId: 'kb-a' })
  const count = f.requests.length
  await resolveWrite(f.writes.create[0])
  assert.equal(f.requests.length, count)
  assert.equal(f.changes.length, 0)
})

for (const boundary of ['close', 'switch', 'unmount'] as const) {
  test(`删除延迟通知在 ${boundary} 边界按知识库归属保留或取消`, async t => {
    const f = await ready(t)
    void f.vm.deleteTag(tag('seed', 7))
    const count = f.requests.length
    await resolveWrite(f.writes.remove[0]); await settleRefresh(f, count)
    assert.deepEqual(f.writes.remove[0].args, ['kb-a', 7, { force: true }])
    assert.equal(f.changes.length, 1)
    if (boundary === 'close') await f.update({ visible: false })
    else if (boundary === 'switch') await f.update({ kbId: 'kb-b' })
    else f.close()
    await f.tick(DELETE_REFRESH_DELAY_MS)
    assert.equal(f.changes.length, boundary === 'close' ? 2 : 1)
    for (const change of f.changes) assert.deepEqual(change, { kb: 'kb-a', payload: { deletedTagId: 'seed' } })
  })
}

test('卸载后写成功不刷新、不通知，也不弹提示', async t => {
  const f = await ready(t)
  create(f); f.close()
  const count = f.requests.length
  await resolveWrite(f.writes.create[0])
  assert.equal(f.requests.length, count)
  assert.equal(f.changes.length, 0)
  assert.equal(f.messages.length, 0)
})

test('空名称与未改名编辑保持原校验，不发写请求', async t => {
  const f = await ready(t)
  create(f, '   ')
  assert.equal(f.writes.create.length, 0)
  assert.equal(f.messages.at(-1)?.kind, 'warning')
  edit(f, tag('seed'), '   ')
  assert.equal(f.writes.edit.length, 0)
  edit(f, tag('seed'), 'seed')
  assert.equal(f.writes.edit.length, 0)
  assert.equal(f.vm.editingTagId, null)
})

test('当前写失败保留草稿并结束 loading，允许重试', async t => {
  const f = await ready(t)
  create(f, 'keep draft')
  f.writes.create[0].reject(new Error('write failed')); await flush()
  assert.equal(f.vm.newTagName, 'keep draft')
  assert.equal(f.vm.creatingTag, true)
  assert.equal(f.vm.creatingTagLoading, false)
  assert.deepEqual(f.messages.at(-1), { kind: 'error', message: 'write failed' })
  void f.vm.submitCreateTag()
  assert.equal(f.writes.create.length, 2)
})

test('Esc 保留取消编辑行为且阻止默认事件', async t => {
  const f = await ready(t)
  f.vm.startCreateTag(); f.vm.newTagName = 'draft'
  let stopped = false, prevented = false
  f.vm.onEditKeydown({ e: { key: 'Escape', stopPropagation: () => { stopped = true }, preventDefault: () => { prevented = true } } }, f.vm.cancelCreateTag)
  assert.equal(f.vm.creatingTag, false)
  assert.equal(f.vm.newTagName, '')
  assert.equal(stopped, true)
  assert.equal(prevented, true)
})

test('首屏尚未调度完成时输入变化，不被旧首屏回调取消防抖或重复请求', async t => {
  const f = await drawerFixture(t, { visible: true }, vm => { vm.searchQuery = 'early query' })
  assert.equal(f.requests.length, 0)
  await f.tick(299)
  assert.equal(f.requests.length, 0)
  await f.tick(1)
  assert.equal(f.requests.length, 1)
  assert.equal(f.requests[0].params.keyword, 'early query')
})

for (const kind of ['create', 'edit'] as const) {
  test(`同一 ${kind} 提交未完成时重复点击不重复发写请求`, async t => {
    const f = await ready(t)
    if (kind === 'create') { create(f); void f.vm.submitCreateTag() }
    else { edit(f); void f.vm.submitEditTag() }
    assert.equal(f.writes[kind].length, 1)
    assert.equal(kind === 'create' ? f.vm.creatingTagLoading : f.vm.editingTagSubmitting, true)
  })

  test(`${kind} 正常聚焦，取消、关闭和卸载后不执行迟到的聚焦`, async t => {
    const f = await ready(t)
    let focused = 0, selected = 0
    const input = { focus: () => { focused += 1 }, select: () => { selected += 1 } }
    if (kind === 'create') f.vm.newTagInputRef = input
    else f.vm.setEditingTagInputRef(input, 'seed')
    const start = () => kind === 'create' ? f.vm.startCreateTag() : f.vm.startEditTag(tag('seed'))
    const cancel = () => kind === 'create' ? f.vm.cancelCreateTag() : f.vm.cancelEditTag()
    start(); await flush()
    assert.equal(focused, 1, '当前编辑正常聚焦')
    assert.equal(selected, 1)
    cancel(); start(); cancel(); await flush()
    assert.equal(focused, 1, '取消编辑后不聚焦')
    // 先排入父组件关闭更新，确保断言的是关闭后的回调，而非关闭生效前的正常聚焦。
    f.props.visible = false; start(); await flush()
    assert.equal(focused, 1, '关闭抽屉后不聚焦')
    await f.update({ visible: true })
    start(); f.close(); await flush()
    assert.equal(focused, 1, '卸载后不聚焦')
    assert.equal(selected, 1)
  })
}
