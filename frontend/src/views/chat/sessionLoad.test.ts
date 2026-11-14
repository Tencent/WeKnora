import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { computed, ref } from 'vue'
import { historyHasMoreAfterAttempt } from '../../utils/historyPagination'

// Exercise the page's actual load and route handlers with deferred transports.
const source = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')
function block(from: string, to: string) {
  const start = source.indexOf(from)
  const end = source.indexOf(to, start)
  assert(start >= 0 && end > start, `Missing load boundary: ${from}`)
  return source.slice(start, end)
}
function deferred<T = any>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => {
    resolve = yes
    reject = no
  })
  return { promise, resolve, reject }
}
const flush = () => new Promise<void>((resolve) => setImmediate(resolve))
function fixture(
  options: {
    session?: (id: string) => Promise<any>
    messages?: (query: any) => Promise<any>
    queue?: (id: string) => Promise<any>
    embedded?: boolean
    stream?: () => Promise<void>
  } = {},
) {
  let change!: (value: { chatid: string }[]) => Promise<void>
  let dispose!: () => void
  let leave!: (...args: any[]) => void
  let update!: (...args: any[]) => void
  const rows: any[] = [],
    hydrated: any[] = [],
    calls: any[] = [],
    streams: any[] = [],
    errors: any[] = [],
    sent: any[] = []
  const context: Record<string, any> = {
    ref,
    computed,
    historyHasMoreAfterAttempt,
    session_id: ref('A'),
    route: {},
    props: { embeddedMode: options.embedded ?? false },
    firstQuery: ref(''),
    messagesList: rows,
    steerQueue: ref([]),
    getSession:
      options.session ?? (async (id: string) => ({ data: { id, last_request_state: id } })),
    fetchMessageList: async (query: any) => {
      calls.push({ ...query })
      return options.messages ? options.messages(query) : { data: [] }
    },
    listSteerSession: options.queue ?? (async () => ({ items: [] })),
    useSettingsStoreInstance: {
      hydrateSessionInputState: (...args: any[]) => hydrated.push(args),
      restoreDefaultsIfSnapshotted() {},
    },
    cancelSuggestedQuestionsFetch() {},
    fetchSuggestedQuestionsIfNeeded() {},
    clearCitationChunkCache() {},
    console: { error: (...args: any[]) => errors.push(args), warn() {}, log() {} },
    watch: (_value: any, callback: typeof change) => {
      change = callback
    },
    onUnmounted: (callback: () => void) => {
      dispose = callback
    },
    onBeforeRouteLeave: (callback: typeof leave) => {
      leave = callback
    },
    onBeforeRouteUpdate: (callback: typeof update) => {
      update = callback
    },
    sessionActivity: { detach() {}, update() {} },
    activitySessionId: ref('A'),
    stopStream() {},
    referencesDrawer: { close() {} },
    fullContent: ref(''),
    recoverPollTimer: null,
    isImRecovering: ref(false),
    window: { removeEventListener() {} },
    SESSION_MUTATION_EVENT: 'mutation',
    backgroundPoller: { dispose() {} },
    clearComposerApproval() {},
    loadFollowUpSuggestions() {},
    findLastMessage: (predicate: (row: any) => boolean) => [...rows].reverse().find(predicate),
    persistedAssistantId: (row: any) => row.id,
    previewSteerMessage: (_rows: any[], item: any) => rows.push({ id: item.steer_id }),
    startStream: async (request: any) => {
      streams.push(request)
      await options.stream?.()
    },
    error: ref(null),
    isAttachingImStream: ref(false),
    composerLocked: ref(false),
    recordSend: (value: string) => sent.push(value),
  }
  for (const name of [
    'isFirstEnter',
    'scrollLock',
    'historyLoading',
    'historyLoadingMore',
    'hasMoreHistory',
    'loading',
    'isReplying',
    'userHasScrolledUp',
  ])
    context[name] = ref(name === 'hasMoreHistory')
  context.created_at = ref('')
  context.currentAssistantMessageId = ref('')
  context.limit = ref(20)
  let afterHistory!: () => Promise<void>
  context.handleMsgList = async (batch: any[]) => {
    rows.push(...batch)
    await afterHistory()
  }
  const after = block('    onAfterMsgList: async () => {', '    onAgentQuery: ')
    .replace('    onAfterMsgList: ', 'const afterHistory = ')
    .replace(/,\s*$/, ';\n')
  const code =
    block('const currentSession = ref(null);', 'const isAutomationSession = ') +
    block('const loadSessionAndHydrate = ', 'const inputFieldRef = ref();') +
    block('const hydrateSteerQueue = ', 'const {\n    findLastMessage,') +
    block('watch([() => route.params]', 'const scrollFollow = ') +
    block('const getmsgList = ', '// 发送消息') +
    after +
    block('const handleSessionMutation = ', 'onBeforeMount(') +
    block('const sendMsg = ', '    const reasoningEffort = ') +
    'recordSend(value); };\n' +
    block('const clearData = ', '</script>') +
    '\nreturn { currentSession, loadSessionAndHydrate, getmsgList, afterHistory, hydrateSteerQueue, handleSessionMutation, sendMsg, clearData };'
  const page = new Function(...Object.keys(context), code)(...Object.values(context))
  afterHistory = page.afterHistory
  return {
    page,
    context,
    rows,
    hydrated,
    calls,
    streams,
    errors,
    sent,
    change: (id: string) => change([{ chatid: id }]),
    dispose: () => dispose(),
    leave: () => leave({}, {}, () => {}),
    update: () => update({}, {}, () => {}),
  }
}

