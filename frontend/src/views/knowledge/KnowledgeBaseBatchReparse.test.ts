import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import * as stall from '../../utils/knowledgeProcessingStall.ts'
import { fixture, flush } from '../../test-utils/vueFixture.ts'

type Row = {
  id: string
  parse_status: string
  summary_status?: string
  description?: string
}
type QueryResult = { success: boolean; data: Row[] }
type ListQuery = { page: number; page_size: number; folder_path?: string }

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((accept, fail) => { resolve = accept; reject = fail })
  return { promise, resolve, reject }
}

function row(id: string, parse_status = 'completed', description = 'original'): Row {
  return { id, parse_status, summary_status: 'completed', description }
}

// 每推进一小段时间，就处理 Vue 监听器和 Promise 回调，
// 避免一次性触发所有定时器，制造真实运行中不会出现的执行顺序。
async function advance(t: TestContext, milliseconds: number) {
  for (let remaining = milliseconds; remaining > 0; remaining -= 100) {
    t.mock.timers.tick(Math.min(100, remaining))
    await flush()
  }
}

async function setup(t: TestContext, initialRows: Row[] = [row('doc')]) {
  const f = fixture(t)
  const route = vue.reactive({ params: { kbId: 'kb' }, query: {} })
  const scopes = new Map<string, Row[]>([['kb/', initialRows.map(item => ({ ...item }))]])
  const cards = vue.ref<Row[]>([])
  const total = vue.ref(0)
  const getKnowled = t.mock.fn(async (query: ListQuery, kb: string) => {
    const source = scopes.get(kb + '/' + (query.folder_path || '')) || []
    const start = (query.page - 1) * query.page_size
    const next = source.slice(start, start + query.page_size).map(item => ({ ...item }))
    if (query.page === 1) cards.value = next
    else cards.value.push(...next)
    total.value = source.length
  })
  const hook = {
    cardList: cards, total, moreIndex: vue.ref(-1), details: vue.reactive({ id: '' }),
    getKnowled, getCardDetails() {}, getfDetails() {}, onVisibleChange() {},
  }
  const queries: Array<{ ids: string[]; result: ReturnType<typeof deferred<QueryResult>> }> = []
  let autoReply = false
  const batchQueryKnowledge = t.mock.fn((query: string) => {
    const ids = new URLSearchParams(query).getAll('ids')
    const result = deferred<QueryResult>()
    queries.push({ ids, result })
    if (autoReply) {
      const serverRows = scopes.get(route.params.kbId + '/') || []
      result.resolve({
        success: true,
        data: serverRows.filter(item => ids.includes(item.id)).map(item => ({ ...item })),
      })
    }
    return result.promise
  })
  let holdSubmission = false
  const submissions: Array<{ kb: string; ids: string[]; result: ReturnType<typeof deferred<{ success: boolean }>> }> = []
  const batchReparseKnowledge = t.mock.fn((kb: string, ids: string[]) => {
    const result = deferred<{ success: boolean }>()
    submissions.push({ kb, ids: [...ids], result })
    if (!holdSubmission) result.resolve({ success: true })
    return result.promise
  })
  const messages = { success: t.mock.fn(), error: t.mock.fn(), warning: t.mock.fn(), info: t.mock.fn() }
  const page = f.component(new URL('./KnowledgeBase.vue', import.meta.url), {
    'vue-router': { useRoute: () => route, useRouter: () => ({ replace() {} }) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: messages },
    '@/hooks/useKnowledgeBase': () => hook,
    '@/stores/ui': { useUIStore: () => ({ clearSelectedTagIds() {} }) },
    '@/stores/organization': { useOrganizationStore: () => ({ sharedKnowledgeBases: [], getKBPermission: () => 'owner' }) },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/stores/chatResources': { useChatResourcesStore: () => ({
      fetchKnowledgeBaseById: async (id: string) => ({ id, type: 'document' }),
      rawKnowledgeBases: [], async ensureKnowledgeBases() {},
    }) },
    '@/stores/editorResources': { useEditorResourcesStore: () => ({ parserEngines: [], async ensureParserEngines() {} }) },
    '@/stores/uploadConfirm': { useUploadConfirmStore: () => ({}) },
    '@/stores/uploadTasks': { useUploadTasksStore: () => ({}) },
    '@/hooks/useMarqueeSelect': { useMarqueeSelect: () => ({}) },
    '@/composables/useAssistantPageContext': { useAssistantPageContext() {} },
    '@/utils/kbPermission': { permissionCanEditKB: () => true, permissionCanManageKB: () => true },
    '@/utils/knowledgeDeletion': {}, '@/utils/knowledgeTrace': {},
    '@/utils/knowledgeProcessingStall': stall, '@/api/wiki': {}, '@/api/knowledge-base': {},
    '@/api/knowledge-base/index': {
      batchQueryKnowledge, batchReparseKnowledge,
      listKnowledgeFolders: async () => ({ data: { folders: [{ path: 'other', name: 'other' }] } }),
      listKnowledgeTags: async () => ({ data: { data: [], total: 0 } }),
    },
  })
  await flush()
  // 用已加载多页的状态开始测试，覆盖滚动加载后的列表。
  cards.value = initialRows.map(item => ({ ...item }))
  total.value = initialRows.length
  await flush()
  getKnowled.mock.resetCalls()
  const submit = async (ids: string[]) => {
    page.vm.selectedIds = new Set(ids)
    page.vm.batchMode = true
    await page.vm.confirmBatchReparse()
    await flush()
  }
  const respond = async (index: number, data: Row[]) => {
    assert.ok(queries[index], '应存在序号为 ' + index + ' 的状态查询')
    queries[index].result.resolve({ success: true, data })
    await flush()
  }
  return {
    ...f, ...page, route, hook, queries, submissions, messages, batchQueryKnowledge, batchReparseKnowledge,
    submit, respond,
    setScope(kb: string, folder: string, data: Row[]) { scopes.set(kb + '/' + folder, data) },
    autoReply() { autoReply = true },
    holdSubmission() { holdSubmission = true },
  }
}

