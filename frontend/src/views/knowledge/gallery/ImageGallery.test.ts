import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import type { ImageAsset, ImageListParams, ImageListResult } from '@/api/image-gallery'

// Exercise the component's actual loading and viewer logic with deferred API
// responses so races and retries are deterministic, without a live backend.
const source = readFileSync(new URL('./ImageGallery.vue', import.meta.url), 'utf8')
  .split('<script setup lang="ts">')[1].split('</script>')[0]
const compiled = ts.transpileModule(`${source}\nexport {
  items, page, total, loading, error, keyword, hasMore, viewerIndex, viewerOpen,
  reload, loadMore, openViewer, onViewerLoadMore, initGallery,
  resolveImageSrc, thumbUrls,
}`, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText

function setup(proxyImages = false) {
  const calls: Array<{
    kb: string; params: ImageListParams
    resolve: (value: ImageListResult) => void; reject: (reason: Error) => void
  }> = []
  const imageCalls: Array<{
    url: string; signal?: AbortSignal
    resolve: (response: { ok: boolean; blob: () => Promise<Blob> }) => void
    reject: (reason: Error) => void
  }> = []
  const created: string[] = []
  const revoked: string[] = []
  const unmountHooks: Array<() => void> = []
  const props = vue.reactive({ knowledgeBaseId: 'kb-1' })
  const modules: Record<string, unknown> = {
    vue: { ...vue, onMounted() {}, onBeforeUnmount: (hook: () => void) => unmountHooks.push(hook), watch() {} },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key, te: () => false }) },
    '@/api/image-gallery': {
      listGalleryImages: (kb: string, params: ImageListParams) => new Promise((resolve, reject) => {
        calls.push({ kb, params, resolve, reject })
      }),
      fetchGalleryConfig: async () => ({ attributes: [], attribute_sources: [], mode: 'all', status: {} }),
    },
    '@/api/auth': { updateMyPreferences: async () => {} },
    './galleryImageSrc': {
      galleryImageRequest: (url: string, kb: string) => proxyImages && url.startsWith('local://')
        ? { url: `/kb/${kb}/${url}`, headers: { Authorization: 'test-token' } }
        : null,
    },
  }
  const state: any = {}
  vm.runInNewContext(compiled, {
    exports: state, defineProps: () => props, defineEmits: () => () => {},
    setTimeout, clearTimeout, console, Error, AbortController,
    fetch: (url: string, options: RequestInit) => new Promise((resolve, reject) => {
      imageCalls.push({ url, signal: options.signal ?? undefined, resolve, reject })
    }),
    URL: {
      createObjectURL: () => {
        const url = `blob:gallery-${created.length + 1}`
        created.push(url)
        return url
      },
      revokeObjectURL: (url: string) => revoked.push(url),
    },
    require: (id: string) => {
      assert.ok(id in modules, `unexpected import ${id}`)
      return modules[id]
    },
  })
  const respond = (index: number, ids: string[], total = 60) => calls[index].resolve({
    items: ids.map(id => ({ id, url: `https://example.com/${id}.png` }) as ImageAsset),
    total, page: calls[index].params.page!, pageSize: 24,
  })
  const ids = () => Array.from(state.items.value, (img: ImageAsset) => img.id)
  const respondImage = (index: number) => imageCalls[index].resolve({ ok: true, blob: async () => new Blob(['image']) })
  const unmount = () => unmountHooks.forEach(hook => hook())
  return { state, calls, props, respond, ids, imageCalls, respondImage, created, revoked, unmount }
}

const settleImages = () => new Promise<void>(resolve => setImmediate(resolve))

test('scroll appends unique images, serializes requests, and stops at the total', async () => {
  const { state, calls, respond, ids } = setup()
  const first = state.reload()
  respond(0, ['a', 'b'])
  await first
  const next = state.loadMore()
  assert.equal(await state.loadMore(), false)
  assert.equal(calls.length, 2)
  assert.equal(calls[1].params.page, 2)
  respond(1, ['b', 'c'], 48)
  await next
  assert.deepEqual(ids(), ['a', 'b', 'c'])
  assert.equal(state.hasMore.value, false)
  assert.equal(await state.loadMore(), false)
})

