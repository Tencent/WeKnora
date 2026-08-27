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
  assert.match(knowledgeBase, /executeUploadBatch\(files, \{[\s\S]*?\btagIds,[\s\S]*?\}\)/)
  assert.match(knowledgeBase, /executeUrlImport\(url, processConfig, tagIds\)/)
  assert.doesNotMatch(
    knowledgeBase,
    /const tagIdsToUpload = selectedTagIds\.value\.length > 0 \? \[\.\.\.selectedTagIds\.value\] : undefined/,
  )
})

// Browsing a folder pre-fills the upload destination, so the dialog must show it
// and the batch must use the folder the user confirmed there — never the sidebar
// selection as it stands when the uploads actually start.
test('takes the upload destination folder from the confirmation result', () => {
  assert.match(host, /:target-folder="uploadConfirmStore\.targetFolder"/)
  assert.match(host, /:folder-options="uploadConfirmStore\.folderOptions"/)
  assert.match(dialog, /class="destination-row"/)
  assert.match(dialog, /attach="body"/)
  assert.match(dialog, /overlay-class-name="upload-destination-popup"/)
  assert.match(dialog, /FolderPickerMenu/)
  assert.match(dialog, /onDestinationPicked/)
  assert.match(dialog, /targetFolder: localTargetFolder\.value/)
  assert.match(knowledgeBase, /targetFolder: result\.targetFolder \|\| ROOT_FOLDER_PATH/)
  assert.match(knowledgeBase, /targetFolder: selectedFolderPath\.value/)
  assert.match(knowledgeBase, /folderOptions: folderOptions\.value/)
})

test('creates sub-folders from per-row actions without changing the selected destination', () => {
  const picker = readFileSync(new URL('./FolderPickerMenu.vue', import.meta.url), 'utf8')
  assert.match(picker, /startCreatingUnder/)
  assert.match(picker, /folder-picker__add/)
  assert.match(picker, /folder-picker__item--create/)
  assert.match(picker, /@keydown\.enter\.stop="commitNewFolder"/)
  assert.doesNotMatch(picker, /newFolderHintText/)
  assert.doesNotMatch(picker, /folder-picker__create-actions/)
  assert.doesNotMatch(picker, /newFolderParent/)
  assert.match(dialog, /pendingFolderPaths/)
  assert.match(dialog, /pickerFolderOptions/)
  assert.match(dialog, /@create="onDestinationCreated"/)
})

test('calls out directory uploads via relative paths in the file list', () => {
  assert.match(dialog, /fileRelativeDir/)
  assert.doesNotMatch(dialog, /folder-upload-banner/)
})

test('routes global knowledge file drops through the upload confirmation flow', () => {
  assert.match(platform, /gridora:knowledge-file-drop/)
  assert.match(knowledgeBase, /handleKnowledgeFileDrop/)
  assert.match(knowledgeBase, /handleUploadSourceFiles\(files\)/)
})

test('uses section navigation with inline chunking controls and advanced options grouped', () => {
  assert.match(dialog, /v-model="uiState\.chunkingConfig\.strategy"/)
  assert.match(dialog, /v-model="uiState\.chunkingConfig\.chunkSize"/)
  assert.match(dialog, /v-model="uiState\.chunkingConfig\.chunkOverlap"/)
  assert.match(dialog, /chunkingMoreOpen/)
  assert.match(dialog, /activeSection === 'graph'/)
  assert.match(dialog, /class="files-panel"/)
  assert.match(dialog, /statusFull/)
  assert.match(dialog, /data-section="multimodal"/)
  assert.doesNotMatch(dialog, /<KBChunkingSettings/)
})
