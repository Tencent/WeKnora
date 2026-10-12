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
}`, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText

function setup() {
  const calls: Array<{
    kb: string; params: ImageListParams
    resolve: (value: ImageListResult) => void; reject: (reason: Error) => void
  }> = []
  const props = vue.reactive({ knowledgeBaseId: 'kb-1' })
  const modules: Record<string, unknown> = {
    vue: { ...vue, onMounted() {}, onBeforeUnmount() {}, watch() {} },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key, te: () => false }) },
    '@/api/image-gallery': {
      listGalleryImages: (kb: string, params: ImageListParams) => new Promise((resolve, reject) => {
        calls.push({ kb, params, resolve, reject })
      }),
      fetchGalleryConfig: async () => ({ attributes: [], attribute_sources: [], mode: 'all', status: {} }),
    },
    '@/api/auth': { updateMyPreferences: async () => {} },
    './galleryImageSrc': { galleryImageRequest: () => null },
  }
  const state: any = {}
  vm.runInNewContext(compiled, {
    exports: state, defineProps: () => props, defineEmits: () => () => {},
    setTimeout, clearTimeout, URL, console, Error,
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
  return { state, calls, props, respond, ids }
}

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
