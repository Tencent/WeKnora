import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'

import { shouldRejectKnowledgeFileType } from '../../../utils/fileTypeVerification.ts'

// uploadSources.ts imports the shared utils barrel, which reads
// window.__RUNTIME_CONFIG__ at module load and pulls in the i18n runtime. The
// test runs the real module with that single dependency stubbed, so the filter
// logic itself (not a copy of it) is what the assertions cover.
const SOURCE = readFileSync(new URL('./uploadSources.ts', import.meta.url), 'utf8')
const COMPILED = ts.transpileModule(SOURCE, {
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2022,
  },
}).outputText

const MAX_FILE_BYTES = 100

interface FakeFile {
  name: string
  size: number
  webkitRelativePath?: string
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

type UploadSourcesModule = {
  filterUploadFiles: (
    files: FakeFile[],
    options?: {
      supportedFileTypes?: Set<string> | string[]
      fromFolder?: boolean
      multiFile?: boolean
      allowVideo?: boolean
    },
  ) => {
    validFiles: FakeFile[]
    skippedCount: number
    videoFilteredCount: number
    hiddenFileCount: number
  }
  isVideoParsingEnabled: (kbInfo?: unknown) => boolean
  isVideoParsingReady: (kbInfo?: unknown) => boolean
}

function loadUploadSources(): UploadSourcesModule {
  const state: Record<string, unknown> = {}
  vm.runInNewContext(COMPILED, {
    exports: state,
    require: (id: string) => {
      assert.equal(id, '@/utils', `unexpected import ${id}`)
      return utilsStub
    },
  })
  return state as unknown as UploadSourcesModule
}

const { filterUploadFiles, isVideoParsingEnabled, isVideoParsingReady } = loadUploadSources()

const file = (name: string, size = 10): FakeFile => ({ name, size })
// Array.from runs in this realm, so the assertion compares host arrays, not the
// vm realm's arrays.
const names = (files: FakeFile[]) => Array.from(files, f => f.name)

test('filterUploadFiles skips videos and counts them when allowVideo is off', () => {
  const result = filterUploadFiles(
    [file('notes.md'), file('lecture.mp4'), file('trailer.mov'), file('readme.txt')],
    { allowVideo: false },
  )

  assert.deepEqual(names(result.validFiles), ['notes.md', 'readme.txt'])
  assert.equal(result.videoFilteredCount, 2)
  assert.equal(result.skippedCount, 0)
  assert.equal(result.hiddenFileCount, 0)
})

test('filterUploadFiles defaults to filtering videos when allowVideo is absent', () => {
  const result = filterUploadFiles([file('lesson.MP4'), file('notes.md')])

  assert.deepEqual(names(result.validFiles), ['notes.md'])
  assert.equal(result.videoFilteredCount, 1)
})

test('filterUploadFiles admits videos into validFiles when allowVideo is on', () => {
  // The parser-engine whitelist never lists video extensions: the filter must
  // still admit the family instead of falling through to the type check.
  const result = filterUploadFiles(
    [file('notes.md'), file('lecture.mp4'), file('trailer.mov')],
    { allowVideo: true, supportedFileTypes: new Set(['md', 'txt']) },
  )

  assert.deepEqual(names(result.validFiles), ['notes.md', 'lecture.mp4', 'trailer.mov'])
  assert.equal(result.videoFilteredCount, 0)
  assert.equal(result.skippedCount, 0)
})

test('filterUploadFiles keeps the size guard for videos when allowVideo is on', () => {
  const result = filterUploadFiles(
    [file('small.mp4', 10), file('huge.mp4', MAX_FILE_BYTES + 1)],
    { allowVideo: true, multiFile: true },
  )

  assert.deepEqual(names(result.validFiles), ['small.mp4'])
  assert.equal(result.videoFilteredCount, 0)
  assert.equal(result.skippedCount, 1)
})

test('filterUploadFiles still rejects unsupported non-video types when allowVideo is on', () => {
  const result = filterUploadFiles([file('payload.exe'), file('lecture.mp4')], {
    allowVideo: true,
    supportedFileTypes: new Set(['md']),
  })

  assert.deepEqual(names(result.validFiles), ['lecture.mp4'])
  assert.equal(result.skippedCount, 1)
})

test('isVideoParsingEnabled follows video_config.enabled and treats a missing config as off', () => {
  for (const [kbInfo, expected] of [
    [{ video_config: { enabled: true } }, true],
    [{ video_config: { enabled: false } }, false],
    [{ video_config: null }, false],
    [{}, false],
    [null, false],
    [undefined, false],
  ] as Array<[unknown, boolean]>) {
    assert.equal(isVideoParsingEnabled(kbInfo), expected, JSON.stringify(kbInfo))
  }
})

test('isVideoParsingReady mirrors the backend model requirement per mode', () => {
  const ready: Array<[string, unknown]> = [
    [
      'audio_only with the video ASR model',
      { video_config: { enabled: true, mode: 'audio_only', model_id: 'asr-1' } },
    ],
    [
      'keyframes with the video section VLM',
      {
        video_config: {
          enabled: true,
          mode: 'keyframes',
          vlm_config: { enabled: true, model_id: 'vlm-video' },
        },
      },
    ],
    [
      'audio_and_keyframes satisfied by frames alone',
      {
        video_config: {
          enabled: true,
          mode: 'audio_and_keyframes',
          vlm_config: { enabled: true, model_id: 'vlm-video' },
        },
      },
    ],
  ]

  const notReady: Array<[string, unknown]> = [
    ['the switch is off', { video_config: { enabled: false, model_id: 'asr-1' } }],
    ['audio_only without any model', { video_config: { enabled: true, mode: 'audio_only' } }],
    [
      'audio_only with only the audio section model',
      {
        video_config: { enabled: true, mode: 'audio_only' },
        asr_config: { enabled: true, model_id: 'asr-audio' },
      },
    ],
    [
      'audio_only with an ASR config that has no model',
      {
        video_config: { enabled: true, mode: 'audio_only' },
        asr_config: { enabled: true, model_id: '' },
      },
    ],
    ['keyframes without the VLM', { video_config: { enabled: true, mode: 'keyframes' } }],
    [
      'keyframes with a video VLM that has no model',
      {
        video_config: {
          enabled: true,
          mode: 'keyframes',
          vlm_config: { enabled: true, model_id: '' },
        },
      },
    ],
    [
      'keyframes with only the image section VLM',
      {
        video_config: { enabled: true, mode: 'keyframes' },
        vlm_config: { enabled: true, model_id: 'vlm-1' },
      },
    ],
    [
      'keyframes with a legacy image-section VLM',
      {
        video_config: { enabled: true, mode: 'keyframes' },
        vlm_config: { model_name: 'qwen-vl', base_url: 'http://vlm' },
      },
    ],
    [
      'keyframes with a video VLM that is not enabled',
      {
        video_config: {
          enabled: true,
          mode: 'keyframes',
          vlm_config: { enabled: false, model_id: 'vlm-video' },
        },
      },
    ],
    ['an old knowledge base without video_config', {}],
  ]

  for (const [label, kbInfo] of ready) {
    assert.equal(isVideoParsingReady(kbInfo), true, label)
  }
  for (const [label, kbInfo] of notReady) {
    assert.equal(isVideoParsingReady(kbInfo), false, label)
  }
})
