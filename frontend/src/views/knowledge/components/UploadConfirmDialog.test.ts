import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const dialog = readFileSync(new URL('./UploadConfirmDialog.vue', import.meta.url), 'utf8')
const host = readFileSync(new URL('../../../components/UploadConfirmHost.vue', import.meta.url), 'utf8')
const knowledgeBase = readFileSync(new URL('../KnowledgeBase.vue', import.meta.url), 'utf8')
const platform = readFileSync(new URL('../../platform/index.vue', import.meta.url), 'utf8')

test('selects multiple document tags and returns them with the confirmation result', () => {
  assert.match(host, /:tag-ids="uploadConfirmStore\.tagIds"/)
  assert.match(dialog, /v-model="selectedTagIds"/)
  assert.match(dialog, /multiple/)
  assert.match(dialog, /listKnowledgeTags\(kbId, \{ page: 1, page_size: 1000 \}\)/)
  assert.match(dialog, /tagIds: \[\.\.\.selectedTagIds\.value\]/)
})

test('uses confirmed tags for file and URL imports instead of reading the list filter at upload time', () => {
  assert.match(knowledgeBase, /const tagIds = result\.tagIds \|\| \[\]/)
  assert.match(knowledgeBase, /executeUploadBatch\(files, \{ processConfig, tagIds \}\)/)
  assert.match(knowledgeBase, /executeUrlImport\(url, processConfig, tagIds\)/)
  assert.doesNotMatch(
    knowledgeBase,
    /const tagIdsToUpload = selectedTagIds\.value\.length > 0 \? \[\.\.\.selectedTagIds\.value\] : undefined/,
  )
})

test('routes global knowledge file drops through the upload confirmation flow', () => {
  assert.match(platform, /weknora:knowledge-file-drop/)
  assert.match(knowledgeBase, /handleKnowledgeFileDrop/)
  assert.match(knowledgeBase, /handleUploadSourceFiles\(files\)/)
})
