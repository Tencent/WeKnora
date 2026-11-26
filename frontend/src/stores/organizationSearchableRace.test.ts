import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import * as pinia from 'pinia'
import * as vue from 'vue'
import type { ApiResponse, SearchableOrganizationItem } from '../api/organization'
import { fixture } from '../test-utils/vueFixture.ts'
import * as localizedResourceCache from './localizedResourceCache.ts'
import type { useOrganizationStore } from './organization'
import * as organizationState from './organizationState.ts'
import * as versionedRequest from './versionedRequest.ts'

type SearchResponse = ApiResponse<{ data: SearchableOrganizationItem[]; total: number }>

function results(...ids: string[]): SearchResponse {
  return {
    success: true,
    data: {
      total: ids.length,
      data: ids.map(id => ({
        id,
        name: id,
        description: '',
        member_count: 1,
        member_limit: 0,
        share_count: 0,
        is_already_member: false,
        require_approval: false,
      })),
    },
  }
}

function setup(t: TestContext) {
  const f = fixture(t)
  const requests: Array<{ query: string; resolve: (response: SearchResponse) => void }> = []
  const { useOrganizationStore: createOrganizationStore } = f.evaluate(
    readFileSync(new URL('./organization.ts', import.meta.url), 'utf8'),
    {
      pinia, vue,
      '@/api/organization': {
        searchSearchableOrganizations: (query: string) => new Promise<SearchResponse>(resolve => {
          requests.push({ query, resolve })
        }),
      },
      '@/utils/request': { getCurrentLanguage: () => 'en-US' },
      './versionedRequest': versionedRequest,
      './localizedResourceCache': localizedResourceCache,
      './organizationState': organizationState,
    },
  ) as { useOrganizationStore: typeof useOrganizationStore }
  const root = pinia.createPinia()
  t.after(() => pinia.disposePinia(root))
  const store = createOrganizationStore(root)
  const ids = () => Array.from(store.searchableOrganizations, item => item.id)

  function start(query: string, options?: { force?: boolean }) {
    const requestCount = requests.length
    const finished = store.fetchSearchableOrganizations(query, options)
    assert.equal(requests.length, requestCount + 1)
    assert.equal(requests[requestCount].query, query.trim())
    return async (response: SearchResponse) => {
      requests[requestCount].resolve(response)
      await finished
    }
  }

  async function readCached(query: string) {
    const requestCount = requests.length
    const finished = store.fetchSearchableOrganizations(query)
    assert.equal(requests.length, requestCount)
    await finished
  }

  return { store, ids, start, readCached }
}

for (const outcome of ['success', 'failure'] as const) {
  test(`an older ${outcome} cannot change the result of restarting the same query`, async t => {
    const { ids, start, readCached } = setup(t)
    const oldAlpha = start('alpha')
    const beta = start('beta')
    await start(' alpha ')(results('current-alpha'))
    await beta(results('old-beta'))
    await oldAlpha(outcome === 'success' ? results('old-alpha') : { success: false })

    assert.deepEqual(ids(), ['current-alpha'])
    await readCached('alpha')
    assert.deepEqual(ids(), ['current-alpha'])
  })
}

test('a superseded query cannot populate its cache entry', async t => {
  const { ids, start } = setup(t)
  const alpha = start('alpha')
  await start('beta')(results('current-beta'))
  await alpha(results('old-alpha'))
  assert.deepEqual(ids(), ['current-beta'])

  await start('alpha')(results('current-alpha'))
  assert.deepEqual(ids(), ['current-alpha'])
})

test('a cache hit supersedes an in-flight forced refresh of the same query', async t => {
  const { ids, start, readCached } = setup(t)
  await start('alpha')(results('cached-alpha'))
  const refresh = start('alpha', { force: true })
  await readCached('alpha')
  await refresh(results('old-refresh'))

  assert.deepEqual(ids(), ['cached-alpha'])
  await readCached('alpha')
  assert.deepEqual(ids(), ['cached-alpha'])
})

test('clearing results invalidates a pending empty query and preserves settled cache entries', async t => {
  const { store, ids, start, readCached } = setup(t)
  await start('alpha')(results('cached-alpha'))
  const initial = start('')
  store.clearSearchableOrganizations()
  await initial(results('old-initial'))
  assert.deepEqual(ids(), [])

  await readCached('alpha')
  assert.deepEqual(ids(), ['cached-alpha'])
  await start('')(results('new-initial'))
  assert.deepEqual(ids(), ['new-initial'])
})

test('invalidating results prevents a late search from restoring an excluded organization or its cache', async t => {
  const { store, ids, start } = setup(t)
  await start('alpha')(results('joined', 'still-joinable'))
  const refresh = start('alpha', { force: true })
  store.invalidateOrganizationData({
    searchableOrganizations: true,
    excludeSearchableOrganizationId: 'joined',
  })
  assert.deepEqual(ids(), ['still-joinable'])
  await refresh(results('joined'))
  assert.deepEqual(ids(), ['still-joinable'])

  await start('alpha')(results('new-alpha'))
  assert.deepEqual(ids(), ['new-alpha'])
})

test('unsuccessful current response clears results without caching an empty list', async t => {
  const { ids, start } = setup(t)
  await start('alpha')(results('a1'))
  assert.deepEqual(ids(), ['a1'])

  await start('beta')({ success: false } as SearchResponse)
  assert.deepEqual(ids(), [])

  // Next lookup must hit the API again rather than reading a cached empty array.
  await start('beta')(results('b1'))
  assert.deepEqual(ids(), ['b1'])
})
