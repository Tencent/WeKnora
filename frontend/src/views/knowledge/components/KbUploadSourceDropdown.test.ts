import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'

import { shouldRejectKnowledgeFileType } from '../../../utils/fileTypeVerification.ts'

// The component and the filter module both import the app's '@/utils' barrel,
// which needs a browser (window.__RUNTIME_CONFIG__) and the i18n runtime. The
// test runs the real sources with that one dependency stubbed, so the
// assertions cover the shipped wiring rather than a copy of it.
const uploadSourcesSource = readFileSync(
  new URL('../utils/uploadSources.ts', import.meta.url),
  'utf8',
)
const uploadSourcesCompiled = ts.transpileModule(uploadSourcesSource, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

const dropdownSource = readFileSync(
  new URL('./KbUploadSourceDropdown.vue', import.meta.url),
  'utf8',
).split('<script setup lang="ts">')[1].split('</script>')[0]
const dropdownCompiled = ts.transpileModule(
  `${dropdownSource}\nexport { handleFilesChange }`,
  { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } },
).outputText

const knowledgeBaseSource = readFileSync(
  new URL('../KnowledgeBase.vue', import.meta.url),
  'utf8',
)
const uploadConfirmSource = readFileSync(new URL('./UploadConfirmDialog.vue', import.meta.url), 'utf8')

const MAX_FILE_BYTES = 100

interface FakeFile {
  name: string
  size: number
}

const utilsStub = {
  fileSizeVerification: (file: { size: number }, silent = false) => {
    void silent
    return file.size > MAX_FILE_BYTES
  },
  kbFileTypeVerification: (
    file: { name: string; size: number },
    silent = false,
    validTypes?: Set<string> | string[],
  ) => {
    void silent
    if (shouldRejectKnowledgeFileType(file.name, validTypes)) return true
    return file.size > MAX_FILE_BYTES
  },
}

type VideoHelpers = {
  isVideoParsingEnabled: (kbInfo?: unknown) => boolean
  isVideoParsingReady: (kbInfo?: unknown) => boolean
}

function loadUploadSources() {
  const state: Record<string, unknown> = {}
  vm.runInNewContext(uploadSourcesCompiled, {
    exports: state,
    require: (id: string) => {
      assert.equal(id, '@/utils', `unexpected import ${id}`)
      return utilsStub
    },
  })
  return state as VideoHelpers
}

const videoHelpers = loadUploadSources()

type DropdownHarness = {
  handleFilesChange: (event: unknown, fromFolder: boolean) => void
  tCalls: Array<{ key: string; params?: Record<string, unknown> }>
  warnings: string[]
  emitted: Array<{ event: string; payload: unknown }>
}

function mountDropdown(props: Record<string, unknown>): DropdownHarness {
  const tCalls: DropdownHarness['tCalls'] = []
  const warnings: string[] = []
  const emitted: DropdownHarness['emitted'] = []
  const state: Record<string, unknown> = {}
  const modules: Record<string, unknown> = {
    vue: { ...vue },
    'vue-i18n': {
      useI18n: () => ({
        t: (key: string, params?: Record<string, unknown>) => {
          tCalls.push({ key, params })
          return key
        },
      }),
    },
    'tdesign-vue-next': {
      MessagePlugin: {
        warning: (content: string) => warnings.push(content),
        error: (content: string) => warnings.push(content),
        success: () => {},
        info: () => {},
      },
    },
    'tdesign-icons-vue-next': {
      AddIcon: () => null,
      FileAddIcon: () => null,
      UploadIcon: () => null,
      FolderAddIcon: () => null,
      LinkIcon: () => null,
      EditIcon: () => null,
    },
    '../utils/uploadSources': videoHelpers,
  }

  vm.runInNewContext(dropdownCompiled, {
    exports: state,
    URL,
    defineProps: () => props,
    withDefaults: (declared: unknown) => declared,
    defineEmits: () => (event: string, payload: unknown) => {
      emitted.push({ event, payload })
    },
    defineExpose: () => {},
    require: (id: string) => {
      assert.ok(id in modules, `unexpected import ${id}`)
      return modules[id]
    },
  })

  return {
    handleFilesChange: state.handleFilesChange as DropdownHarness['handleFilesChange'],
    tCalls,
    warnings,
    emitted,
  }
}

const file = (name: string, size = 10): FakeFile => ({ name, size })

function pickFiles(files: FakeFile[]) {
  return { target: { files, value: 'C:\\fakepath\\file' } }
}

function emittedFileNames(harness: DropdownHarness): string[] {
  const filesEvent = harness.emitted.find(item => item.event === 'files')
  assert.ok(filesEvent, 'the component must emit the accepted files')
  return Array.from(filesEvent.payload as FakeFile[], f => f.name)
}

