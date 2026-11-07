import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { fixture, flush } from '../../test-utils/vueFixture.ts'
import type { TenantInviteCandidate } from '../../api/organization'

const SEARCH_DELAY_MS = 300

type SearchRequest = {
  orgId: string
  query: string
  limit: number
  resolve: (response: { success: boolean; data?: TenantInviteCandidate[] }) => void
  reject: (error: Error) => void
}

function candidate(id: number, name = `workspace-${id}`): TenantInviteCandidate {
  return {
    tenant_id: id,
    tenant_name: name,
    representative_user_id: `user-${id}`,
    representative_username: `member-${id}`,
    representative_email: `member-${id}@example.test`,
  }
}

// 挂载真实组件脚本，让 Vue 自己执行 watch 和卸载钩子；只替换外围接口与展示依赖。
async function setup(t: TestContext) {
  const f = fixture(t)
  f.evaluate("document.body = { style: { overflow: '' } }", {})
  const requests: SearchRequest[] = []
  const invite = t.mock.fn(async (_orgId: string, _payload: unknown) => ({ success: true }))
  const fetchMembers = t.mock.fn(async (_orgId: string) => undefined)
  const messages = { success: t.mock.fn(), error: t.mock.fn(), warning: t.mock.fn() }
  t.mock.method(console, 'error', () => undefined)
  const organizationStore = {
    currentOrganization: { id: 'org-a', my_role: 'admin', is_owner: true },
    currentMembers: [],
    clearCurrentOrganizationContext() {},
    inviteOrganizationMember: invite,
    fetchMembers,
  }
  const { vm, close } = f.component(new URL('./OrganizationSettingsModal.vue', import.meta.url), {
    'vue-router': { useRouter: () => ({ push() {} }) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: messages },
    '@/stores/organization': { useOrganizationStore: () => organizationStore },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true, canAccessAllTenants: false }) },
    '@/composables/useModalShell': { useModalShell: () => ({ markClean() {}, requestClose() {} }) },
    '@/utils/clipboard': { copyWithToast: async () => undefined },
    '@/assets/img/agent.svg': { default: '' },
    '@/assets/img/agent-green.svg': { default: '' },
    '@/api/organization': {
      getOrganization: async () => ({ success: false }),
      listOrgShares: async () => ({ success: true, data: { shares: [] } }),
      listOrgAgentShares: async () => ({ success: true, data: { shares: [] } }),
      listJoinRequests: async () => ({ success: true, data: { requests: [] } }),
      searchTenantsForInvite: (orgId: string, query: string, limit: number) => new Promise((resolve, reject) => {
        requests.push({ orgId, query, limit, resolve, reject })
      }),
    },
  }, { visible: true, orgId: 'org-a', mode: 'edit' })
  vm.addMemberPopupVisible = true
  await flush()

  // 常规查询依次模拟即时 input-change 和防抖后的 search；两者间的竞态另有专门用例。
  const input = (query: string) => {
    vm.handleTenantInputChange(query)
    vm.handleTenantSearch(query)
  }
  const search = (query: string) => {
    input(query)
    t.mock.timers.tick(SEARCH_DELAY_MS)
    return requests.at(-1)!
  }
  const complete = async (request: SearchRequest, id: number, name?: string) => {
    request.resolve({ success: true, data: [candidate(id, name)] })
    await flush()
  }
  // 直接更新实例的响应式 props，效果与父组件更新一致，不手工调用被测 watch。
  const setProps = async (props: Record<string, unknown>) => {
    Object.assign(vm.$.props, props)
    await flush()
  }
  const ids = () => Array.from(vm.tenantSearchResults, (item: TenantInviteCandidate) => item.tenant_id)
  return { vm, close, requests, input, search, complete, setProps, ids, invite, fetchMembers, messages }
}

