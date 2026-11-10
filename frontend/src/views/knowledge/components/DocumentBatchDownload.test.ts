import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const knowledgeBase = readFileSync(new URL('../KnowledgeBase.vue', import.meta.url), 'utf8')
const batchBar = readFileSync(new URL('./DocumentBatchBar.vue', import.meta.url), 'utf8')
const listView = readFileSync(new URL('./DocumentListView.vue', import.meta.url), 'utf8')
const knowledgeApi = readFileSync(new URL('../../../api/knowledge-base/index.ts', import.meta.url), 'utf8')
const request = readFileSync(new URL('../../../utils/request.ts', import.meta.url), 'utf8')

test('批量下载使用受认证的 Blob 请求并支持取消', () => {
  assert.match(knowledgeApi, /knowledge-bases\/\$\{encodeURIComponent\(kbId\)\}\/knowledge\/batch-download/)
  assert.match(knowledgeApi, /responseType:\s*'blob'/)
  assert.match(knowledgeApi, /signal,/)
  assert.match(knowledgeBase, /new AbortController\(\)/)
  assert.match(knowledgeBase, /batchDownloadController\?\.abort\(\)/)
})

test('批量下载界面限制单批 200 项并保留只读权限边界', () => {
  assert.match(batchBar, /v-if="canDownload"/)
  assert.match(batchBar, /count > 200/)
  assert.match(batchBar, /batch-download-trigger/)
  assert.match(batchBar, /v-if="canMutate"/)
  assert.match(knowledgeBase, /isBatchDownloadableKnowledge/)
  assert.match(knowledgeBase, /ids\.length > MAX_BATCH_DOWNLOAD_FILES/)
  assert.match(knowledgeBase, /:can-download="canDownloadKnowledge"/)
  assert.match(knowledgeBase, /@select-loaded="toggleSelectAll\(true\)"/)
  assert.match(listView, /v-if="canEdit \|\| canDownload"/)
})

test('Blob 格式的 JSON 错误会还原为可读消息', () => {
  assert.match(request, /error\.response\.data instanceof Blob/)
  assert.match(request, /JSON\.parse\(text\)/)
  assert.match(request, /text\.startsWith\('\{'\)/)
})