test('returning to A ignores detail and composer settings from its previous visit', async () => {
  const held = deferred()
  let first = true
  const f = fixture({
    session: async (id) => {
      if (id === 'A' && first) {
        first = false
        return held.promise
      }
      return { data: { id, last_request_state: id + '-current' } }
    },
  })
  const old = f.page.loadSessionAndHydrate('A')
  await f.change('B')
  await f.change('A')
  held.resolve({ data: { id: 'A', last_request_state: 'A-stale' } })
  await old
  assert.equal(f.page.currentSession.value.last_request_state, 'A-current')
  assert.equal(
    f.hydrated.some((args) => args[0] === 'A-stale'),
    false,
  )
})

test('returning to A ignores history and cursor from its previous visit', async () => {
  const held = deferred()
  let first = true
  const f = fixture({
    messages: async (query) => {
      if (query.session_id === 'A' && first) {
        first = false
        return held.promise
      }
      return { data: [{ id: query.session_id + '-current', created_at: 'current' }] }
    },
  })
  const old = f.page.getmsgList({ session_id: 'A' })
  await f.change('B')
  await flush()
  await f.change('A')
  await flush()
  held.resolve({ data: [{ id: 'A-stale', created_at: 'stale' }] })
  await old
  assert.deepEqual(
    f.rows.map((row) => row.id),
    ['A-current'],
  )
  assert.equal(f.context.created_at.value, 'current')
})

test('obsolete route hydration does not start another request for the current chat', async () => {
  const held = deferred()
  let first = true
  const f = fixture({
    session: async (id) => {
      if (id === 'B' && first) {
        first = false
        return held.promise
      }
      return { data: { id } }
    },
  })
  const old = f.change('B')
  await f.change('A')
  await flush()
  const before = f.calls.length
  held.resolve({ data: { id: 'B' } })
  await old
  await flush()
  assert.equal(f.calls.length, before)
})

test('an old history completion cannot release the new visit loading flags', async () => {
  const oldPage = deferred(),
    currentPage = deferred()
  const f = fixture({
    messages: async (query) => (query.session_id === 'A' ? oldPage.promise : currentPage.promise),
  })
  const old = f.page.getmsgList({ session_id: 'A' })
  await f.change('B')
  await flush()
  f.context.historyLoadingMore.value = true
  oldPage.resolve({ data: [] })
  await old
  assert.equal(f.context.historyLoading.value, true)
  assert.equal(f.context.historyLoadingMore.value, true)
  currentPage.resolve({ data: [] })
  await flush()
  assert.equal(f.context.historyLoading.value, false)
})

