import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { reactive, ref } from 'vue'
import type { ListMembersParams, ListMembersResponse, TenantMember } from '../../api/tenant/members'
import { fixture, flush } from '../../test-utils/vueFixture.ts'

const SEARCH_DEBOUNCE_MS = 320
const GENERIC_ERROR = 'tenantMember.errors.generic'

type MemberRequest = {
  tenantId: number
  params: ListMembersParams
  resolve: (response: ListMembersResponse) => void
  reject: (reason: unknown) => void
}

/** 只替换接口边界；输入、分页和空间切换由组件的真实响应式逻辑处理。 */
async function membersFixture(t: TestContext, tenantId = 7, initialRole = 'owner') {
  const f = fixture(t)
  const requests: MemberRequest[] = []
  const invitationRequests: number[] = []
  const auth = reactive({
    currentTenantId: tenantId,
    currentTenantRole: initialRole,
    canAccessAllTenants: false,
    user: { id: 'current-user' },
  })
  const { vm, close } = f.component(new URL('./TenantMembers.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key, tm: () => ({}), locale: ref('zh-CN') }) },
    'tdesign-vue-next': { MessagePlugin: { success() {}, error: assert.fail } },
    '@/stores/auth': { useAuthStore: () => auth },
    '@/utils/clipboard': { copyWithToast: async () => {} },
    '@/utils/docsUrl': { docsUrl: () => '' },
    '@/i18n/auditActionRegistry': { AUDIT_ACTION_I18N_ROOTS: { tenantMember: '' } },
    '@/i18n/auditActionLabel': { auditActionLabel: () => '' },
    '@/api/tenant/members': {
      listMembers: (id: number, params: ListMembersParams) => new Promise<ListMembersResponse>((resolve, reject) => {
        requests.push({ tenantId: id, params: { ...params }, resolve, reject })
      }),
    },
    '@/api/tenant/invitations': {
      listTenantInvitations: async (id: number) => {
        invitationRequests.push(id)
        return { success: true, data: { invitations: [], total: 0 } }
      },
    },
    '@/api/tenant/audit-log': {},
  })
  await flush()
  return {
    vm, close, auth, requests, invitationRequests,
    async input(query: string) { vm.searchQuery = query; await flush() },
    async advance(ms = SEARCH_DEBOUNCE_MS) { t.mock.timers.tick(ms); await flush() },
    async search(query: string) {
      vm.searchQuery = query
      await flush()
      t.mock.timers.tick(SEARCH_DEBOUNCE_MS)
      await flush()
    },
    async page(page: number, pageSize = vm.membersPageSize) {
      vm.membersPage = page
      vm.membersPageSize = pageSize
      vm.onMembersPageChange()
      await flush()
    },
    async tenant(id: number) { auth.currentTenantId = id; await flush() },
  }
}

type MembersFixture = Awaited<ReturnType<typeof membersFixture>>

function member(userId: string, username = userId): TenantMember {
  return {
    user_id: userId, username, email: `${userId}@example.test`,
    role: 'viewer', status: 'active', joined_at: '2026-10-26T00:00:00Z',
  }
}

function response(rows: TenantMember[], total = rows.length, page = 1, pageSize = 20): ListMembersResponse {
  return { success: true, data: { members: rows, total, page, page_size: pageSize } }
}

async function resolve(request: MemberRequest, value: ListMembersResponse) {
  request.resolve(value)
  await flush()
}

async function reject(request: MemberRequest, message: string) {
  request.reject(new Error(message))
  await flush()
}

/** 跨 VM 比较只包含可见状态与审计姓名缓存，避免对象原型影响断言。 */
function state(f: MembersFixture) {
  return JSON.parse(JSON.stringify({
    members: f.vm.members, total: f.vm.membersTotal, page: f.vm.membersPage,
    pageSize: f.vm.membersPageSize, error: f.vm.error, loading: f.vm.loading,
    names: f.vm.memberDisplayByUserId,
  }))
}

async function ready(t: TestContext) {
  const f = await membersFixture(t)
  await resolve(f.requests[0], response([member('seed', '初始成员')], 120))
  return f
}