test('批量重建保留已加载的文档、页码、总数和加载状态，不重载列表', async t => {
  const f = await setup(t, Array.from({ length: 100 }, (_, index) => row('doc-' + index)))
  f.vm.page = 3
  f.vm.knowledgeScroll = { scrollTop: 2400 }
  const originalList = f.hook.cardList.value
  const loading: boolean[] = []
  const stop = vue.watch(() => f.vm.docListLoading, value => loading.push(value), { flush: 'sync' })
  t.after(stop)

  await f.submit(['doc-0', 'doc-99'])

  assert.equal(f.hook.getKnowled.mock.callCount(), 0, '批量重建不应重新请求第一页')
  assert.equal(f.hook.cardList.value, originalList)
  assert.equal(f.hook.cardList.value.length, 100)
  assert.equal(f.hook.total.value, 100)
  assert.equal(f.vm.page, 3)
  assert.equal(f.vm.knowledgeScroll.scrollTop, 2400)
  assert.equal(f.vm.docListLoading, false)
  assert.ok(!loading.includes(true), '后台状态轮询不应隐藏文档计数')
  assert.equal(f.hook.cardList.value[0].parse_status, 'pending')
  assert.equal(f.hook.cardList.value[99].parse_status, 'pending')
  assert.equal(f.hook.cardList.value[1].parse_status, 'completed')
  await advance(t, 1500)
  assert.deepEqual(f.queries[0].ids.sort(), ['doc-0', 'doc-99'])
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
})

test('状态请求未返回时，文档属性变化不会再发起一次查询', async t => {
  const f = await setup(t, [row('doc', 'processing')])
  await advance(t, 1500)
  assert.equal(f.queries.length, 1)
  f.hook.cardList.value[0].description = 'changed while the server is slow'
  await flush()
  await advance(t, 3000)
  assert.equal(f.queries.length, 1, '同一视图最多只能有一个尚未返回的状态请求')
  await f.respond(0, [row('doc', 'processing')])
  await advance(t, 1500)
  assert.equal(f.queries.length, 2, '当前请求结束后应继续正常轮询')
})

test('新增批次加入现有轮询，不与尚未返回的状态请求重叠', async t => {
  const f = await setup(t, [row('first', 'processing'), row('second')])
  await advance(t, 1500)
  assert.deepEqual(f.queries[0].ids, ['first'])
  await f.submit(['second'])
  await advance(t, 3000)
  assert.equal(f.queries.length, 1)
  await f.respond(0, [row('first', 'processing')])
  await advance(t, 1500)
  assert.equal(f.queries.length, 2)
  assert.deepEqual(f.queries[1].ids.sort(), ['first', 'second'])
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
})