test('valid workspace IDs are trimmed and searched after 300 ms of quiet input', async t => {
  const f = await setup(t)
  f.input(' 101 ')
  t.mock.timers.tick(SEARCH_DELAY_MS - 1)
  assert.equal(f.requests.length, 0)
  f.input(' 202 ')
  t.mock.timers.tick(SEARCH_DELAY_MS - 1)
  assert.equal(f.requests.length, 0)
  t.mock.timers.tick(1)
  assert.equal(f.requests.length, 1)
  assert.equal(f.requests[0].orgId, 'org-a')
  assert.equal(f.requests[0].query, '202')
  assert.equal(f.requests[0].limit, 10)
  assert.equal(f.vm.tenantSearchLoading, true)
  await f.complete(f.requests[0], 202)
  assert.deepEqual(f.ids(), [202])
  assert.equal(f.vm.tenantSearchLoading, false)
})

for (const invalid of ['', '   ', '0', '01', '-1', '1.5', 'workspace', '1 2']) {
  test(`invalid workspace ID ${JSON.stringify(invalid)} clears results and ignores an earlier response`, async t => {
    const f = await setup(t)
    await f.complete(f.search('101'), 101)
    const pending = f.search('202')
    f.input(invalid)
    assert.deepEqual(f.ids(), [])
    await f.complete(pending, 202)
    assert.deepEqual(f.ids(), [])
    assert.equal(f.vm.tenantSearchLoading, false)
    t.mock.timers.tick(SEARCH_DELAY_MS)
    assert.equal(f.requests.length, 2)
  })
}

test('clearing input ends loading immediately without waiting for the stale request', async t => {
  const f = await setup(t)
  f.search('101')
  assert.equal(f.vm.tenantSearchLoading, true)
  f.input('')
  assert.equal(f.vm.tenantSearchLoading, false)
})

test('clearing input cancels the debounce timer', async t => {
  const f = await setup(t)
  f.input('101')
  t.mock.timers.tick(SEARCH_DELAY_MS - 1)
  f.input('')
  t.mock.timers.tick(SEARCH_DELAY_MS)
  assert.equal(f.requests.length, 0)
})

test('a late response cannot replace the newer search result', async t => {
  const f = await setup(t)
  const first = f.search('101')
  const second = f.search('202')
  await f.complete(second, 202)
  await f.complete(first, 101)
  assert.deepEqual(f.ids(), [202])
  assert.equal(f.vm.tenantSearchLoading, false)
})

test('changing input invalidates the old request before the next debounce expires', async t => {
  const f = await setup(t)
  const first = f.search('101')
  f.input('202')
  await f.complete(first, 101)
  assert.deepEqual(f.ids(), [])
  assert.equal(f.requests.length, 1)
  t.mock.timers.tick(SEARCH_DELAY_MS)
  await f.complete(f.requests[1], 202)
  assert.deepEqual(f.ids(), [202])
})

for (const outcome of ['success', 'failure'] as const) {
  test(`an older ${outcome} cannot clear the current request's loading state`, async t => {
    const f = await setup(t)
    const first = f.search('101')
    const second = f.search('202')
    if (outcome === 'success') first.resolve({ success: true, data: [candidate(101)] })
    else first.reject(new Error('old request failed'))
    await flush()
    assert.equal(f.vm.tenantSearchLoading, true)
    assert.deepEqual(f.ids(), [])
    await f.complete(second, 202)
    assert.deepEqual(f.ids(), [202])
    assert.equal(f.vm.tenantSearchLoading, false)
  })
}

test('an older failure cannot disturb a completed newer result', async t => {
  const f = await setup(t)
  const first = f.search('101')
  await f.complete(f.search('202'), 202)
  first.reject(new Error('old request failed'))
  await flush()
  assert.deepEqual(f.ids(), [202])
  assert.equal(f.vm.tenantSearchLoading, false)
})

test('clearing and searching the same ID again does not revive its first request', async t => {
  const f = await setup(t)
  const first = f.search('101')
  f.input('')
  const second = f.search('101')
  await f.complete(second, 101, 'current workspace')
  await f.complete(first, 101, 'stale workspace')
  assert.equal(f.vm.tenantSearchResults[0].tenant_name, 'current workspace')
})