const staleOutcomes = ['success', 'failure', 'exception'] as const

async function settleStale(request: MemberRequest, outcome: typeof staleOutcomes[number]) {
  if (outcome === 'success') {
    await resolve(request, response([member('stale', '过期姓名')], 200, 4, 25))
  } else if (outcome === 'failure') {
    await resolve(request, { success: false, message: '过期业务错误' })
  } else {
    await reject(request, '过期网络错误')
  }
}

test('当前成功响应更新成员、分页元数据和审计姓名缓存', async t => {
  const f = await membersFixture(t)
  assert.equal(f.vm.loading, true)
  assert.deepEqual(f.requests[0].params, { page: 1, page_size: 20, q: undefined })
  await resolve(f.requests[0], response([member('alice', '当前姓名')], 81, 2, 50))
  assert.equal(f.vm.members[0].user_id, 'alice')
  assert.equal(f.vm.membersTotal, 81)
  assert.equal(f.vm.membersPage, 2)
  assert.equal(f.vm.membersPageSize, 50)
  assert.equal(f.vm.actorDisplayName('alice'), '当前姓名')
  assert.equal(f.vm.error, '')
  assert.equal(f.vm.loading, false)
})

test('连续输入只在最后一次输入满 320ms 后查询去空格的关键词', async t => {
  const f = await ready(t)
  await f.input('a')
  await f.advance(160)
  await f.input('  alice  ')
  await f.advance(319)
  assert.equal(f.requests.length, 1)
  await f.advance(1)
  assert.equal(f.requests.length, 2)
  assert.deepEqual(f.requests[1].params, { page: 1, page_size: 20, q: 'alice' })
})

test('当前网络失败保留已有成员并显示错误，重试清错后成功', async t => {
  const f = await ready(t)
  void f.vm.loadMembers()
  await reject(f.requests[1], '网络暂不可用')
  assert.equal(f.vm.error, '网络暂不可用')
  assert.equal(f.vm.loading, false)
  assert.equal(f.vm.members[0].user_id, 'seed')
  void f.vm.loadMembers()
  assert.equal(f.vm.error, '')
  assert.equal(f.vm.loading, true)
  await resolve(f.requests[2], response([member('recovered')]))
  assert.equal(f.vm.members[0].user_id, 'recovered')
  assert.equal(f.vm.loading, false)
})

test('当前业务失败与无 data 响应显示原消息或通用错误并结束 loading', async t => {
  const f = await membersFixture(t)
  await resolve(f.requests[0], { success: false, message: '查询失败' })
  assert.equal(f.vm.error, '查询失败')
  assert.equal(f.vm.loading, false)
  void f.vm.loadMembers()
  await resolve(f.requests[1], { success: true })
  assert.equal(f.vm.error, GENERIC_ERROR)
  assert.equal(f.vm.loading, false)
})

test('当前空结果正常清空成员和总数', async t => {
  const f = await ready(t)
  await f.search('missing')
  await resolve(f.requests[1], response([]))
  assert.equal(f.vm.members.length, 0)
  assert.equal(f.vm.membersTotal, 0)
  assert.equal(f.vm.membersPage, 1)
  assert.equal(f.vm.error, '')
  assert.equal(f.vm.loading, false)
})

for (const outcome of staleOutcomes) {
  test(`新搜索完成后旧 ${outcome} 响应不能覆盖状态和审计缓存`, async t => {
    const f = await ready(t)
    await f.search('old')
    await f.search('current')
    await resolve(f.requests[2], response([member('current', '最新姓名')], 81, 2, 50))
    const expected = state(f)
    await settleStale(f.requests[1], outcome)
    assert.deepEqual(state(f), expected)
    assert.equal(f.vm.actorDisplayName('stale'), 'stale')
    assert.equal(f.requests.length, 3)
  })

  test(`新搜索在途时旧 ${outcome} 的 finally 不能结束当前 loading`, async t => {
    const f = await ready(t)
    await f.search('old')
    await f.search('current')
    const expected = state(f)
    assert.equal(expected.loading, true)
    await settleStale(f.requests[1], outcome)
    assert.deepEqual(state(f), expected)
    await resolve(f.requests[2], response([member('current')]))
    assert.equal(f.vm.loading, false)
  })

  test(`输入已变化但新防抖尚未触发时旧 ${outcome} 必须失效`, async t => {
    const f = await ready(t)
    await f.search('old')
    await f.input('current')
    const expected = state(f)
    await settleStale(f.requests[1], outcome)
    assert.deepEqual(state(f), expected)
    assert.equal(f.requests.length, 2)
    await f.advance()
    assert.equal(f.requests[2].params.q, 'current')
  })
}

