import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../test-utils/vueFixture.ts'
import { notificationPreview } from '../utils/notificationPreview.ts'

const notice = (id: string, read = false) => ({ id, kind: 'automation.succeeded', title: id, body: 'done',
  link: `/platform/automations/${id}`, read_at: read ? '2026-10-20T00:00:00Z' : null, created_at: '2026-10-20T00:00:00Z' })
const approval = { id: 'prompt', title: 'Approval', kind: 'tool_approval', link: '/platform/chat/session', created_at: '2026-10-20T00:00:00Z' }
const page = (items: ReturnType<typeof notice>[] = [], extra = {}) => ({ data: { items, pending: [approval],
  unread: items.filter(item => !item.read_at).length, read_count: items.filter(item => item.read_at).length, next_cursor: '', ...extra } })
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(r => { resolve = r })
  return { promise, resolve }
}
function setup(t: TestContext, api: Record<string, unknown>) {
  const f = fixture(t)
  const auth = vue.reactive({ user: { id: 'owner' }, effectiveTenantId: 1, pendingInvitationCount: 1 })
  const routes: string[] = []
  const errors: string[] = []
  const component = f.component(new URL('./GlobalInvitationBell.vue', import.meta.url), {
    vue: { ...vue, onMounted() {}, onBeforeUnmount() {} },
    'vue-router': { useRouter: () => ({ push: (path: string) => { routes.push(path) } }) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key, te: () => true, locale: vue.ref('en-US') }) },
    'tdesign-vue-next': { MessagePlugin: { error: (message: string) => { errors.push(message) } } },
    '@/stores/auth': { useAuthStore: () => auth }, '@/api/automation': api,
    '@/utils/notificationPreview': { notificationPreview },
  })
  return { ...component, auth, routes, errors }
}

test('marking and clearing history never removes pending actions or their badge', async t => {
  let history = [notice('one'), notice('two')]
  const marked: string[][] = []
  let cleared = 0
  const { vm, routes } = setup(t, {
    listNotifications: async () => page(history),
    markNotificationsRead: async (ids: string[] = []) => {
      marked.push(ids)
      history = history.map(item => !ids.length || ids.includes(item.id) ? notice(item.id, true) : item)
    },
    clearReadNotifications: async () => { cleared++; history = history.filter(item => !item.read_at) },
  })
  await vm.refresh()
  assert.equal(vm.badgeCount, 4)
  vm.openPending(approval)
  assert.equal(marked.length, 0)
  assert.equal(routes[0], '/platform/chat/session')
  await vm.markAllRead()
  assert.equal(vm.unread, 0)
  assert.equal(vm.readCount, 2)
  assert.equal(vm.badgeCount, 2, 'one invitation and one live approval remain')
  await vm.clearRead()
  assert.equal(cleared, 1)
  assert.equal(vm.items.length, 0)
  assert.equal(vm.pending.length, 1)
  assert.equal(vm.badgeCount, 2)
})

test('history retries its cursor and preserves reading position when new results arrive', async t => {
  let newer = false
  let failMore = true
  const cursors: string[] = []
  const { vm } = setup(t, {
    listNotifications: async (_limit: number, cursor = '') => {
      cursors.push(cursor)
      if (cursor) {
        if (failMore) { failMore = false; throw new Error('offline') }
        return page([notice('two'), notice('three')])
      }
      return page(newer ? [notice('new'), notice('one')] : [notice('one'), notice('two')], {
        next_cursor: 'older', pending: newer ? [] : [approval],
      })
    },
  })
  vm.panelVisible = true
  await flush()
  await vm.loadMore()
  assert.equal(vm.loadError, 'more')
  assert.equal(vm.nextCursor, 'older')
  await vm.retry()
  assert.equal(vm.items.map((item: any) => item.id).join(','), 'one,two,three')
  assert.equal(cursors.slice(-2).join(','), 'older,older')
  newer = true
  await vm.refresh()
  assert.equal(vm.pending.length, 0, 'resolved approvals disappear while browsing history')
  assert.equal(vm.hasNew, true)
  assert.equal(vm.items.map((item: any) => item.id).join(','), 'one,two,three')
  await vm.refresh(true)
  assert.equal(vm.items.map((item: any) => item.id).join(','), 'new,one')
  assert.equal(vm.hasNew, false)
  assert.equal(vm.nextCursor, 'older')
})

test('a response from a previous workspace cannot repopulate the notification panel', async t => {
  const first = deferred<ReturnType<typeof page>>()
  const second = deferred<ReturnType<typeof page>>()
  let calls = 0
  const { vm, auth } = setup(t, { listNotifications: () => ++calls === 1 ? first.promise : second.promise })
  const old = vm.refresh()
  auth.effectiveTenantId = 2
  await vue.nextTick()
  assert.equal(vm.items.length, 0)
  second.resolve(page([notice('workspace-two')], { pending: [] }))
  await flush()
  first.resolve(page([notice('workspace-one')]))
  await old
  assert.equal(vm.items.map((item: any) => item.id).join(','), 'workspace-two')
  assert.equal(vm.pending.length, 0)
})

