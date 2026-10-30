import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import * as pinia from 'pinia'
import { fixture, flush } from '../test-utils/vueFixture.ts'
import * as knowledgeReadOptions from '../api/knowledge-base/knowledgeReadOptions.ts'

type Request = {
  url: string
  resolve: (response: unknown) => void
  reject: (error: unknown) => void
}

function setup(t: TestContext) {
  const f = fixture(t)
  const metadata: Request[] = []
  const chunks: Request[] = []
  const put = t.mock.fn(async (_url: string, _body: unknown) => ({ success: true }))
  const apiSource = readFileSync(new URL('../api/knowledge-base/index.ts', import.meta.url), 'utf8')
  const api = f.evaluate(`import { URLSearchParams } from 'node:url'\n${apiSource}`, {
    'node:url': { URLSearchParams },
    '../../utils/request': {
      get: (url: string) => new Promise((resolve, reject) => {
        const requests = url.startsWith('/api/v1/chunks/') ? chunks : metadata
        requests.push({ url, resolve, reject })
      }),
      put,
    },
    './knowledgeFileListQuery': {},
    './knowledgeReadOptions': knowledgeReadOptions,
  })
  const stores = f.evaluate(readFileSync(new URL('../stores/knowledge.ts', import.meta.url), 'utf8'), { vue, pinia })
  const root = pinia.createPinia()
  pinia.setActivePinia(root)
  t.after(() => pinia.disposePinia(root))
  const { default: useKnowledgeBase } = f.evaluate(readFileSync(new URL('./useKnowledgeBase.ts', import.meta.url), 'utf8'), {
    vue, pinia,
    '../utils/index': { formatStringDate: (date: Date) => date.toISOString() },
    '@/api/knowledge-base/index': api,
    '@/stores/knowledge': stores,
    'vue-router': { useRoute: () => ({ params: { kbId: 'kb' } }) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
  })
  const hook: ReturnType<typeof import('./useKnowledgeBase')['default']> = useKnowledgeBase('kb')
  return { hook, metadata, chunks, api, put }
}

function detail(id: string, revision = 'current') {
  return {
    success: true,
    data: {
      id, file_name: `${id}-${revision}.pdf`, file_type: 'pdf',
      updated_at: '2026-01-01T00:00:00Z', description: `${id}-${revision} summary`,
      custom_metadata: { revision }, tags: [{ id: revision, name: revision }],
    },
  }
}

const chunkPage = (content: string) => ({ success: true, data: [{ id: content, content }], total: 50 })

test('late metadata and chunks from a previous document cannot replace the current document', async t => {
  const { hook, metadata, chunks } = setup(t)
  hook.getCardDetails({ id: 'a' })
  hook.getCardDetails({ id: 'b' })
  metadata[1].resolve(detail('b'))
  chunks[1].resolve(chunkPage('b content'))
  await flush()
  const current = JSON.stringify(hook.details)

  metadata[0].resolve(detail('a', 'stale'))
  chunks[0].resolve(chunkPage('a content'))
  await flush()
  assert.equal(JSON.stringify(hook.details), current)
  assert.equal(hook.details.id, 'b')
  assert.equal(hook.details.md[0].content, 'b content')
})

for (const ids of [['a', 'b', 'a'], ['a', 'a']]) {
  test(`only the latest metadata request applies when opening ${ids.join(' → ')}`, async t => {
    const { hook, metadata } = setup(t)
    for (const id of ids) hook.getCardDetails({ id })
    metadata.at(-1)!.resolve(detail('a', 'newest'))
    await flush()
    for (let i = metadata.length - 2; i >= 0; i--) metadata[i].resolve(detail(ids[i], 'stale'))
    await flush()

    assert.equal(hook.details.id, 'a')
    assert.equal(hook.details.title, 'a-newest.pdf')
    assert.equal(hook.details.description, 'a-newest summary')
    assert.equal(hook.details.custom_metadata.revision, 'newest')
    assert.equal(hook.details.tags[0].id, 'newest')
  })
}

test('chunk pagination does not discard pending metadata for the same document', async t => {
  const { hook, metadata, chunks } = setup(t)
  hook.getCardDetails({ id: 'a' })
  hook.getfDetails('a', 2)
  assert.match(chunks[1].url, /page=2&/)
  chunks[1].resolve(chunkPage('page 2'))
  await flush()
  metadata[0].resolve(detail('a'))
  chunks[0].resolve(chunkPage('stale page 1'))
  await flush()

  assert.equal(hook.details.id, 'a')
  assert.equal(hook.details.title, 'a-current.pdf')
  assert.equal(hook.details.md[0].content, 'page 2')
  assert.equal(hook.details.chunkLoading, false)
})

test('summary updates target the current document after an old detail response arrives', async t => {
  const { hook, metadata, api, put } = setup(t)
  hook.getCardDetails({ id: 'a' })
  hook.getCardDetails({ id: 'b' })
  metadata[1].resolve(detail('b'))
  await flush()
  metadata[0].resolve(detail('a', 'stale'))
  await flush()

  // The drawer submits its current details.id to this production API helper.
  await api.updateKnowledgeSummary(hook.details.id, 'Edited B summary')
  assert.equal(put.mock.calls[0].arguments[0], '/api/v1/knowledge/b')
  assert.equal((put.mock.calls[0].arguments[1] as { description: string }).description, 'Edited B summary')
})

test('a rejected old metadata request leaves the current document intact', async t => {
  const { hook, metadata } = setup(t)
  hook.getCardDetails({ id: 'a' })
  hook.getCardDetails({ id: 'b' })
  metadata[1].resolve(detail('b'))
  await flush()
  metadata[0].reject(new Error('old request failed'))
  await flush()
  assert.equal(hook.details.id, 'b')
  assert.equal(hook.details.title, 'b-current.pdf')
})