test('清空搜索回到第一页且请求不携带 q，旧搜索结果不能重新出现', async t => {
  const f = await ready(t)
  await f.search('alice')
  await f.page(3)
  await f.search('')
  assert.deepEqual(f.requests[3].params, { page: 1, page_size: 20, q: undefined })
  await resolve(f.requests[3], response([member('all')], 120))
  const expected = state(f)
  await resolve(f.requests[2], response([member('filtered')], 120, 3))
  await reject(f.requests[1], '已清空的搜索失败')
  assert.deepEqual(state(f), expected)
})

test('清空后重新输入同词不能复活此前同词请求', async t => {
  const f = await ready(t)
  await f.search('alice')
  await f.input('')
  await f.input('alice')
  await f.advance()
  assert.equal(f.requests.length, 3)
  assert.equal(f.requests[2].params.q, 'alice')
  await resolve(f.requests[2], response([member('fresh-alice')]))
  const expected = state(f)
  await resolve(f.requests[1], response([member('old-alice')]))
  assert.deepEqual(state(f), expected)
})

test('同 tick 搜索 A-B-A 也立即废弃旧 A 并只发一次新 A', async t => {
  const f = await ready(t)
  await f.search('alice')
  f.vm.searchQuery = 'bob'
  f.vm.searchQuery = 'alice'
  await flush()
  const expected = state(f)
  await resolve(f.requests[1], response([member('old-alice')]))
  assert.deepEqual(state(f), expected)
  await f.advance()
  assert.equal(f.requests.length, 3)
  assert.equal(f.requests[2].params.q, 'alice')
})

test('第 2、3 页乱序完成时保留第 3 页全部状态', async t => {
  const f = await ready(t)
  await f.page(2)
  await f.page(3)
  await resolve(f.requests[2], response([member('page-three')], 90, 3))
  const expected = state(f)
  await resolve(f.requests[1], response([member('page-two')], 80, 2))
  assert.deepEqual(state(f), expected)
  assert.equal(f.vm.actorDisplayName('page-two'), 'page-two')
})

test('page_size 切换后旧每页数量和成员不能覆盖新响应', async t => {
  const f = await ready(t)
  await f.page(2, 20)
  await f.page(1, 50)
  assert.deepEqual(f.requests[2].params, { page: 1, page_size: 50, q: undefined })
  await resolve(f.requests[2], response([member('fifty')], 100, 1, 50))
  const expected = state(f)
  await resolve(f.requests[1], response([member('twenty')], 80, 2, 20))
  assert.deepEqual(state(f), expected)
})

test('分页 v-model 先变化、change 尚未触发时旧成功和异常已失效', async t => {
  const f = await ready(t)
  await f.page(2)
  // TDesign 先写回 page-size，再在 nextTick 触发 change。
  f.vm.membersPageSize = 50
  const resizedState = state(f)
  await resolve(f.requests[1], response([], 1, 2, 20))
  assert.deepEqual(state(f), resizedState)
  assert.equal(f.requests.length, 2)
  f.vm.onMembersPageChange()
  await flush()
  assert.deepEqual(f.requests[2].params, { page: 2, page_size: 50, q: undefined })
  f.vm.membersPage = 3
  const changedPageState = state(f)
  await reject(f.requests[2], '旧分页异常')
  assert.deepEqual(state(f), changedPageState)
  assert.equal(f.requests.length, 3)
  f.vm.onMembersPageChange()
  await flush()
  assert.deepEqual(f.requests[3].params, { page: 3, page_size: 50, q: undefined })
  await resolve(f.requests[3], response([member('resized')], 300, 3, 100))
  assert.equal(f.vm.membersPageSize, 100)
  assert.equal(f.vm.loading, false)
})