for (const boundary of ['popup', 'outer modal', 'organization', 'unmount'] as const) {
  test(`${boundary} invalidates an in-flight search`, async t => {
    const f = await setup(t)
    const pending = f.search('101')
    if (boundary === 'popup') {
      f.vm.addMemberPopupVisible = false
      await flush()
    } else if (boundary === 'outer modal') {
      await f.setProps({ visible: false })
    } else if (boundary === 'organization') {
      await f.setProps({ orgId: 'org-b' })
    } else {
      f.close()
    }
    await f.complete(pending, 101)
    assert.deepEqual(f.ids(), [])
    assert.equal(f.vm.tenantSearchLoading, false)
  })

  test(`${boundary} cancels a search that is still debouncing`, async t => {
    const f = await setup(t)
    f.input('101')
    if (boundary === 'popup') {
      f.vm.addMemberPopupVisible = false
      await flush()
    } else if (boundary === 'outer modal') {
      await f.setProps({ visible: false })
    } else if (boundary === 'organization') {
      await f.setProps({ orgId: 'org-b' })
    } else {
      f.close()
    }
    t.mock.timers.tick(SEARCH_DELAY_MS)
    assert.equal(f.requests.length, 0)
  })
}

for (const boundary of ['popup', 'outer modal', 'organization'] as const) {
  test(`${boundary} returning to its original state cannot accept an earlier session's result`, async t => {
    const f = await setup(t)
    const first = f.search('101')
    if (boundary === 'popup') {
      f.vm.addMemberPopupVisible = false
      await flush()
      f.vm.addMemberPopupVisible = true
      await flush()
    } else if (boundary === 'outer modal') {
      await f.setProps({ visible: false })
      await f.setProps({ visible: true })
      f.vm.addMemberPopupVisible = true
      await flush()
    } else {
      await f.setProps({ orgId: 'org-b' })
      await f.setProps({ orgId: 'org-a' })
      f.vm.addMemberPopupVisible = true
      await flush()
    }
    const second = f.search('101')
    assert.equal(f.requests.length, 2)
    await f.complete(second, 101, 'new session')
    await f.complete(first, 101, 'old session')
    assert.equal(f.vm.tenantSearchResults[0].tenant_name, 'new session')
  })
}

test('a response from the previous organization cannot overwrite the new organization search', async t => {
  const f = await setup(t)
  const first = f.search('101')
  await f.setProps({ orgId: 'org-b' })
  f.vm.addMemberPopupVisible = true
  await flush()
  const second = f.search('202')
  assert.equal(second.orgId, 'org-b')
  await f.complete(second, 202)
  await f.complete(first, 101)
  assert.deepEqual(f.ids(), [202])
})

for (const outcome of ['rejection', 'unsuccessful response'] as const) {
  test(`the current search recovers after ${outcome}`, async t => {
    const f = await setup(t)
    const first = f.search('101')
    if (outcome === 'rejection') first.reject(new Error('temporary outage'))
    else first.resolve({ success: false })
    await flush()
    assert.equal(f.vm.tenantSearchLoading, false)
    assert.deepEqual(f.ids(), [])
    await f.complete(f.search('101'), 101)
    assert.deepEqual(f.ids(), [101])
    assert.equal(f.vm.tenantSearchLoading, false)
  })
}

test('successful invitation preserves the selected tenant, representative user and role', async t => {
  const f = await setup(t)
  await f.complete(f.search('202'), 202)
  f.vm.selectedTenantId = 202
  f.vm.addMemberRole = 'editor'
  const originalFetches = f.fetchMembers.mock.callCount()
  await f.vm.handleAddMember()
  await flush()
  assert.equal(f.invite.mock.callCount(), 1)
  assert.equal(f.invite.mock.calls[0].arguments[0], 'org-a')
  assert.deepEqual(JSON.parse(JSON.stringify(f.invite.mock.calls[0].arguments[1])), {
    tenant_id: 202,
    representative_user_id: 'user-202',
    role: 'editor',
  })
  assert.equal(f.fetchMembers.mock.callCount(), originalFetches + 1)
  assert.equal(f.vm.addMemberPopupVisible, false)
  assert.equal(f.vm.selectedTenantId, null)
  assert.equal(f.vm.addMemberRole, 'viewer')
  assert.deepEqual(f.ids(), [])
  assert.equal(f.messages.success.mock.callCount(), 1)
})

