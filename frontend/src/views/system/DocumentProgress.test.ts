import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture, flush } from '../../test-utils/vueFixture.ts'

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}
const page = (id = '', total = id ? 1 : 0) => ({ data: { documents: id ? [{ id }] : [], total, page: 1, page_size: 25 } })
function dependencies(overrides: Record<string, unknown> = {}, messages: Array<{ kind: string; params?: object }> = []) {
  return {
    'vue-i18n': { useI18n: () => ({ t: (key: string, params?: object) => {
      if (params && (key.endsWith('.started') || key.endsWith('.startError'))) messages.push({ kind: key, params })
      return key
    } }) },
    'tdesign-vue-next': { MessagePlugin: { success() {}, error() {} } },
    '@/api/system/documentProgress': {
      listDocumentProgress: async () => page(),
      getDocumentProgress: async () => ({ data: { document: { id: 'doc' }, failures: [] } }),
      startDocumentWiki: async () => ({ data: { requested: 1, started: 1, already_queued: 0 } }),
      ...overrides,
    },
  }
}

test('stale list responses cannot overwrite newer document progress', async t => {
  const f = fixture(t)
  const first = deferred<ReturnType<typeof page>>()
  const second = deferred<ReturnType<typeof page>>()
  let calls = 0
  const { vm } = f.component(new URL('./DocumentProgress.vue', import.meta.url), dependencies({
    listDocumentProgress: () => (++calls === 1 ? first.promise : second.promise),
  }))
  const refresh = vm.reload()
  second.resolve(page('current'))
  await refresh
  first.resolve(page('stale'))
  await flush()
  assert.equal(vm.documents[0].id, 'current')
  assert.equal(vm.loading, false)
})

test('detail requests keep the latest document and ignore results after unmount', async t => {
  const f = fixture(t)
  const first = deferred<any>()
  const second = deferred<any>()
  const { vm, close } = f.component(new URL('./DocumentProgress.vue', import.meta.url), dependencies({
    getDocumentProgress: (id: string) => id === 'first' ? first.promise : second.promise,
  }))
  const old = vm.openDetail('first')
  const latest = vm.openDetail('second')
  second.resolve({ data: { document: { id: 'second' }, failures: [] } })
  await latest
  close()
  first.resolve({ data: { document: { id: 'first' }, failures: [] } })
  await old
  assert.equal(vm.detail.document.id, 'second')
})

test('Wiki starts validate counts, retain search scope and suppress duplicate clicks', async t => {
  const f = fixture(t)
  const accepted = deferred<any>()
  const writes: object[] = []
  const { vm } = f.component(new URL('./DocumentProgress.vue', import.meta.url), dependencies({
    startDocumentWiki: (body: object) => { writes.push(JSON.parse(JSON.stringify(body))); return accepted.promise },
  }))
  vm.count = 101
  await vm.startWiki()
  assert.equal(writes.length, 0)
  vm.count = 2
  vm.searchDraft = '  scoped document  '
  vm.applySearch()
  const start = vm.startWiki()
  await vm.startWiki()
  assert.deepEqual(writes, [{ count: 2, search: 'scoped document' }])
  accepted.resolve({ data: { requested: 2, started: 2, already_queued: 0 } })
  await start
  vm.status = 'failed'
  await vm.startWiki()
  assert.equal(writes.length, 1)
})

test('single-document starts send only the selected identity and count one', async t => {
  const f = fixture(t)
  const writes: object[] = []
  const { vm } = f.component(new URL('./DocumentProgress.vue', import.meta.url), dependencies({
    startDocumentWiki: async (body: object) => {
      writes.push(JSON.parse(JSON.stringify(body)))
      return { data: { requested: 1, started: 1, already_queued: 0 } }
    },
  }))
  vm.count = 25
  await vm.startWiki('selected-doc')
  assert.deepEqual(writes, [{ count: 1, knowledge_id: 'selected-doc' }])
})

test('partial queue failures report accepted work and refresh without automatic re-enqueue', async t => {
  const f = fixture(t)
  const messages: Array<{ kind: string; params?: object }> = []
  let writes = 0
  let reads = 0
  const { vm } = f.component(new URL('./DocumentProgress.vue', import.meta.url), dependencies({
    listDocumentProgress: async () => { reads++; return page() },
    startDocumentWiki: async () => { writes++; throw { data: { started: 1 } } },
  }, messages))
  vm.count = 3
  await vm.startWiki()
  assert.equal(writes, 1)
  assert.ok(reads >= 2)
  assert.equal(messages.at(-1)?.kind, 'documentProgress.startError')
  assert.equal((messages.at(-1)?.params as { count: number }).count, 1)
  assert.equal(vm.starting, false)
})

test('shrinking live results clamp pagination before displaying an empty last page', async t => {
  const f = fixture(t)
  const pages: number[] = []
  const { vm } = f.component(new URL('./DocumentProgress.vue', import.meta.url), dependencies({
    listDocumentProgress: async (query: { page: number }) => {
      pages.push(query.page)
      return query.page === 1 ? page('current') : page('', 1)
    },
  }))
  await flush()
  vm.page = 4
  await flush()
  await flush()
  assert.equal(vm.page, 1)
  assert.equal(vm.documents[0].id, 'current')
  assert.deepEqual(pages, [1, 4, 1])
})
