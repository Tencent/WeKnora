import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test, type TestContext } from 'node:test'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { ref } from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture.ts'
import type { ListAllUsersResponse, SystemAdminUser } from '@/api/system'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function makeUser(index: number, overrides: Partial<SystemAdminUser> = {}): SystemAdminUser {
  const padded = String(index).padStart(2, '0')
  return {
    id: `u-${padded}`,
    username: `user-${padded}`,
    email: `user-${padded}@example.com`,
    is_active: true,
    is_system_admin: index === 1,
    created_at: `2026-01-${padded}T00:00:00Z`,
    updated_at: `2026-01-${padded}T00:00:00Z`,
    ...overrides,
  }
}

function mountUserManagement(
  t: TestContext,
  getImpl: (url: string) => Promise<unknown>,
  activeProp = true,
) {
  const f = fixture(t)
  const systemApiSource = readFileSync(new URL('../../api/system/index.ts', import.meta.url), 'utf8')
  const systemApi = f.evaluate(`const { URLSearchParams } = require('node:url');\n${systemApiSource}`, {
    'node:url': { URLSearchParams },
    '@/utils/request': {
      get: getImpl,
      post: async () => ({}),
      put: async () => ({}),
      del: async () => ({}),
      patch: async () => ({}),
      postUpload: async () => ({}),
    },
  })
  return {
    f,
    ...f.component(
      new URL('./UserManagement.vue', import.meta.url),
      {
        'vue-i18n': {
          useI18n: () => ({
            t: (key: string) => key,
            locale: ref('en-US'),
          }),
        },
        'tdesign-vue-next': {
          MessagePlugin: { success() {}, error() {} },
        },
        '@/api/system': systemApi,
        '@/stores/auth': {
          useAuthStore: () => ({ currentUserId: 'u-01' }),
        },
      },
      { active: activeProp },
    ),
  }
}

test('UserManagement SFC template compiles cleanly against script bindings', () => {
  const source = readFileSync(new URL('./UserManagement.vue', import.meta.url), 'utf8')
  const { descriptor, errors } = parse(source, { filename: 'UserManagement.vue' })
  assert.deepEqual(errors, [])
  const script = compileScript(descriptor, { id: 'user-mgmt-template-check' })
  const template = compileTemplate({
    source: descriptor.template!.content,
    filename: 'UserManagement.vue',
    id: 'user-mgmt-template-check',
    compilerOptions: { bindingMetadata: script.bindings },
  })
  assert.deepEqual(template.errors, [])
  assert.equal('filteredUsers' in (script.bindings ?? {}), false)
})

test('searches across all pages via q query param, resets offset to 0, and clears immediately', async t => {
  const roster: SystemAdminUser[] = [
    ...Array.from({ length: 24 }, (_, i) => makeUser(i + 1)),
    makeUser(25, {
      id: 'u-25-legacy',
      username: 'founder_alice',
      email: 'alice@legacy.example',
      is_active: false,
    }),
  ]
  const requestedUrls: string[] = []

  const { vm } = mountUserManagement(t, async (url: string) => {
    requestedUrls.push(url)
    const parsed = new URL(url, 'http://localhost')
    assert.equal(parsed.pathname, '/api/v1/system/admin/users')
    const offset = Number(parsed.searchParams.get('offset') ?? '0')
    const limit = Number(parsed.searchParams.get('limit') ?? '50')
    const q = (parsed.searchParams.get('q') ?? '').toLowerCase()
    const matched = roster.filter(
      u => !q || u.username.toLowerCase().includes(q) || u.email.toLowerCase().includes(q),
    )
    return {
      users: matched.slice(offset, offset + limit),
      offset,
      limit,
      has_more: offset + limit < matched.length,
    } satisfies ListAllUsersResponse
  })

  await flush()
  assert.equal(requestedUrls.at(-1), '/api/v1/system/admin/users?offset=0&limit=20')
  assert.equal(vm.users.length, 20)
  assert.equal(vm.hasMore, true)
  assert.equal(vm.users.some((u: SystemAdminUser) => u.id === 'u-25-legacy'), false)

  // Move to page 2 first to verify a subsequent search resets offset back to 0.
  await vm.fetchPage(20, vm.appliedQuery)
  assert.equal(vm.offset, 20)
  assert.equal(vm.users.length, 5)

  // Type a search matching the 25th user; debounce should wait 250ms then query offset=0 with q.
  vm.search = '  alice@legacy  '
  await flush()
  assert.equal(vm.hasPendingSearch, true)
  const countBeforeDebounce = requestedUrls.length

  t.mock.timers.tick(249)
  await flush()
  assert.equal(requestedUrls.length, countBeforeDebounce, 'must not fire before debounce window')

  t.mock.timers.tick(1)
  await flush()
  assert.equal(
    requestedUrls.at(-1),
    '/api/v1/system/admin/users?q=alice%40legacy&offset=0&limit=20',
  )
  assert.equal(vm.offset, 0)
  assert.equal(vm.hasMore, false)
  assert.equal(vm.hasPendingSearch, false)
  assert.deepEqual(
    vm.users.map((u: SystemAdminUser) => u.id),
    ['u-25-legacy'],
  )

  // Clearing search skips debounce and omits empty `q` parameter.
  vm.search = ''
  await flush()
  await flush()
  assert.equal(requestedUrls.at(-1), '/api/v1/system/admin/users?offset=0&limit=20')
  assert.equal(vm.users.length, 20)
})