for (const destination of ['folder', 'knowledge base'] as const) {
  test('切换' + (destination === 'folder' ? '目录' : '知识库') + '后，旧状态响应不能覆盖新视图或干扰其轮询', async t => {
    const f = await setup(t, [row('doc', 'processing', 'old view')])
    await advance(t, 1500)
    assert.equal(f.queries.length, 1)
    if (destination === 'folder') {
      f.setScope('kb', 'other', [row('doc', 'processing', 'new view')])
      f.vm.selectedFolderPath = 'other'
    } else {
      f.setScope('other-kb', '', [row('doc', 'processing', 'new view')])
      f.route.params.kbId = 'other-kb'
    }
    await flush()
    await advance(t, 1500)
    assert.equal(f.queries.length, 2, '新视图应能独立发起状态查询')
    await f.respond(0, [row('doc', 'completed', 'stale response')])
    assert.equal(f.hook.cardList.value[0].description, 'new view')
    assert.equal(f.hook.cardList.value[0].parse_status, 'processing')
    await advance(t, 3000)
    assert.equal(f.queries.length, 2, '旧请求返回后不能替换新视图中尚未结束的请求')
    await f.respond(1, [row('doc', 'completed', 'current response')])
    assert.equal(f.hook.cardList.value[0].description, 'current response')
  })
}

for (const settlesAs of ['success', 'failure'] as const) {
  test('卸载后忽略旧请求的' + (settlesAs === 'success' ? '成功响应' : '失败响应') + '，不再继续轮询', async t => {
    const f = await setup(t, [row('doc', 'processing', 'before unmount')])
    await advance(t, 1500)
    assert.equal(f.queries.length, 1)
    f.close()
    if (settlesAs === 'success') await f.respond(0, [row('doc', 'completed', 'late completion')])
    else {
      f.queries[0].result.reject(new Error('late network failure'))
      await flush()
    }
    assert.equal(f.hook.cardList.value[0].parse_status, 'processing')
    assert.equal(f.hook.cardList.value[0].description, 'before unmount')
    await advance(t, 15000)
    assert.equal(f.queries.length, 1)
  })
}

for (const destination of ['folder', 'knowledge base', 'unmount'] as const) {
  test(({ folder: '切换目录', 'knowledge base': '切换知识库', unmount: '卸载组件' }[destination]) + '后，旧批量提交响应不能修改当前列表', async t => {
    const f = await setup(t)
    f.holdSubmission()
    const submission = f.submit(['doc'])
    await flush()
    assert.equal(f.submissions.length, 1)
    if (destination === 'folder') {
      f.setScope('kb', 'other', [row('doc', 'completed', 'new view')])
      f.vm.selectedFolderPath = 'other'
    } else if (destination === 'knowledge base') {
      f.setScope('other-kb', '', [row('doc', 'completed', 'new view')])
      f.route.params.kbId = 'other-kb'
    } else {
      f.close()
    }
    await flush()
    const before = f.hook.cardList.value.map(item => ({ ...item }))
    f.hook.getKnowled.mock.resetCalls()
    f.submissions[0].result.resolve({ success: true })
    await submission
    assert.deepEqual(f.hook.cardList.value.map(item => ({ ...item })), before)
    assert.equal(f.hook.getKnowled.mock.callCount(), 0)
    await advance(t, 15000)
    assert.equal(f.queries.length, 0)
  })
}

test('服务器确认开始处理后解除临时待处理状态，完成后停止轮询', async t => {
  const f = await setup(t)
  await f.submit(['doc'])
  await advance(t, 1500)
  await f.respond(0, [row('doc', 'processing')])
  assert.equal(f.hook.cardList.value[0].parse_status, 'processing')
  await advance(t, 1500)
  await f.respond(1, [row('doc', 'completed', 'rebuilt')])
  assert.equal(f.hook.cardList.value[0].parse_status, 'completed')
  assert.equal(f.hook.cardList.value[0].description, 'rebuilt')
  const count = f.queries.length
  await advance(t, 15000)
  assert.equal(f.queries.length, count, '已完成文档的确认定时器到期后，不应重新启动轮询')
})