test('an obsolete failed page leaves the current visit and its pagination state alone', async () => {
  const held = deferred()
  const f = fixture({
    messages: async (query) => (query.session_id === 'A' ? held.promise : { data: [] }),
  })
  const old = f.page.getmsgList({ session_id: 'A' }, true)
  await f.change('B')
  await flush()
  f.context.historyLoading.value = true
  f.context.historyLoadingMore.value = true
  held.reject(new Error('Synthetic old failure'))
  await old
  assert.equal(f.context.historyLoading.value, true)
  assert.equal(f.context.historyLoadingMore.value, true)
  assert.equal(f.errors.length, 0)
})

test('leaving or disposing the page invalidates pending detail and history writes', async () => {
  for (const action of ['leave', 'dispose'] as const) {
    const detail = deferred(),
      history = deferred()
    const f = fixture({ session: () => detail.promise, messages: () => history.promise })
    const pending = [f.page.loadSessionAndHydrate('A'), f.page.getmsgList({ session_id: 'A' })]
    f[action]()
    detail.resolve({ data: { id: 'A', last_request_state: 'late' } })
    history.resolve({ data: [{ id: 'late', created_at: 'late' }] })
    await Promise.all(pending)
    assert.equal(f.page.currentSession.value, null, action)
    assert.deepEqual(f.rows, [], action)
    assert.deepEqual(f.hydrated, [], action)
  }
})

test('a stale queue restore cannot inject messages or resume a stream in another visit', async () => {
  const queue = deferred()
  const f = fixture({ queue: () => queue.promise })
  const old = f.page.getmsgList({ session_id: 'A' })
  // Trigger the real post-history callback with an incomplete assistant.
  f.rows.push({ id: 'A-assistant', role: 'assistant', is_completed: false })
  const after = f.page.afterHistory()
  await f.change('B')
  f.rows.push({ id: 'B-assistant', role: 'assistant', is_completed: false })
  queue.resolve({ items: [{ steer_id: 'old-steer', delivery: 'inject', content: 'old' }] })
  await Promise.all([old, after])
  await flush()
  assert.equal(
    f.rows.some((row) => row.id === 'old-steer'),
    false,
  )
  assert.deepEqual(f.streams, [])
  assert.deepEqual(f.context.steerQueue.value, [])
})

test('current failures preserve the history cursor and a later retry can succeed', async () => {
  let failing = true
  const f = fixture({
    messages: async () => {
      if (failing) throw new Error('Synthetic current failure')
      return { data: [{ id: 'older', created_at: 'older' }] }
    },
  })
  f.rows.push({ id: 'existing' })
  f.context.created_at.value = 'cursor'
  await f.page.getmsgList({ session_id: 'A', created_at: 'cursor' }, true)
  assert.equal(f.context.created_at.value, 'cursor')
  assert.equal(f.context.hasMoreHistory.value, true)
  assert.deepEqual(
    f.rows.map((row) => row.id),
    ['existing'],
  )
  failing = false
  await f.page.getmsgList({ session_id: 'A', created_at: 'cursor' }, true)
  assert.deepEqual(
    f.rows.map((row) => row.id),
    ['existing', 'older'],
  )
})

test('current detail retains a new-chat draft and embedded detail skips global settings', async () => {
  const f = fixture()
  f.context.firstQuery.value = 'draft'
  await f.page.loadSessionAndHydrate('A')
  assert.deepEqual(f.hydrated, [['A', true]])
  const embedded = fixture({ embedded: true })
  await embedded.page.loadSessionAndHydrate('A')
  await embedded.change('B')
  assert.deepEqual(embedded.hydrated, [])
  assert.deepEqual(embedded.calls, [])
})

test('a send waiting for old detail hydration cannot continue in the next visit', async () => {
  const held = deferred()
  let first = true
  const f = fixture({
    session: async (id) => {
      if (id === 'A' && first) {
        first = false
        return held.promise
      }
      return { data: { id } }
    },
  })
  const pending = f.page.sendMsg('A draft')
  await f.change('B')
  held.resolve({ data: { id: 'A' } })
  await pending
  assert.deepEqual(f.sent, [])
  await f.page.sendMsg('B draft')
  assert.deepEqual(f.sent, ['B draft'])
})