test('newer search supersedes in-flight request and ignores stale response', async t => {
  const initial = deferred<ListAllUsersResponse>()
  const staleSearch = deferred<ListAllUsersResponse>()
  const latestSearch = deferred<ListAllUsersResponse>()
  const seenUrls: string[] = []

  const { vm } = mountUserManagement(t, (url: string) => {
    seenUrls.push(url)
    if (url.includes('q=al&')) return staleSearch.promise
    if (url.includes('q=alice&')) return latestSearch.promise
    return initial.promise
  })

  initial.resolve({ users: [makeUser(1)], offset: 0, limit: 20, has_more: false })
  await flush()

  // Trigger first search ("al"), keep it in flight (`loading === true`).
  vm.search = 'al'
  await flush()
  t.mock.timers.tick(250)
  await flush()
  assert.equal(vm.loading, true)

  // User finishes typing "alice" and hits Enter while "al" is still loading.
  vm.search = 'alice'
  vm.applySearchNow()
  await flush()
  assert.ok(seenUrls.some(u => u.includes('q=alice&')), 'in-flight loading must not drop newer search')

  latestSearch.resolve({
    users: [makeUser(25, { id: 'u-alice', username: 'alice' })],
    offset: 0,
    limit: 20,
    has_more: false,
  })
  await flush()
  assert.equal(vm.loading, false)
  assert.deepEqual(vm.users.map((u: SystemAdminUser) => u.id), ['u-alice'])

  // Stale "al" response arrives afterwards and must be discarded.
  staleSearch.resolve({
    users: [makeUser(2, { id: 'u-stale', username: 'albert' })],
    offset: 0,
    limit: 20,
    has_more: true,
  })
  await flush()
  assert.deepEqual(vm.users.map((u: SystemAdminUser) => u.id), ['u-alice'])
  assert.equal(vm.hasMore, false)
})

test('clamps empty upper page, refreshes on user creation while dialog stays open, and guards open reset dialog', async t => {
  let totalRows = 21
  const calls: string[] = []

  const { vm } = mountUserManagement(t, async (url: string) => {
    calls.push(url)
    const parsed = new URL(url, 'http://localhost')
    const offset = Number(parsed.searchParams.get('offset') ?? '0')
    const limit = Number(parsed.searchParams.get('limit') ?? '20')
    const all = Array.from({ length: totalRows }, (_, i) => makeUser(i + 1))
    return {
      users: all.slice(offset, offset + limit),
      offset,
      limit,
      has_more: offset + limit < all.length,
    } satisfies ListAllUsersResponse
  })
  await flush()

  // Go to page 2 (1 item), then shrink dataset to 20 items and refresh -> clamps back to offset 0.
  await vm.fetchPage(20, '')
  assert.equal(vm.offset, 20)
  totalRows = 20
  vm.refreshUsers()
  await flush()
  await flush()
  assert.equal(vm.offset, 0)
  assert.equal(vm.users.length, 20)

  // CreateUserDialog emits @created while createVisible is still true (password reveal step).
  vm.createVisible = true
  const beforeCreateCalls = calls.length
  vm.onUserCreated()
  await flush()
  assert.equal(calls.length, beforeCreateCalls + 1)
  vm.createVisible = false

  // Opening in-row ResetPasswordDialog blocks background list replacement so the dialog row stays mounted.
  vm.resetVisibleId = 'u-01'
  const beforeResetCalls = calls.length
  await vm.fetchPage(0, 'other')
  assert.equal(calls.length, beforeResetCalls)
})
