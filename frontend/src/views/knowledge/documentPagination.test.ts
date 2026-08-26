import assert from 'node:assert/strict'
import test from 'node:test'

import { getNextDocumentPage } from './documentPagination.ts'

test('按当前批量继续分页，直到全部加载完毕', () => {
  const pages: number[] = []
  const pageSize = 45
  let loadedCount = pageSize
  let currentPage = 1

  while (true) {
    const nextPage = getNextDocumentPage(loadedCount, 100, currentPage, pageSize)
    if (!nextPage) break
    pages.push(nextPage)
    currentPage = nextPage
    loadedCount = Math.min(loadedCount + pageSize, 100)
  }

  assert.deepEqual(pages, [2, 3])
})

test('全部文档已加载时不再创建翻页请求', () => {
  assert.equal(getNextDocumentPage(90, 90, 3, 35), null)
})

test('异常总数不会导致请求超过计算出的总页数', () => {
  assert.equal(getNextDocumentPage(70, 90, 3, 35), null)
})