test('clearing messages invalidates the earlier history response', async () => {
  const held = deferred()
  const f = fixture({ messages: () => held.promise })
  f.context.historyLoading.value = true
  const old = f.page.getmsgList({ session_id: 'A' })
  f.page.handleSessionMutation({ detail: { sessionId: 'A', messagesCleared: true } })
  held.resolve({ data: [{ id: 'cleared', created_at: 'old' }] })
  await old
  assert.deepEqual(f.rows, [])
  assert.equal(f.context.historyLoading.value, false)
  assert.equal(f.context.hasMoreHistory.value, true)
})

test('late detail failures do not report errors against a later visit', async () => {
  const held = deferred()
  const f = fixture({ session: async (id) => (id === 'A' ? held.promise : { data: { id } }) })
  const old = f.page.loadSessionAndHydrate('A')
  await f.change('B')
  held.reject(new Error('Synthetic obsolete denial'))
  await old
  assert.equal(f.page.currentSession.value.id, 'B')
  assert.deepEqual(f.errors, [])
})

test('an obsolete explicit history request does not consume the current pagination lock', async () => {
  const f = fixture()
  await f.change('B')
  await flush()
  const count = f.calls.length
  await f.page.getmsgList({ session_id: 'A' }, true)
  assert.equal(f.calls.length, count)
  assert.equal(f.context.historyLoadingMore.value, false)
})

test('embedded disposal prevents its pending history from resuming a stream', async () => {
  const held = deferred()
  const f = fixture({ embedded: true, messages: () => held.promise })
  const old = f.page.getmsgList({ session_id: 'A' })
  f.dispose()
  held.resolve({
    data: [{ id: 'embedded-old', created_at: 'old', role: 'assistant', is_completed: false }],
  })
  await old
  assert.deepEqual(f.rows, [])
  assert.deepEqual(f.streams, [])
})

test('current history still restores the queue and resumes its incomplete reply', async () => {
  const f = fixture({
    messages: async () => ({
      data: [
        { id: 'reply-A', role: 'assistant', is_completed: false, channel: 'im', created_at: 'now' },
      ],
    }),
    queue: async () => ({
      items: [{ steer_id: 'queued-A', content: 'current', delivery: 'inject' }],
    }),
  })
  await f.page.getmsgList({ session_id: 'A' })
  assert.equal(f.context.steerQueue.value[0].steer_id, 'queued-A')
  assert.deepEqual(f.streams, [
    { session_id: 'A', query: 'reply-A', method: 'GET', url: '/api/v1/sessions/continue-stream' },
  ])
  assert.equal(f.context.isAttachingImStream.value, false)
})

test('old stream completion does not clear the next visit IM attach flag', async () => {
  const held = deferred()
  const f = fixture({
    stream: () => held.promise,
    messages: async (query) => ({
      data:
        query.session_id === 'A'
          ? [
              {
                id: 'reply-A',
                role: 'assistant',
                is_completed: false,
                channel: 'im',
                created_at: 'old',
              },
            ]
          : [],
    }),
  })
  const old = f.page.getmsgList({ session_id: 'A' })
  await flush()
  assert.equal(f.streams.length, 1)
  await f.change('B')
  await flush()
  f.context.isAttachingImStream.value = true
  held.resolve(undefined)
  await old
  assert.equal(f.context.isAttachingImStream.value, true)
})

test('an embedded view keeps its pending history when the host route changes', async () => {
  const held = deferred()
  const f = fixture({ embedded: true, messages: () => held.promise })
  const pending = f.page.getmsgList({ session_id: 'A' })
  f.update()
  f.leave()
  await f.change('B')
  held.resolve({ data: [{ id: 'embedded-current', created_at: 'now' }] })
  await pending
  assert.deepEqual(
    f.rows.map((row) => row.id),
    ['embedded-current'],
  )
})
