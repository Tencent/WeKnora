import assert from 'node:assert/strict'
import test from 'node:test'

import {
  beginDocumentPageRequest,
  createDocumentPaginationState,
  resetDocumentPagination,
  settleDocumentPageRequest,
  shouldLoadNextDocumentPage,
} from './documentPagination.ts'

const viewport = {
  loadedCount: 35,
  totalCount: 90,
  pageSize: 35,
  scrollTop: 0,
  scrollHeight: 600,
  clientHeight: 600,
}

test('首批内容没有撑出滚动条时仍应继续加载下一页', () => {
  assert.equal(shouldLoadNextDocumentPage(viewport), true)
})

test('尚未接近列表底部时不应提前翻页', () => {
  assert.equal(
    shouldLoadNextDocumentPage({
      ...viewport,
      scrollHeight: 1200,
      clientHeight: 600,
      threshold: 120,
    }),
    false,
  )
})

test('翻页失败后不推进页码并允许重试同一页', () => {
  const state = createDocumentPaginationState()
  const firstRequest = beginDocumentPageRequest(state, viewport)

  assert.deepEqual(firstRequest, { page: 2, generation: 0 })
  assert.equal(settleDocumentPageRequest(state, firstRequest!, false), 'failed')
  assert.equal(state.page, 1)

  const retryRequest = beginDocumentPageRequest(state, viewport)
  assert.deepEqual(retryRequest, { page: 2, generation: 0 })
  assert.equal(settleDocumentPageRequest(state, retryRequest!, true), 'loaded')
  assert.equal(state.page, 2)
})

test('重置列表后忽略旧筛选条件下完成的请求', () => {
  const state = createDocumentPaginationState()
  const request = beginDocumentPageRequest(state, viewport)

  resetDocumentPagination(state)

  assert.equal(settleDocumentPageRequest(state, request!, true), 'stale')
  assert.deepEqual(state, { page: 1, loading: false, generation: 1 })
})

test('全部文档已加载时不再创建翻页请求', () => {
  const state = createDocumentPaginationState()
  const completeViewport = { ...viewport, loadedCount: 90 }

  assert.equal(beginDocumentPageRequest(state, completeViewport, true), null)
})