test('failed read and clear requests preserve unread state and history', async t => {
  const { vm, errors } = setup(t, {
    listNotifications: async () => page([notice('unread'), notice('read', true)]),
    markNotificationsRead: async () => { throw new Error('offline') },
    clearReadNotifications: async () => { throw new Error('offline') },
  })
  await vm.refresh()
  await vm.open(vm.items[0])
  assert.equal(vm.unread, 1)
  assert.equal(vm.items[0].read_at, null)
  await vm.clearRead()
  assert.equal(vm.items.length, 2)
  assert.equal(errors.length, 2)
})

test('banners skip the initial backlog, deduplicate polls, and prioritize new approvals', async t => {
  let response = page([notice('old')])
  const { vm } = setup(t, { listNotifications: async () => response })
  await vm.refresh()
  assert.equal(vm.banners.length, 0)
  response = page([notice('new-1'), notice('new-2'), notice('new-3'), notice('old')], {
    pending: [approval, { ...approval, id: 'new-approval' }],
  })
  await vm.refresh()
  assert.deepEqual(Array.from(vm.banners, (banner: any) => banner.key),
    ['pending:new-approval', 'result:new-1', 'result:new-2'])
  assert.equal(vm.banners[0].type, 'pending')
  await vm.refresh()
  assert.equal(vm.banners.length, 3)
  const unread = vm.unread
  vm.dismissBanner('result:new-1')
  assert.equal(vm.unread, unread, 'dismissing a banner does not mark the notification read')
  vm.dismissBanner('pending:new-approval')
  assert.equal(vm.pending.length, 2, 'dismissing a banner never resolves an approval')
  await vm.refresh()
  assert.equal(vm.banners.length, 1, 'dismissed or overflow banners do not replay on the next poll')
})

test('banners expire after ten seconds and pause while being read', async t => {
  let response = page()
  const { vm } = setup(t, { listNotifications: async () => response })
  await vm.refresh()
  response = page([notice('new')])
  await vm.refresh()
  t.mock.timers.tick(9_000)
  assert.equal(vm.banners.length, 1)
  vm.setBannerHover(true)
  t.mock.timers.tick(20_000)
  assert.equal(vm.banners.length, 1)
  vm.setBannerFocus({ type: 'focusin' })
  vm.setBannerHover(false)
  t.mock.timers.tick(20_000)
  assert.equal(vm.banners.length, 1, 'keyboard focus keeps the banner visible after the pointer leaves')
  vm.setBannerFocus({ type: 'focusout', currentTarget: { contains: () => false }, relatedTarget: null })
  t.mock.timers.tick(10_000)
  assert.equal(vm.banners.length, 0)
  assert.equal(vm.unread, 1)
})

test('clicking a result banner opens and marks it read; an approval only opens its conversation', async t => {
  let response = page()
  const marked: string[][] = []
  const { vm, routes } = setup(t, {
    listNotifications: async () => response,
    markNotificationsRead: async (ids: string[]) => { marked.push(ids) },
  })
  await vm.refresh()
  response = page([notice('new')], { pending: [approval, { ...approval, id: 'new-approval' }] })
  await vm.refresh()
  vm.openBanner(vm.banners[0])
  assert.equal(routes.at(-1), approval.link)
  assert.equal(marked.length, 0)
  vm.openBanner(vm.banners[0])
  await flush()
  assert.equal(routes.at(-1), '/platform/automations/new')
  assert.deepEqual(marked.map(ids => Array.from(ids)), [['new']])
})

test('resolved approvals and read results disappear, and opening the inbox suppresses duplicate banners', async t => {
  let response = page()
  const { vm } = setup(t, { listNotifications: async () => response })
  await vm.refresh()
  response = page([notice('new')], { pending: [{ ...approval, id: 'new-approval' }] })
  await vm.refresh()
  assert.equal(vm.banners.length, 2)
  response = page([notice('new', true)], { pending: [] })
  await vm.refresh()
  assert.equal(vm.banners.length, 0)
  response = page([notice('another')])
  await vm.refresh()
  assert.equal(vm.banners.length, 1)
  vm.panelVisible = true
  await flush()
  assert.equal(vm.banners.length, 0)
  response = page([notice('while-open'), notice('another')])
  await vm.refresh()
  vm.panelVisible = false
  await vm.refresh()
  assert.equal(vm.banners.length, 0)
})

test('workspace switches reset banners, and older results revealed by clearing history are not new arrivals', async t => {
  let response = page([notice('initial')])
  const { vm, auth } = setup(t, { listNotifications: async () => response })
  await vm.refresh()
  response = page([notice('new')])
  await vm.refresh()
  assert.equal(vm.banners.length, 1)
  auth.effectiveTenantId = 2
  await flush()
  assert.equal(vm.banners.length, 0, 'the new workspace is seeded without replaying history')
  response = page([{ ...notice('older'), created_at: '2026-10-19T00:00:00Z' }])
  await vm.refresh()
  assert.equal(vm.banners.length, 0)
})
