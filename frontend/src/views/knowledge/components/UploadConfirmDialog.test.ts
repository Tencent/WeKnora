import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const dialog = readFileSync(new URL('./UploadConfirmDialog.vue', import.meta.url), 'utf8')
const host = readFileSync(new URL('../../../components/UploadConfirmHost.vue', import.meta.url), 'utf8')
const knowledgeBase = readFileSync(new URL('../KnowledgeBase.vue', import.meta.url), 'utf8')
const platform = readFileSync(new URL('../../platform/index.vue', import.meta.url), 'utf8')

test('uses the current folder as the upload destination', () => {
  assert.match(host, /:current-folder-id="uploadConfirmStore\.currentFolderId"/)
  assert.match(dialog, /const selectedFolderId = ref<string \| null>\(props\.currentFolderId\)/)
  assert.match(dialog, /folderId: selectedFolderId\.value/)
})

test('uses the confirmed folder for file and URL imports', () => {
  assert.match(knowledgeBase, /const folderId = result\.folderId/)
  assert.match(knowledgeBase, /executeUploadBatch\(files, \{ processConfig, folderId \}\)/)
  assert.match(knowledgeBase, /executeUrlImport\(url, processConfig, folderId\)/)
})

test('dispatches global knowledge file drops and exposes the local upload confirmation flow', () => {
  assert.match(platform, /weknora:knowledge-file-drop/)
  assert.match(knowledgeBase, /const handleUploadSourceFiles = \(files: File\[\]\)/)
  assert.match(knowledgeBase, /openUploadConfirmDialog\(files\)/)
})

test('uses section navigation with shared parser settings and advanced options grouped', () => {
  assert.match(dialog, /<KBParserSettings/)
  assert.match(dialog, /:parser-engine-rules="uiState\.chunkingConfig\.parserEngineRules"/)
  assert.match(dialog, /@update:parser-engine-rules="handleParserEngineRulesUpdate"/)
  assert.match(dialog, /activeSection === 'graph'/)
  assert.match(dialog, /class="files-panel"/)
  assert.match(dialog, /activeSection === 'multimodal'/)
  assert.match(dialog, /<KBChunkingSettings/)
})