test('持续返回旧完成状态时，先保留待处理提示，等待到期后恢复服务器状态', async t => {
  const f = await setup(t)
  f.autoReply()
  await f.submit(['doc'])
  await advance(t, 6000)
  assert.equal(f.hook.cardList.value[0].parse_status, 'pending')
  await advance(t, 9000)
  assert.equal(f.hook.cardList.value[0].parse_status, 'completed')
  const count = f.queries.length
  await advance(t, 6000)
  assert.equal(f.queries.length, count, '确认等待结束后不应无限轮询')
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
})

test('第一批等待到期不会提前结束第二批的确认等待', async t => {
  const f = await setup(t, [row('first'), row('second')])
  f.autoReply()
  await f.submit(['first'])
  await advance(t, 6000)
  await f.submit(['second'])
  await advance(t, 8000)
  assert.equal(f.hook.cardList.value.find(item => item.id === 'first')?.parse_status, 'completed')
  assert.equal(f.hook.cardList.value.find(item => item.id === 'second')?.parse_status, 'pending')
  await advance(t, 7000)
  assert.equal(f.hook.cardList.value.find(item => item.id === 'second')?.parse_status, 'completed')
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
})

test('状态查询失败时保留文档，通过现有轮询重试', async t => {
  const f = await setup(t, [row('doc', 'processing')])
  await advance(t, 1500)
  f.queries[0].result.reject(new Error('temporary network failure'))
  await flush()
  assert.equal(f.hook.cardList.value.length, 1)
  assert.equal(f.hook.cardList.value[0].parse_status, 'processing')
  await advance(t, 1500)
  assert.equal(f.queries.length, 2)
  await f.respond(1, [row('doc', 'completed')])
  const count = f.queries.length
  await advance(t, 3000)
  assert.equal(f.queries.length, count)
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
})

test('移除状态响应中已消失的文档，但保留未参与查询的已完成文档', async t => {
  const f = await setup(t, [row('gone', 'processing'), row('active', 'processing'), row('untouched')])
  await advance(t, 1500)
  assert.deepEqual(f.queries[0].ids.sort(), ['active', 'gone'])
  await f.respond(0, [row('active', 'processing')])
  assert.deepEqual(f.hook.cardList.value.map(item => item.id), ['active', 'untouched'])
  assert.equal(f.hook.total.value, 2)
  await advance(t, 1500)
  assert.deepEqual(f.queries[1].ids, ['active'])
  await f.respond(1, [row('active', 'completed')])
  const count = f.queries.length
  await advance(t, 3000)
  assert.equal(f.queries.length, count)
})


for (const oldResponse of ['processing', 'missing'] as const) {
  test('重建前发出的查询不能确认新批次或删除其文档：' + (oldResponse === 'processing' ? '旧响应为处理中' : '旧响应不含文档'), async t => {
    // 文档已解析完成但摘要仍待处理时，会继续轮询，也允许重新构建。
    // 先挂起这次查询，再提交重建，确保旧响应不能被当作新一轮的处理结果。
    const f = await setup(t, [{ ...row('doc'), summary_status: 'pending' }])
    await advance(t, 1500)
    assert.equal(f.queries.length, 1)
    await f.submit(['doc'])
    await f.respond(0, oldResponse === 'processing' ? [row('doc', 'processing', 'previous parse')] : [])
    assert.equal(f.hook.cardList.value.length, 1, '重建前的快照不能删除刚提交重建的文档')
    assert.equal(f.hook.total.value, 1)
    assert.equal(f.hook.cardList.value[0].parse_status, 'pending')
    assert.equal(f.hook.cardList.value[0].description, '')
    await advance(t, 1500)
    assert.equal(f.queries.length, 2)
    await f.respond(1, [row('doc', 'completed')])
    assert.equal(f.hook.cardList.value[0].parse_status, 'pending',
      '旧查询中的处理中状态不能结束新批次的确认等待')
  })
}