test('append failures retain cards and retry the same page', async () => {
  const { state, calls, respond, ids } = setup()
  const first = state.reload()
  respond(0, ['a'])
  await first
  const next = state.loadMore()
  calls[1].reject(new Error('offline'))
  assert.equal(await next, false)
  assert.deepEqual(ids(), ['a'])
  assert.equal(state.page.value, 1)
  assert.equal(state.error.value, 'offline')
  const retry = state.loadMore()
  assert.equal(calls[2].params.page, 2)
  respond(2, ['b'])
  await retry
  assert.deepEqual(ids(), ['a', 'b'])
  assert.equal(state.error.value, '')
})

test('a new search discards an older append and resets the viewer', async () => {
  const { state, calls, respond, ids } = setup()
  const first = state.reload()
  respond(0, ['a'])
  await first
  state.openViewer(0)
  const older = state.onViewerLoadMore()
  state.keyword.value = 'new search'
  const fresh = state.reload()
  assert.equal(state.viewerOpen.value, false)
  assert.equal(calls[2].params.keyword, 'new search')
  respond(2, ['new'], 1)
  await fresh
  respond(1, ['old'])
  await older
  assert.deepEqual(ids(), ['new'])
  assert.equal(state.total.value, 1)
})

test('viewer loads past the last card without dropping previous images', async () => {
  const { state, respond, ids } = setup()
  const first = state.reload()
  respond(0, ['a', 'b'])
  await first
  state.openViewer(1)
  const step = state.onViewerLoadMore()
  respond(1, ['c', 'd'])
  await step
  assert.deepEqual(ids(), ['a', 'b', 'c', 'd'])
  assert.equal(state.viewerIndex.value, 2)
  assert.equal(state.viewerOpen.value, true)
})

test('switching knowledge bases invalidates the previous request immediately', async () => {
  const { state, props, calls, respond, ids } = setup()
  const old = state.reload()
  props.knowledgeBaseId = 'kb-2'
  const fresh = state.initGallery()
  respond(0, ['old'])
  await old
  await vue.nextTick()
  assert.deepEqual(ids(), [])
  assert.equal(calls[1].kb, 'kb-2')
  respond(1, ['new'], 1)
  await fresh
  assert.deepEqual(ids(), ['new'])
})

test('an empty batch stops automatic loading even if the total is stale', async () => {
  const { state, respond, calls } = setup()
  const first = state.reload()
  respond(0, ['a'])
  await first
  const next = state.loadMore()
  respond(1, [], 100)
  await next
  assert.equal(state.hasMore.value, false)
  assert.equal(await state.loadMore(), false)
  assert.equal(calls.length, 2)
})

test('proxied images share a request and cached URL, then release it on unmount', async () => {
  const { state, imageCalls, respondImage, created, revoked, unmount } = setup(true)
  const first = state.resolveImageSrc('local://image')
  assert.equal(state.resolveImageSrc('local://image'), first)
  assert.equal(imageCalls.length, 1)
  respondImage(0)
  const url = await first
  assert.equal(await state.resolveImageSrc('local://image'), url)
  assert.equal(imageCalls.length, 1)
  assert.deepEqual(created, [url])
  unmount()
  assert.deepEqual(revoked, [url])
  const late = state.resolveImageSrc('local://image')
  assert.equal(imageCalls.length, 1)
  assert.equal(await late, 'local://image')
})

test('unmount aborts a thumbnail fetch and ignores a response that arrives anyway', async () => {
  const { state, calls, imageCalls, created, revoked, unmount } = setup(true)
  const listing = state.reload()
  calls[0].resolve({ items: [{ id: 'a', url: 'local://image' } as ImageAsset], total: 1, page: 1, pageSize: 24 })
  await listing
  const pending = state.resolveImageSrc('local://image')
  unmount()
  assert.equal(imageCalls[0].signal?.aborted, true)
  let bodyReads = 0
  imageCalls[0].resolve({ ok: true, blob: async () => { bodyReads++; return new Blob(['image']) } })
  assert.equal(await pending, 'local://image')
  assert.equal(bodyReads, 0)
  assert.deepEqual(created, [])
  assert.deepEqual(revoked, [])
  assert.deepEqual(Object.keys(state.thumbUrls.value), [])
})

