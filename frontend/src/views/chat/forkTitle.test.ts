import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { createI18n } from 'vue-i18n'
import { LOCALE_BUNDLES } from '../../i18n/localeKeyAudit'

// As in sessionLoad.test.ts, exercise the page's actual handler with deferred transports.
const page = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')
const handler = page.slice(page.indexOf('async function handleFork('), page.indexOf('async function handleRewind('))
type Source = { id: string; title?: string } | null
function fixture(source: Source, locale = 'en-US', failRead = false) {
  const i18n = createI18n({ legacy: false, locale, messages: LOCALE_BUNDLES })
  const current = { value: source }
  const session = { value: 'original' }
  const requests: { message_id: string; title?: string }[] = []
  const rows: { title: string }[] = []
  const reads: string[] = []
  const paths: string[] = []
  let complete!: (value: unknown) => void
  const context = {
    currentSession: current, session_id: session, t: i18n.global.t,
    props: {}, composerLocked: { value: false }, messagesList: [{ id: 'message', role: 'assistant' }],
    forkSession: (_id: string, request: typeof requests[number]) => {
      requests.push(request)
      return new Promise(resolve => { complete = resolve })
    },
    getSession: async (id: string) => {
      reads.push(id)
      if (failRead) throw new Error('unavailable')
      return { data: { title: 'Server default' } }
    },
    stashForkLanding() {}, usemenuStore: { updataMenuChildren: (row: typeof rows[number]) => rows.push(row) },
    router: { push: async (path: string) => { paths.push(path) } },
    MessagePlugin: { error: assert.fail, warning: assert.fail },
  }
  const run = new Function(...Object.keys(context), 'let forkInFlight = false;\n' + handler + '\nreturn handleFork;')(...Object.values(context))
  return { run: () => run('message'), finish: () => complete({ data: { session_id: 'child' } }),
    current, session, i18n, requests, rows, reads, paths }
}

for (const [locale, title] of Object.entries({
  'en-US': 'Notes (branch)', 'zh-CN': 'Notes（分支）', 'zh-TW': 'Notes（分支）',
  'ja-JP': 'Notes（分岐）', 'ko-KR': 'Notes (분기)', 'ru-RU': 'Notes (ветка)',
})) {
  test(`fork uses the same ${locale} title in the POST and sidebar, even after switching chats or languages`, async () => {
    const f = fixture({ id: 'original', title: 'Notes' }, locale)
    const done = f.run()
    f.session.value = 'other'
    f.current.value = { id: 'other', title: 'Wrong chat' }
    f.i18n.global.locale.value = locale === 'en-US' ? 'zh-CN' : 'en-US'
    f.finish()
    await done
    assert.deepEqual(f.requests, [{ message_id: 'message', title }])
    assert.equal(f.rows[0].title, title)
    assert.deepEqual(f.reads, [])
  })
}

test('a missing, empty or stale source title leaves naming to the server', async () => {
  for (const source of [null, { id: 'original' }, { id: 'original', title: ' ' }, { id: 'other', title: 'Unrelated' }]) {
    const f = fixture(source)
    const done = f.run()
    f.finish()
    await done
    assert.deepEqual(f.requests, [{ message_id: 'message' }])
    assert.deepEqual(f.reads, ['child'])
    assert.equal(f.rows[0].title, 'Server default')
    assert.deepEqual(f.paths, ['/platform/chat/child'])
  }
})

test('long source titles leave room for the suffix without splitting Unicode characters', async () => {
  const f = fixture({ id: 'original', title: '🥭'.repeat(255) })
  const done = f.run()
  f.finish()
  await done
  const expected = '🥭'.repeat(255 - ' (branch)'.length) + ' (branch)'
  assert.equal(f.requests[0].title, expected)
  assert.equal(f.rows[0].title, expected)
})

test('a missing child title does not turn an already-created fork into a failed action', async () => {
  const f = fixture(null, 'en-US', true)
  const done = f.run()
  f.finish()
  await done
  assert.deepEqual(f.requests, [{ message_id: 'message' }])
  assert.equal(f.rows[0].title, 'New Chat')
  assert.deepEqual(f.paths, ['/platform/chat/child'])
})