test('切目录后允许新提交，旧请求的 finally 不能解除新提交的忙碌状态', async t => {
  const f = await setup(t)
  f.holdSubmission()
  const oldSubmission = f.submit(['doc'])
  await flush()
  assert.equal(f.submissions.length, 1)
  f.setScope('kb', 'other', [row('new-doc')])
  f.vm.selectedFolderPath = 'other'
  await flush()
  const newSubmission = f.submit(['new-doc'])
  await flush()
  assert.equal(f.submissions.length, 2, '新目录的提交不应被旧目录尚未返回的 POST 阻塞')
  assert.equal(f.vm.batchReparsing, true)

  f.submissions[0].result.resolve({ success: true })
  await oldSubmission
  assert.equal(f.vm.batchReparsing, true, '旧请求的 finally 不能清除新 POST 的忙碌状态')
  assert.equal(f.hook.cardList.value[0].id, 'new-doc')
  assert.equal(f.hook.cardList.value[0].parse_status, 'completed')
  await f.submit(['new-doc'])
  assert.equal(f.submissions.length, 2, '当前 POST 尚未结束时，应继续阻止第三次提交')

  f.submissions[1].result.resolve({ success: true })
  await newSubmission
  assert.equal(f.vm.batchReparsing, false)
  assert.equal(f.hook.cardList.value[0].parse_status, 'pending')
})


for (const oldResponse of ['completed', 'missing'] as const) {
  test('确认等待到期后，重建前的查询仍然无效：' + (oldResponse === 'completed' ? '旧响应为已完成' : '旧响应不含文档'), async t => {
    const f = await setup(t, [{ ...row('doc'), summary_status: 'pending' }])
    await advance(t, 1500)
    assert.equal(f.queries.length, 1)
    await f.submit(['doc'])
    // 让重建前的查询挂起到新批次的确认等待结束之后。
    // 等待到期只能解除临时提示，不能让旧响应重新变成有效结果。
    await advance(t, 15000)
    assert.equal(f.queries.length, 1, '旧请求挂起期间不应发起重叠查询')
    await f.respond(0, oldResponse === 'completed' ? [row('doc', 'completed', 'previous parse')] : [])
    assert.equal(f.hook.cardList.value.length, 1, '旧响应未包含文档时，也不能删除新一轮重建的文档')
    assert.equal(f.hook.total.value, 1)
    assert.equal(f.hook.cardList.value[0].parse_status, 'pending')
    assert.equal(f.hook.cardList.value[0].description, '')

    await advance(t, 1500)
    assert.equal(f.queries.length, 2, '丢弃旧响应后仍应继续轮询')
    await f.respond(1, [row('doc', 'completed', 'rebuilt')])
    assert.equal(f.hook.cardList.value[0].parse_status, 'completed')
    assert.equal(f.hook.cardList.value[0].description, 'rebuilt')
    await advance(t, 3000)
    assert.equal(f.queries.length, 2, '当前有效请求返回已完成后，应停止轮询')
  })
}


test('同视图刷新列表后继续状态轮询，并保留原确认等待期限', async t => {
  const f = await setup(t)
  f.autoReply()
  await f.submit(['doc'])
  await advance(t, 6000)
  assert.equal(f.hook.cardList.value[0].parse_status, 'pending')
  const beforeRefresh = f.hook.cardList.value
  const queriesBeforeRefresh = f.queries.length

  // 排序、筛选或上传都可能正常刷新列表。此时任务尚未开始，
  // 新数组里仍是旧的已完成状态，不能因此停止等待，也不能重新计时。
  await f.vm.loadKnowledgeFiles('kb')
  await flush()
  assert.notEqual(f.hook.cardList.value, beforeRefresh)
  assert.equal(f.hook.getKnowled.mock.callCount(), 1, '这里只允许本次主动刷新列表的请求')
  assert.equal(f.hook.cardList.value[0].parse_status, 'pending')
  await advance(t, 5900)
  assert.ok(f.queries.length > queriesBeforeRefresh, '替换列表快照后不应停止状态轮询')
  assert.equal(f.hook.cardList.value[0].parse_status, 'pending')

  f.setScope('kb', '', [row('doc', 'completed', 'fresh server result')])
  const queriesBeforeExpiry = f.queries.length
  await advance(t, 3100)
  assert.ok(f.queries.length > queriesBeforeExpiry, '原等待期限到达后，应发起新的状态查询')
  assert.equal(f.hook.cardList.value[0].parse_status, 'completed',
    '第 6 秒刷新列表，不应把 12 秒确认等待从头计时')
  assert.equal(f.hook.cardList.value[0].description, 'fresh server result')
  assert.equal(f.hook.getKnowled.mock.callCount(), 1, '后台等待确认不应额外重载列表')
  const completedQueries = f.queries.length
  await advance(t, 3000)
  assert.equal(f.queries.length, completedQueries)
})