test('unmount during response body reading prevents late Blob URL creation', async () => {
  const { state, imageCalls, created, unmount } = setup(true)
  const pending = state.resolveImageSrc('local://image')
  let finishBody!: (blob: Blob) => void
  const body = new Promise<Blob>(resolve => { finishBody = resolve })
  let bodyReads = 0
  imageCalls[0].resolve({ ok: true, blob: () => { bodyReads++; return body } })
  await settleImages()
  assert.equal(bodyReads, 1)
  unmount()
  finishBody(new Blob(['image']))
  assert.equal(await pending, 'local://image')
  assert.deepEqual(created, [])
})

test('reload cancels old image requests without removing a replacement for the same URL', async () => {
  const { state, imageCalls, respondImage, respond, created, unmount } = setup(true)
  const old = state.resolveImageSrc('local://image')
  const listing = state.reload()
  assert.equal(imageCalls[0].signal?.aborted, true)
  const fresh = state.resolveImageSrc('local://image')
  assert.equal(imageCalls.length, 2)
  assert.equal(imageCalls[1].signal?.aborted, false)
  respondImage(0)
  assert.equal(await old, 'local://image')
  assert.equal(state.resolveImageSrc('local://image'), fresh)
  assert.equal(imageCalls.length, 2)
  respond(0, [], 0)
  await listing
  respondImage(1)
  assert.equal(await fresh, 'blob:gallery-1')
  assert.deepEqual(created, ['blob:gallery-1'])
  unmount()
})

test('switching knowledge bases releases cached URLs and starts new scoped image requests', async () => {
  const { state, props, imageCalls, respondImage, respond, created, revoked, unmount } = setup(true)
  const cached = state.resolveImageSrc('local://cached')
  respondImage(0)
  await cached
  const old = state.resolveImageSrc('local://pending')
  props.knowledgeBaseId = 'kb-2'
  const listing = state.initGallery()
  assert.equal(imageCalls[1].signal?.aborted, true)
  assert.deepEqual(revoked, ['blob:gallery-1'])
  await settleImages()
  respond(0, [], 0)
  await listing
  const fresh = state.resolveImageSrc('local://pending')
  assert.equal(imageCalls[2].url, '/kb/kb-2/local://pending')
  respondImage(1)
  assert.equal(await old, 'local://pending')
  assert.equal(state.resolveImageSrc('local://pending'), fresh)
  respondImage(2)
  await fresh
  const refetch = state.resolveImageSrc('local://cached')
  assert.equal(imageCalls[3].url, '/kb/kb-2/local://cached')
  respondImage(3)
  await refetch
  assert.deepEqual(created, ['blob:gallery-1', 'blob:gallery-2', 'blob:gallery-3'])
  unmount()
})

test('HTTP and network failures allow retry, and ordinary URLs skip the proxy', async () => {
  const { state, imageCalls, respondImage, unmount } = setup(true)
  const raw = 'local://image'
  const failedHTTP = state.resolveImageSrc(raw)
  imageCalls[0].resolve({ ok: false, blob: async () => { throw new Error('must not read') } })
  assert.equal(await failedHTTP, raw)
  const failedNetwork = state.resolveImageSrc(raw)
  imageCalls[1].reject(new Error('offline'))
  assert.equal(await failedNetwork, raw)
  const success = state.resolveImageSrc(raw)
  respondImage(2)
  assert.equal(await success, 'blob:gallery-1')
  assert.equal(await state.resolveImageSrc('https://example.com/image.png'), 'https://example.com/image.png')
  assert.equal(imageCalls.length, 3)
  unmount()
})