for (const settled of [false, true]) {
  test(`an unsuccessful older API response cannot disturb a ${settled ? 'completed' : 'pending'} newer search`, async t => {
    const f = await setup(t)
    const first = f.search('101')
    const second = f.search('202')
    if (settled) await f.complete(second, 202)
    // 实际接口会把网络失败转换为 success:false；拒绝 Promise 的测试另作防御性覆盖。
    first.resolve({ success: false })
    await flush()
    assert.equal(f.vm.tenantSearchLoading, !settled)
    assert.deepEqual(f.ids(), settled ? [202] : [])
    if (!settled) {
      await f.complete(second, 202)
      assert.deepEqual(f.ids(), [202])
    }
  })
}

for (const boundary of ['popup', 'outer modal', 'organization'] as const) {
  test(`${boundary} invalidation survives a close-and-reopen in the same Vue tick`, async t => {
    const f = await setup(t)
    const first = f.search('101')
    // 同一个更新周期内回到原值，仍然属于新会话，不能只比较当前 ID 或可见状态。
    if (boundary === 'popup') {
      f.vm.addMemberPopupVisible = false
      f.vm.addMemberPopupVisible = true
    } else if (boundary === 'outer modal') {
      f.vm.$.props.visible = false
      f.vm.$.props.visible = true
      f.vm.addMemberPopupVisible = true
    } else {
      f.vm.$.props.orgId = 'org-b'
      f.vm.$.props.orgId = 'org-a'
      f.vm.addMemberPopupVisible = true
    }
    await flush()
    await f.complete(first, 101, 'old session')
    assert.deepEqual(f.ids(), [])
    await f.complete(f.search('101'), 101, 'current session')
    assert.equal(f.vm.tenantSearchResults[0].tenant_name, 'current session')
  })

  test(`${boundary} clears the selected tenant and role before another invitation`, async t => {
    const f = await setup(t)
    await f.complete(f.search('101'), 101)
    f.vm.selectedTenantId = 101
    f.vm.addMemberRole = 'admin'
    if (boundary === 'popup') {
      f.vm.addMemberPopupVisible = false
      await flush()
    } else if (boundary === 'outer modal') {
      await f.setProps({ visible: false })
    } else {
      await f.setProps({ orgId: 'org-b' })
    }
    assert.deepEqual(f.ids(), [])
    assert.equal(f.vm.selectedTenantId, null)
    assert.equal(f.vm.addMemberRole, 'viewer')
    await f.vm.handleAddMember()
    assert.equal(f.invite.mock.callCount(), 0)
  })
}

// 输入组件还会将 search 延迟触发，下面故意让旧响应抢在该事件之前返回。
for (const query of ['', 'invalid', '202']) {
  test(`immediate input ${JSON.stringify(query)} invalidates a request before the select search event`, async t => {
    const f = await setup(t)
    const old = f.search('101')
    f.vm.handleTenantInputChange(query)
    await f.complete(old, 101)
    assert.deepEqual(f.ids(), [])
    assert.equal(f.vm.tenantSearchLoading, false)
    assert.equal(f.requests.length, 1)
    f.vm.handleTenantSearch(query)
    t.mock.timers.tick(SEARCH_DELAY_MS)
    assert.equal(f.requests.length, query === '202' ? 2 : 1)
    if (query === '202') await f.complete(f.requests[1], 202)
  })
}

test('immediate input removes settled candidates before the select search event', async t => {
  const f = await setup(t)
  await f.complete(f.search('101'), 101)
  f.vm.handleTenantInputChange('202')
  assert.deepEqual(f.ids(), [])
})

test('a delayed search for an earlier input cannot cancel the current pending query', async t => {
  const f = await setup(t)
  f.input('202')
  t.mock.timers.tick(SEARCH_DELAY_MS - 1)
  f.vm.handleTenantSearch('101')
  t.mock.timers.tick(1)
  assert.deepEqual(f.requests.map(request => request.query), ['202'])
  await f.complete(f.requests[0], 202)
  f.vm.handleTenantSearch('101')
  t.mock.timers.tick(SEARCH_DELAY_MS)
  assert.equal(f.requests.length, 1)
  assert.deepEqual(f.ids(), [202])
})

