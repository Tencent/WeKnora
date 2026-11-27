import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import * as pinia from 'pinia'
import * as vue from 'vue'
import type { ApiResponse, SearchableOrganizationItem } from '../../api/organization'
import * as localizedResourceCache from '../../stores/localizedResourceCache.ts'
import * as organizationState from '../../stores/organizationState.ts'
import * as versionedRequest from '../../stores/versionedRequest.ts'
import { fixture, flush } from '../../test-utils/vueFixture.ts'
import { shouldShowOrgRelationTag } from '../../utils/card-list-badge.ts'
import { matchesResourceQuery } from '../../utils/resourceListSearch.ts'
import * as resourceSorting from '../../utils/resourceSorting.ts'

const SEARCH_DELAY_MS = 300

type SearchResponse = ApiResponse<{ data: SearchableOrganizationItem[]; total: number }>
type SearchRequest = {
  query: string
  resolve: (response: SearchResponse) => void
  reject: (error: Error) => void
}

function setup(t: TestContext) {
  const f = fixture(t)
  const requests: SearchRequest[] = []
  const api = {
    listMyOrganizations: async () => ({ success: true, data: { organizations: [] } }),
    searchSearchableOrganizations: (query: string) => new Promise<SearchResponse>((resolve, reject) => {
      requests.push({ query, resolve, reject })
    }),
  }

  // Exercise the actual store so lifecycle tests can observe late writes to shared results.
  const { useOrganizationStore } = f.evaluate(
    readFileSync(new URL('../../stores/organization.ts', import.meta.url), 'utf8'),
    {
      pinia, vue,
      '@/api/organization': api,
      '@/utils/request': { getCurrentLanguage: () => 'en-US' },
      './versionedRequest': versionedRequest,
      './localizedResourceCache': localizedResourceCache,
      './organizationState': organizationState,
    },
  )
  const root = pinia.createPinia()
  const store = useOrganizationStore(root)
  const { vm, close } = f.component(new URL('./OrganizationList.vue', import.meta.url), {
    'vue-router': { useRoute: () => ({ path: '/organization', query: {} }), useRouter: () => ({ replace() {} }) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: {} },
    '@/stores/organization': { useOrganizationStore: () => store },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true, canAccessAllTenants: false }) },
    '@/components/settings/useConfirmDelete': { useConfirmDelete: () => () => undefined },
    '@/utils/clipboard': { copyWithToast: async () => undefined },
    '@/utils/resourceListSearch': { matchesResourceQuery },
    '@/utils/resourceSorting': resourceSorting,
    '@/utils/card-list-badge': { shouldShowOrgRelationTag },
    '@/api/organization': api,
  })
  t.after(() => pinia.disposePinia(root))

  vm.handleJoinOrganization()
  vm.joinStep = 'search'

  const input = (query: string) => {
    vm.searchQuery = query
    vm.doSearchSearchableDebounced()
  }
  const search = (query: string) => {
    vm.searchQuery = query
    void vm.doSearchSearchable()
    const request = requests.at(-1)!
    assert.equal(request.query, query)
    return request
  }
  const complete = async (request: SearchRequest) => {
    const result: SearchableOrganizationItem = {
      id: request.query,
      name: request.query,
      description: '',
      member_count: 1,
      member_limit: 0,
      share_count: 0,
      is_already_member: false,
      require_approval: false,
    }
    request.resolve({ success: true, data: { data: [result], total: 1 } })
    await flush()
  }
  const resultIds = () => Array.from(store.searchableOrganizations, (item: SearchableOrganizationItem) => item.id)
  return { vm, close, requests, input, search, complete, resultIds }
}

for (const outcome of ['response', 'rejection'] as const) {
  test(`an earlier ${outcome} cannot finish a newer search's loading state`, async t => {
    const f = setup(t)
    const old = f.search('alpha')
    const current = f.search('beta')
    assert.equal(f.vm.searchLoading, true)

    if (outcome === 'response') {
      await f.complete(old)
    } else {
      old.reject(new Error('older search failed'))
      await flush()
    }
    assert.equal(f.vm.searchLoading, true)

    await f.complete(current)
    assert.deepEqual(f.resultIds(), ['beta'])
    assert.equal(f.vm.searchLoading, false)
  })
}

test('an earlier rejection cannot erase the latest results', async t => {
  const f = setup(t)
  const old = f.search('alpha')
  await f.complete(f.search('beta'))
  old.reject(new Error('older search failed'))
  await flush()

  assert.deepEqual(f.resultIds(), ['beta'])
})

test('typing supersedes an in-flight search before the new debounce fires', async t => {
  const f = setup(t)
  const old = f.search('alpha')
  f.input('beta')
  await f.complete(old)

  assert.deepEqual(f.resultIds(), [])
  assert.equal(f.requests.length, 1)
  t.mock.timers.tick(SEARCH_DELAY_MS)
  assert.deepEqual(f.requests.map(request => request.query), ['alpha', 'beta'])
  await f.complete(f.requests[1])
  assert.deepEqual(f.resultIds(), ['beta'])
})

test('pressing Enter cancels the pending search for the same input', async t => {
  const f = setup(t)
  f.input('alpha')
  const current = f.search('alpha')
  t.mock.timers.tick(SEARCH_DELAY_MS)

  assert.equal(f.requests.length, 1)
  await f.complete(current)
  assert.deepEqual(f.resultIds(), ['alpha'])
})

type SearchFixture = ReturnType<typeof setup>
const searchExits: Array<{
  exit: string
  leaveSearch: (f: SearchFixture) => void
  restoreSearch?: (f: SearchFixture) => void
}> = [
  {
    exit: 'closing the join dialog',
    leaveSearch: f => { f.vm.showInvitePreview = false },
    restoreSearch: f => { f.vm.showInvitePreview = true },
  },
  {
    exit: 'switching to the invite step',
    leaveSearch: f => { f.vm.joinStep = 'invite' },
    restoreSearch: f => { f.vm.joinStep = 'search' },
  },
  {
    exit: 'unmounting the organization list',
    leaveSearch: f => f.close(),
  },
]

for (const { exit, leaveSearch, restoreSearch } of searchExits) {
  test(`${exit} cancels a pending search timer`, async t => {
    const f = setup(t)
    f.input('alpha')
    leaveSearch(f)
    // Returning in the same tick must not re-enable the old timer.
    restoreSearch?.(f)
    await flush()
    t.mock.timers.tick(SEARCH_DELAY_MS)

    assert.equal(f.requests.length, 0)
  })

  test(`${exit} invalidates an in-flight search`, async t => {
    const f = setup(t)
    const current = f.search('alpha')
    assert.equal(f.vm.searchLoading, true)
    leaveSearch(f)
    await flush()
    const loadingAfterExit = f.vm.searchLoading

    await f.complete(current)
    assert.deepEqual(f.resultIds(), [])
    assert.equal(loadingAfterExit, false)
  })
}

test('searchQuery input binds TDesign @change instead of native @input', () => {
  const sfc = readFileSync(new URL('./OrganizationList.vue', import.meta.url), 'utf8')
  const match = sfc.match(/<t-input[^>]*v-model="searchQuery"[^>]*>/)
  assert.ok(match, 'expected searchQuery t-input in OrganizationList.vue')
  assert.match(match[0], /@change="doSearchSearchableDebounced"/)
  assert.doesNotMatch(match[0], /@input="doSearchSearchableDebounced"/)
})