// 固定 UUID 便于复现，同时保留真实请求中每个 ID 的 36 字符长度。
const uuid = (index: number) => `00000000-0000-4000-8000-${(index + 1).toString(16).padStart(12, '0')}`
const uuidRows = (count: number, status = 'completed') =>
  Array.from({ length: count }, (_, index) => row(uuid(index), status))

function assertBoundedQueries(queries: Array<{ ids: string[] }>) {
  for (const { ids } of queries) {
    assert.ok(ids.length > 0 && ids.length <= 100, '每个状态查询必须包含 1 至 100 个文档 ID')
    assert.ok(ids.every(id => /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-8[0-9a-f]{3}-[0-9a-f]{12}$/.test(id)),
      '边界用例必须使用真实长度的 UUID')
    const query = new URLSearchParams([['kb_id', uuid(999)], ...ids.map(id => ['ids', id])])
    assert.ok(Buffer.byteLength(`GET /api/v1/knowledge/batch?${query} HTTP/1.1\r\n`) < 8192,
      '包含知识库 UUID 的状态请求行应小于 8 KB')
  }
}

test('200 个 UUID 分成两个串行查询，重建完成后保留列表和滚动位置', async t => {
  const initial = uuidRows(200)
  const f = await setup(t, initial)
  f.vm.page = 6
  f.vm.knowledgeScroll = { scrollTop: 8400 }
  await f.submit(initial.map(item => item.id))
  await advance(t, 1500)
  assert.equal(f.queries.length, 1)
  assert.deepEqual(f.queries[0].ids, initial.slice(0, 100).map(item => item.id))
  await advance(t, 3000)
  assert.equal(f.queries.length, 1, '第一批返回前不能发送第二批或另一轮轮询')
  await f.respond(0, initial.slice(0, 100).map(item => row(item.id, 'processing')))
  assert.equal(f.queries.length, 2)
  assert.equal(f.hook.cardList.value.length, 200, '第一批响应不能把第二批文档判为缺失')
  await advance(t, 3000)
  assert.equal(f.queries.length, 2, '第二批未返回时，整轮请求仍应保持占用')
  await f.respond(1, initial.slice(100).map(item => row(item.id, 'processing')))
  assert.deepEqual(f.queries.flatMap(query => query.ids), initial.map(item => item.id))
  await advance(t, 1500)
  await f.respond(2, initial.slice(0, 100))
  await f.respond(3, initial.slice(100))
  await advance(t, 15000)
  assert.equal(f.queries.length, 4, '所有文档完成后应停止轮询')
  assertBoundedQueries(f.queries)
  assert.ok(f.hook.cardList.value.every(item => item.parse_status === 'completed'))
  assert.equal(f.hook.cardList.value.length, 200)
  assert.equal(f.hook.total.value, 200)
  assert.equal(f.vm.page, 6)
  assert.equal(f.vm.knowledgeScroll.scrollTop, 8400)
  assert.equal(f.vm.docListLoading, false)
  assert.equal(f.hook.getKnowled.mock.callCount(), 0, '整个重建过程不能回到第一页整表刷新')
})

for (const failure of ['reject', 'unsuccessful'] as const) {
  for (const failedBatch of [0, 1]) {
    test(`第 ${failedBatch + 1} 个子批次${failure === 'reject' ? '网络失败' : '返回失败'}后保留文档，其他子批次正常处理并可恢复`, async t => {
      const initial = uuidRows(200, 'processing')
      const f = await setup(t, initial)
      await advance(t, 1500)
      for (let batch = 0; batch < 2; batch++) {
        assert.equal(f.queries.length, batch + 1)
        assert.equal(f.queries[batch].ids.length, 100)
        if (batch === failedBatch) {
          if (failure === 'reject') f.queries[batch].result.reject(new Error('临时网络故障'))
          else f.queries[batch].result.resolve({ success: false, data: [] })
          await flush()
        } else {
          // 成功子批次少返回一行：只允许删除这一行，失败子批次必须完整保留。
          await f.respond(batch, initial.slice(batch * 100 + 1, (batch + 1) * 100).map(item => row(item.id)))
        }
      }
      const failedIds = initial.slice(failedBatch * 100, (failedBatch + 1) * 100).map(item => item.id)
      assert.equal(f.hook.cardList.value.length, 199)
      assert.equal(f.hook.total.value, 199)
      assert.deepEqual(f.hook.cardList.value.filter(item => item.parse_status === 'processing').map(item => item.id), failedIds)
      await advance(t, 1500)
      assert.equal(f.queries.length, 3)
      assert.deepEqual(f.queries[2].ids, failedIds, '下一轮只重试仍待处理的文档')
      await f.respond(2, failedIds.map(id => row(id)))
      await advance(t, 3000)
      assert.equal(f.queries.length, 3)
      assertBoundedQueries(f.queries)
      assert.equal(f.hook.getKnowled.mock.callCount(), 0)
    })
  }
}