test('旧越界响应不能回退当前页或发出额外请求', async t => {
  const f = await ready(t)
  await f.page(2)
  await f.page(3)
  await resolve(f.requests[1], response([], 1, 2))
  assert.equal(f.requests.length, 3)
  assert.equal(f.vm.membersPage, 3)
  assert.equal(f.vm.loading, true)
  await resolve(f.requests[2], response([member('page-three')], 100, 3))
  assert.equal(f.vm.members[0].user_id, 'page-three')
})

test('当前越界响应回退有效页并保持子请求 loading 到完成', async t => {
  const f = await ready(t)
  await f.page(4)
  await resolve(f.requests[1], response([], 25, 4))
  assert.equal(f.requests.length, 3)
  assert.deepEqual(f.requests[2].params, { page: 2, page_size: 20, q: undefined })
  assert.equal(f.vm.loading, true)
  await resolve(f.requests[2], response([member('last-page')], 25, 2))
  assert.equal(f.vm.membersPage, 2)
  assert.equal(f.vm.membersTotal, 25)
  assert.equal(f.vm.members[0].user_id, 'last-page')
  assert.equal(f.vm.loading, false)
})

test('回退子请求过期后父 finally 也不能结束后续请求的 loading', async t => {
  const f = await ready(t)
  await f.page(4)
  await resolve(f.requests[1], response([], 25, 4))
  await f.page(1)
  const expected = state(f)
  assert.equal(expected.loading, true)
  await resolve(f.requests[2], response([member('old-last-page')], 25, 2))
  assert.deepEqual(state(f), expected)
  await resolve(f.requests[3], response([member('new-first-page')], 25))
  assert.equal(f.vm.members[0].user_id, 'new-first-page')
  assert.equal(f.vm.loading, false)
})

test('防抖中分页立即应用最新搜索并回到第一页，不留下重复请求', async t => {
  const f = await ready(t)
  await f.input('  newest  ')
  await f.page(3)
  assert.deepEqual(f.requests[1].params, { page: 1, page_size: 20, q: 'newest' })
  assert.equal(f.vm.membersPage, 1)
  await f.advance()
  assert.equal(f.requests.length, 2)
})

test('同 tick 输入后立即重试使用最新关键词并取消待发防抖', async t => {
  const f = await ready(t)
  f.vm.membersPage = 3
  f.vm.searchQuery = '  retry-query  '
  void f.vm.loadMembers()
  await flush()
  assert.deepEqual(f.requests[1].params, { page: 1, page_size: 20, q: 'retry-query' })
  await f.advance()
  assert.equal(f.requests.length, 2)
})

test('切空间清除旧成员和审计姓名缓存，旧成功响应不能写回', async t => {
  const f = await ready(t)
  await f.search('old')
  await f.tenant(8)
  assert.equal(f.vm.members.length, 0)
  assert.equal(f.vm.actorDisplayName('seed'), 'seed')
  assert.equal(f.vm.searchQuery, '')
  assert.equal(f.vm.membersPage, 1)
  assert.equal(f.vm.membersPageSize, 20)
  const expected = state(f)
  await resolve(f.requests[1], response([member('old-space')]))
  assert.deepEqual(state(f), expected)
  await resolve(f.requests[2], response([member('new-space')]))
  assert.equal(f.vm.members[0].user_id, 'new-space')
})

test('切空间后旧错误和 finally 不影响新空间在途状态', async t => {
  const f = await ready(t)
  void f.vm.loadMembers()
  await f.tenant(8)
  const expected = state(f)
  await reject(f.requests[1], '旧空间网络错误')
  assert.deepEqual(state(f), expected)
  assert.equal(f.vm.loading, true)
})