for (const boundary of ['popup', 'outer modal', 'organization', 'unmount'] as const) {
  test(`${boundary} rejects a select search event queued before it was reset`, async t => {
    const f = await setup(t)
    f.vm.handleTenantInputChange('101')
    if (boundary === 'popup') {
      f.vm.addMemberPopupVisible = false
      f.vm.addMemberPopupVisible = true
    } else if (boundary === 'outer modal') {
      f.vm.$.props.visible = false
      f.vm.$.props.visible = true
      f.vm.addMemberPopupVisible = true
    } else if (boundary === 'organization') {
      f.vm.$.props.orgId = 'org-b'
      f.vm.$.props.orgId = 'org-a'
      f.vm.addMemberPopupVisible = true
    } else {
      f.close()
    }
    f.vm.handleTenantSearch('101')
    t.mock.timers.tick(SEARCH_DELAY_MS)
    assert.equal(f.requests.length, 0)
    assert.deepEqual(f.ids(), [])
    assert.equal(f.vm.tenantSearchLoading, false)
  })
}


test('reopening the selected workspace dropdown preserves its representative user on submission', async t => {
  const f = await setup(t)
  await f.complete(f.search('202'), 202)
  f.vm.selectedTenantId = 202
  // TDesign 重开选项面板时会重置输入，但不会取消已经选中的空间。
  f.vm.handleTenantInputChange('')
  assert.deepEqual(f.ids(), [])
  assert.equal(f.vm.selectedTenantId, 202)
  await f.vm.handleAddMember()
  const payload = f.invite.mock.calls[0].arguments[1] as { representative_user_id?: string }
  assert.equal(payload.representative_user_id, 'user-202')
})

test('changing a selection after reopening the dropdown submits only the new representative', async t => {
  const f = await setup(t)
  await f.complete(f.search('101'), 101)
  f.vm.selectedTenantId = 101
  f.vm.handleTenantInputChange('')
  await f.complete(f.search('202'), 202)
  f.vm.selectedTenantId = 202
  f.vm.handleTenantInputChange('')
  await f.vm.handleAddMember()
  const payload = f.invite.mock.calls[0].arguments[1] as { tenant_id: number; representative_user_id?: string }
  assert.equal(payload.tenant_id, 202)
  assert.equal(payload.representative_user_id, 'user-202')
})

test('clearing the selection prevents submission until another workspace is selected', async t => {
  const f = await setup(t)
  await f.complete(f.search('101'), 101)
  f.vm.selectedTenantId = 101
  f.vm.selectedTenantId = undefined
  f.vm.handleTenantInputChange('')
  await f.vm.handleAddMember()
  assert.equal(f.invite.mock.callCount(), 0)
  await f.complete(f.search('202'), 202)
  f.vm.selectedTenantId = 202
  f.vm.handleTenantInputChange('')
  await f.vm.handleAddMember()
  const payload = f.invite.mock.calls[0].arguments[1] as { representative_user_id?: string }
  assert.equal(payload.representative_user_id, 'user-202')
})


test('searching the selected ID again refreshes its representative without a value change', async t => {
  const f = await setup(t)
  await f.complete(f.search('202'), 202)
  f.vm.selectedTenantId = 202
  f.vm.handleTenantInputChange('')
  const current = f.search('202')
  current.resolve({ success: true, data: [{ ...candidate(202), representative_user_id: 'new-user-202' }] })
  await flush()
  // 再次选中同一个 ID 不会触发 value 变化；最新有效结果也必须能更新提交信息。
  f.vm.selectedTenantId = 202
  f.vm.handleTenantInputChange('')
  await f.vm.handleAddMember()
  const payload = f.invite.mock.calls[0].arguments[1] as { representative_user_id?: string }
  assert.equal(payload.representative_user_id, 'new-user-202')
})

test('an older response cannot replace the selected candidate snapshot', async t => {
  const f = await setup(t)
  const old = f.search('202')
  await f.complete(f.search('202'), 202)
  f.vm.selectedTenantId = 202
  old.resolve({ success: true, data: [{ ...candidate(202), representative_user_id: 'old-user-202' }] })
  await flush()
  f.vm.handleTenantInputChange('')
  await f.vm.handleAddMember()
  const payload = f.invite.mock.calls[0].arguments[1] as { representative_user_id?: string }
  assert.equal(payload.representative_user_id, 'user-202')
})