test('连续提交三批重建时，新文档加入下一轮分块查询，不重叠也不丢失', async t => {
  const initial = uuidRows(300)
  const f = await setup(t, initial)
  await f.submit(initial.slice(0, 100).map(item => item.id))
  await advance(t, 1500)
  await f.submit(initial.slice(100, 200).map(item => item.id))
  await f.submit(initial.slice(200).map(item => item.id))
  await advance(t, 3000)
  assert.equal(f.queries.length, 1)
  await f.respond(0, initial.slice(0, 100).map(item => row(item.id, 'processing')))
  assert.equal(f.hook.cardList.value.length, 300)
  assert.ok(f.hook.cardList.value.slice(100).every(item => item.parse_status === 'pending'))
  await advance(t, 1500)
  for (let batch = 0; batch < 3; batch++) {
    assert.equal(f.queries.length, batch + 2)
    assert.deepEqual(f.queries[batch + 1].ids, initial.slice(batch * 100, (batch + 1) * 100).map(item => item.id))
    await f.respond(batch + 1, initial.slice(batch * 100, (batch + 1) * 100).map(item => row(item.id, 'processing')))
  }
  assertBoundedQueries(f.queries)
  assert.equal(f.hook.cardList.value.length, 300)
  assert.ok(f.hook.cardList.value.every(item => item.parse_status === 'processing'))
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
})

for (const destination of ['folder', 'knowledge base', 'unmount'] as const) {
  for (const settlesAs of ['success', 'failure'] as const) {
    test(`分块查询中${{ folder: '切目录', 'knowledge base': '切知识库', unmount: '卸载' }[destination]}后，忽略旧批次${settlesAs === 'success' ? '成功' : '失败'}并停止后续请求`, async t => {
      const initial = uuidRows(200, 'processing')
      const f = await setup(t, initial)
      await advance(t, 1500)
      assert.equal(f.queries[0].ids.length, 100)
      if (destination === 'folder') {
        f.setScope('kb', 'other', [row(uuid(0), 'processing', '新目录')])
        f.vm.selectedFolderPath = 'other'
      } else if (destination === 'knowledge base') {
        f.setScope('other-kb', '', [row(uuid(0), 'processing', '新知识库')])
        f.route.params.kbId = 'other-kb'
      } else f.close()
      await flush()
      const currentRows = f.hook.cardList.value.map(item => ({ ...item }))
      await advance(t, 1500)
      const count = destination === 'unmount' ? 1 : 2
      assert.equal(f.queries.length, count)
      if (settlesAs === 'success') await f.respond(0, [])
      else {
        f.queries[0].result.reject(new Error('旧视图请求失败'))
        await flush()
      }
      assert.deepEqual(f.hook.cardList.value.map(item => ({ ...item })), currentRows)
      await advance(t, 3000)
      assert.equal(f.queries.length, count, '旧请求不能继续第二子批次，也不能解除新视图的请求锁')
      if (destination !== 'unmount') {
        await f.respond(1, [row(uuid(0), 'completed', '当前视图')])
        assert.equal(f.hook.cardList.value[0].description, '当前视图')
      }
      assertBoundedQueries(f.queries)
    })
  }
}