test('切空间 A-B-A 后首次 A 与 B 的响应都失效', async t => {
  const f = await membersFixture(t)
  await f.tenant(8)
  await f.tenant(7)
  assert.deepEqual(f.requests.map(request => request.tenantId), [7, 8, 7])
  await resolve(f.requests[2], response([member('new-a')]))
  const expected = state(f)
  await resolve(f.requests[0], response([member('old-a')]))
  await resolve(f.requests[1], { success: false, message: '旧 B 错误' })
  assert.deepEqual(state(f), expected)
})

test('同 tick 切空间 A-B-A 也不能复用首次 A 的在途响应', async t => {
  const f = await membersFixture(t)
  f.auth.currentTenantId = 8
  f.auth.currentTenantId = 7
  await flush()
  const expected = state(f)
  await resolve(f.requests[0], response([member('old-a')]))
  assert.deepEqual(state(f), expected)
  assert.ok(f.requests.length > 1)
  assert.equal(f.requests.at(-1)?.tenantId, 7)
})

test('初始空间 0 不请求，空间解析成功后正常加载', async t => {
  const f = await membersFixture(t, 0)
  assert.equal(f.requests.length, 0)
  await f.tenant(7)
  assert.equal(f.requests.length, 1)
  assert.equal(f.requests[0].tenantId, 7)
  await resolve(f.requests[0], response([member('hydrated')]))
  assert.equal(f.vm.members[0].user_id, 'hydrated')
  assert.equal(f.vm.loading, false)
})

test('空间失效为 0 清空状态、取消搜索并忽略旧响应和错误', async t => {
  const f = await ready(t)
  void f.vm.loadMembers()
  void f.vm.loadMembers()
  await f.input('pending')
  await f.tenant(0)
  assert.equal(f.vm.members.length, 0)
  assert.equal(f.vm.membersTotal, 0)
  assert.equal(f.vm.error, '')
  assert.equal(f.vm.loading, false)
  assert.equal(f.vm.actorDisplayName('seed'), 'seed')
  const expected = state(f)
  await resolve(f.requests[1], response([member('old-space')]))
  await reject(f.requests[2], '空间已经失效')
  await f.advance()
  assert.deepEqual(state(f), expected)
  assert.equal(f.requests.length, 3)
})

test('切空间重置搜索且取消旧防抖，不额外发出重复成员请求', async t => {
  const f = await ready(t)
  await f.input('old-query')
  await f.tenant(8)
  assert.equal(f.requests.length, 2)
  assert.deepEqual(f.requests[1].params, { page: 1, page_size: 20, q: undefined })
  await f.advance()
  assert.equal(f.requests.length, 2)
})

test('卸载取消尚未发出的搜索定时器', async t => {
  const f = await ready(t)
  await f.input('pending')
  f.close()
  await f.advance()
  assert.equal(f.requests.length, 1)
})

test('卸载后在途成功不能修改成员、分页或审计缓存', async t => {
  const f = await ready(t)
  await f.search('pending')
  f.close()
  const expected = state(f)
  await resolve(f.requests[1], response([member('after-unmount')], 81, 2, 50))
  assert.deepEqual(state(f), expected)
})

test('卸载后在途异常与业务失败的 finally 不能继续写状态', async t => {
  const f = await ready(t)
  void f.vm.loadMembers()
  void f.vm.loadMembers()
  f.close()
  const expected = state(f)
  await reject(f.requests[1], '卸载后异常')
  await resolve(f.requests[2], { success: false, message: '卸载后业务失败' })
  assert.deepEqual(state(f), expected)
})

test('空间先解析、同 tick 再水合 owner 角色时成员和邀请各加载一次', async t => {
  const f = await membersFixture(t, 0, '')
  assert.equal(f.requests.length, 0)
  assert.equal(f.invitationRequests.length, 0)
  // 与 auth store 一致：先写当前空间，再写所属空间角色，中间不等待 tick。
  f.auth.currentTenantId = 7
  f.auth.currentTenantRole = 'owner'
  await flush()
  assert.equal(f.requests.length, 1)
  assert.equal(f.requests[0].tenantId, 7)
  assert.deepEqual(f.invitationRequests, [7])
})