// Mock knowledge-base details, shaped like the GET /knowledge-bases/:id payload.
const kbVideoOff = {
  id: 'kb-1',
  video_config: { enabled: false, mode: 'audio_only', model_id: '' },
  asr_config: { enabled: true, model_id: 'asr-audio' },
}
const kbVideoOnWithModel = {
  id: 'kb-1',
  video_config: { enabled: true, mode: 'audio_only', model_id: 'asr-video' },
  asr_config: { enabled: true, model_id: 'asr-audio' },
}
const kbVideoOnWithoutModel = {
  id: 'kb-1',
  video_config: { enabled: true, mode: 'audio_only', model_id: '' },
  asr_config: { enabled: false, model_id: '' },
}

test('videos are filtered with an "enable it" hint when the KB switch is off', () => {
  const harness = mountDropdown({
    allowVideo: videoHelpers.isVideoParsingEnabled(kbVideoOff),
    videoParseReady: videoHelpers.isVideoParsingReady(kbVideoOff),
    supportedFileTypes: ['md'],
    acceptFileTypes: '.md',
  })

  harness.handleFilesChange(pickFiles([file('notes.md'), file('lecture.mp4')]), false)

  assert.deepEqual(emittedFileNames(harness), ['notes.md'])
  assert.ok(
    harness.tCalls.some(call => call.key === 'knowledgeBase.videosFilteredParsingDisabled'),
    'the skipped-video warning must point at the KB video setting',
  )
  const skippedCall = harness.tCalls.find(
    call => call.key === 'knowledgeBase.videosFilteredParsingDisabled',
  )
  assert.equal(skippedCall?.params?.count, 1)
  assert.equal(
    harness.tCalls.some(call => call.key === 'knowledgeBase.videoModelNotConfigured'),
    false,
  )
})

test('a videos-only selection reports the skipped count and emits nothing', () => {
  const harness = mountDropdown({
    allowVideo: videoHelpers.isVideoParsingEnabled(kbVideoOff),
    videoParseReady: videoHelpers.isVideoParsingReady(kbVideoOff),
    supportedFileTypes: ['md'],
    acceptFileTypes: '.md',
  })

  harness.handleFilesChange(pickFiles([file('lecture.mp4')]), false)

  assert.equal(
    harness.emitted.some(item => item.event === 'files'),
    false,
    'nothing must be uploaded when every selected file was skipped',
  )
  const skippedCall = harness.tCalls.find(
    call => call.key === 'knowledgeBase.videosFilteredParsingDisabled',
  )
  assert.equal(skippedCall?.params?.count, 1)
  assert.equal(
    harness.tCalls.some(call => call.key === 'knowledgeBase.allFilesSkippedNoEngine'),
    false,
  )
})

test('videos reach the pending list when the KB switch is on and its model is configured', () => {
  const harness = mountDropdown({
    allowVideo: videoHelpers.isVideoParsingEnabled(kbVideoOnWithModel),
    videoParseReady: videoHelpers.isVideoParsingReady(kbVideoOnWithModel),
    supportedFileTypes: ['md'],
    acceptFileTypes: '.md,.mp4',
  })

  harness.handleFilesChange(pickFiles([file('notes.md'), file('lecture.mp4')]), false)

  assert.deepEqual(emittedFileNames(harness), ['notes.md', 'lecture.mp4'])
  assert.deepEqual(harness.warnings, [])
  assert.equal(
    harness.tCalls.some(call => call.key === 'knowledgeBase.videosFilteredParsingDisabled'),
    false,
  )
})

test('videos still reach the list when the KB switch is on but the model is missing', () => {
  const harness = mountDropdown({
    allowVideo: videoHelpers.isVideoParsingEnabled(kbVideoOnWithoutModel),
    videoParseReady: videoHelpers.isVideoParsingReady(kbVideoOnWithoutModel),
    supportedFileTypes: ['md'],
    acceptFileTypes: '.md,.mp4',
  })

  harness.handleFilesChange(pickFiles([file('lecture.mp4')]), false)

  // Advisory only: the warning names the missing model, the upload is not blocked
  // locally because the server stays authoritative.
  assert.deepEqual(emittedFileNames(harness), ['lecture.mp4'])
  assert.ok(
    harness.tCalls.some(call => call.key === 'knowledgeBase.videoModelNotConfigured'),
    'a KB that opted in without a model should warn before the 400',
  )
})

test('the KB page derives allowVideo from the loaded KB detail, not a new request', () => {
  assert.match(knowledgeBaseSource, /isVideoParsingEnabled\(kbInfo\.value\)/)
  assert.match(knowledgeBaseSource, /isVideoParsingReady\(kbInfo\.value\)/)
  assert.match(knowledgeBaseSource, /:allow-video="videoParsingEnabled"/)
  assert.match(knowledgeBaseSource, /:video-parse-ready="videoParsingReady"/)
  assert.match(knowledgeBaseSource, /UPLOAD_VIDEO_EXTENSIONS/)
  assert.doesNotMatch(knowledgeBaseSource, /getKnowledgeBaseById|getKnowledgeBase\(/)
})

test('the upload confirmation dialog forwards its own KB detail to the picker', () => {
  assert.match(uploadConfirmSource, /isVideoParsingEnabled\(props\.kbInfo\)/)
  assert.match(uploadConfirmSource, /:allow-video="videoParsingEnabled"/)
  assert.match(uploadConfirmSource, /:video-parse-ready="videoParsingReady"/)
})