for (const oldResponse of ['completed', 'missing'] as const) {
  test('第二子批次挂起时重新构建，确认等待到期后仍丢弃旧' + (oldResponse === 'completed' ? '完成状态' : '缺失状态'), async t => {
    const initial = uuidRows(200).map(item => ({ ...item, summary_status: 'pending' }))
    const f = await setup(t, initial)
    await advance(t, 1500)
    await f.respond(0, initial.slice(0, 100).map(item => row(item.id)))
    assert.equal(f.queries.length, 2)
    const rebuiltId = uuid(100)
    await f.submit([rebuiltId])
    await advance(t, 15000)
    assert.equal(f.queries.length, 2)
    await f.respond(1, initial.slice(oldResponse === 'completed' ? 100 : 101).map(item => row(item.id)))
    assert.equal(f.hook.cardList.value.length, 200)
    assert.equal(f.hook.total.value, 200)
    assert.equal(f.hook.cardList.value.find(item => item.id === rebuiltId)?.parse_status, 'pending')
    assert.equal(f.hook.cardList.value.find(item => item.id === rebuiltId)?.description, '')
    await advance(t, 1500)
    assert.deepEqual(f.queries[2].ids, [rebuiltId])
    await f.respond(2, [row(rebuiltId, 'completed', '新一轮处理结果')])
    await advance(t, 3000)
    assert.equal(f.queries.length, 3)
    assertBoundedQueries(f.queries)
    assert.equal(f.hook.cardList.value.find(item => item.id === rebuiltId)?.description, '新一轮处理结果')
  })
}


test('201 个文档保留不足 100 个的尾批，响应不能修改其他子批次的文档', async t => {
  const initial = uuidRows(201, 'processing')
  const f = await setup(t, initial)
  await advance(t, 1500)
  await f.respond(0, [
    ...initial.slice(0, 100).map(item => row(item.id)),
    row(uuid(200), 'completed', '不属于当前子请求的结果'),
  ])
  assert.equal(f.hook.cardList.value[200].parse_status, 'processing')
  assert.equal(f.hook.cardList.value[200].description, 'original')
  assert.equal(f.queries[1].ids.length, 100)
  await f.respond(1, initial.slice(100, 200).map(item => row(item.id)))
  assert.deepEqual(f.queries[2].ids, [uuid(200)])
  await f.respond(2, [row(uuid(200))])
  assert.deepEqual(f.queries.flatMap(query => query.ids), initial.map(item => item.id))
  await advance(t, 3000)
  assert.equal(f.queries.length, 3)
  assert.equal(f.hook.total.value, 201)
  assertBoundedQueries(f.queries)
})

test('第二子请求发出前已提交的重建，应使用该子请求开始时的版本确认状态', async t => {
  const initial = uuidRows(200).map(item => ({ ...item, summary_status: 'pending' }))
  const f = await setup(t, initial)
  await advance(t, 1500)
  await f.submit([uuid(100)])
  await f.respond(0, initial.slice(0, 100).map(item => row(item.id)))
  assert.equal(f.queries.length, 2)
  await f.respond(1, initial.slice(100).map(item => row(item.id, item.id === uuid(100) ? 'processing' : 'completed')))
  assert.equal(f.hook.cardList.value[100].parse_status, 'processing', '此响应来自重建提交后的查询，应接受处理状态')
  await advance(t, 1500)
  assert.deepEqual(f.queries[2].ids, [uuid(100)])
  await f.respond(2, [row(uuid(100))])
  assert.equal(f.hook.cardList.value[100].parse_status, 'completed', '已经确认处理状态，不应再等临时提示到期')
  assertBoundedQueries(f.queries)
})

for (const destination of ['folder', 'knowledge base'] as const) {
  test('第二子批次返回前切换' + (destination === 'folder' ? '目录' : '知识库') + '，不会回写旧状态或继续第三批', async t => {
    const initial = uuidRows(201, 'processing')
    const f = await setup(t, initial)
    await advance(t, 1500)
    await f.respond(0, initial.slice(0, 100).map(item => row(item.id)))
    assert.equal(f.queries.length, 2)
    const newRow = row(uuid(100), 'processing', '新视图')
    if (destination === 'folder') {
      f.setScope('kb', 'other', [newRow])
      f.vm.selectedFolderPath = 'other'
    } else {
      f.setScope('other-kb', '', [newRow])
      f.route.params.kbId = 'other-kb'
    }
    await flush()
    await advance(t, 1500)
    assert.equal(f.queries.length, 3)
    await f.respond(1, [row(uuid(100), 'completed', '旧视图')])
    assert.equal(f.hook.cardList.value[0].description, '新视图')
    assert.equal(f.hook.cardList.value[0].parse_status, 'processing')
    await advance(t, 3000)
    assert.equal(f.queries.length, 3, '不能继续旧视图的尾批，也不能解开新视图的请求锁')
    await f.respond(2, [row(uuid(100), 'completed', '新视图已完成')])
    assert.equal(f.hook.cardList.value[0].description, '新视图已完成')
    assertBoundedQueries(f.queries)
  })
}
