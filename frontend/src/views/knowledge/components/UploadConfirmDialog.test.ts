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
  assert.match(knowledgeBase, /enqueueUploads\(files, \{[\s\S]*?\btagIds,[\s\S]*?\}\)/)
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
  assert.match(platform, /weknora:knowledge-file-drop/)
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

// The ASR language used to be a free-text box, so users had to recall vendor codes
// such as `zh` themselves. It is now a dropdown with the common languages, while
// `creatable` keeps es / fr / yue and friends typeable for anything not listed.
test('offers the ASR language as a dropdown instead of a free-text field', () => {
  assert.doesNotMatch(dialog, /v-model="uiState\.asrConfig\.language"/)
  assert.match(dialog, /<t-select[\s\S]{0,200}v-model="asrLanguageModel"/)
  assert.match(dialog, /:options="asrLanguageOptions"/)
  assert.match(dialog, /(?<![-\w])filterable(?![-\w])/)
  assert.match(dialog, /(?<![-\w])creatable(?![-\w])/)
  assert.match(dialog, /(?<![-\w])clearable(?![-\w])/)
})

test('keeps vendor language codes as the option values', () => {
  const optionBlock = dialog.slice(
    dialog.indexOf('const asrLanguageOptions'),
    dialog.indexOf('const asrLanguageModel'),
  )
  for (const code of ['zh', 'en', 'ja', 'ko', 'ru']) {
    assert.match(optionBlock, new RegExp(`value: '${code}'`))
  }
  // Display names come from i18n, never from the stored code (no `value: 'Chinese'`).
  assert.doesNotMatch(optionBlock, /value: '(Chinese|English|Japanese|Korean|Russian)'/)
  assert.match(optionBlock, /value: ASR_LANGUAGE_AUTO/)
})

test('stores the ASR language through the normalising model so clearing means auto-detect', () => {
  assert.match(dialog, /fromAsrLanguageModel\(selected\)/)
  assert.match(dialog, /toAsrLanguageModel\(uiState\.value\.asrConfig\.language\)/)
  assert.match(dialog, /language: state\.asrConfig\.language/)
  assert.match(dialog, /if \(o\.asr_config\.language != null\) s\.asrConfig\.language = o\.asr_config\.language/)
})

test('labels every ASR language option in all six locale bundles', () => {
  const localeKeys = [
    "languageAutoDetect:",
    "languageOptions: {",
    "zh:",
    "en:",
    "ja:",
    "ko:",
    "ru:",
  ]
  for (const locale of ['zh-CN', 'zh-TW', 'en-US', 'ja-JP', 'ko-KR', 'ru-RU']) {
    const bundle = readFileSync(new URL(`../../../i18n/locales/${locale}.ts`, import.meta.url), 'utf8')
    const labelAt = bundle.indexOf('      languageLabel:')
    const placeholderAt = bundle.indexOf('      languagePlaceholder:', labelAt)
    assert.ok(labelAt >= 0 && placeholderAt > labelAt, `${locale} has no ASR language block`)
    const asrBlock = bundle.slice(labelAt, placeholderAt)
    for (const key of localeKeys) {
      assert.ok(asrBlock.includes(key), `${locale} is missing ${key}`)
    }
  }
})
