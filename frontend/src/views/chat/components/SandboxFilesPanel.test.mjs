import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const panel = readFileSync(new URL('./SandboxFilesPanel.vue', import.meta.url), 'utf8')
const api = readFileSync(new URL('../../../api/chat/sandbox-files.ts', import.meta.url), 'utf8')

test('live files use one relative-path API for every operation', () => {
  assert.match(api, /encodeURIComponent\(sessionId\)/)
  assert.match(api, /new URLSearchParams\(\{ path: relativePath \}\)/)
  assert.match(api, /form\.append\('path', relativePath\)/)
  assert.match(api, /\{ source, target \}/)
  assert.doesNotMatch(api, /\/workspace\/output/)
})

test('file panel exposes only the scoped browse workflow', () => {
  for (const operation of [
    'listSandboxLiveFiles',
    'downloadSandboxLiveFile',
    'uploadSandboxLiveFile',
    'renameSandboxLiveFile',
    'deleteSandboxLiveFile',
  ]) {
    assert.match(panel, new RegExp(operation))
  }
  assert.match(panel, /entry\.type !== 'other'/)
  assert.match(panel, /nextName\.includes\('\/'\)/)
  assert.doesNotMatch(panel, /v-html|\/workspace\/output|desktop|editor/i)
})
