import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const __dirname = dirname(fileURLToPath(import.meta.url))

test('batch reparse confirms latest process config before enqueueing', () => {
  const component = readFileSync(join(__dirname, 'KnowledgeBase.vue'), 'utf8')

  assert.match(component, /uploadConfirmStore\.open\(\{\s*mode: 'reparse'/s)
  assert.match(component, /processOverrides: null/)
  assert.match(component, /processConfig = result\.processConfig/)
  assert.match(component, /batchReparseKnowledge\(kbId\.value, ids, processConfig\)/)
})

test('reparse confirmation dialog summarizes every selected file type', () => {
  const component = readFileSync(join(__dirname, 'components', 'UploadConfirmDialog.vue'), 'utf8')

  assert.match(component, /props\.reparsePreview\?\.fileTypes/)
  assert.match(component, /set\.add\(ext\)/)
})

test('batch reparse action opens the config dialog without a second popconfirm', () => {
  const component = readFileSync(join(__dirname, 'components', 'DocumentBatchBar.vue'), 'utf8')

  assert.match(component, /@click\.stop="emit\('reparse'\)"/)
  assert.doesNotMatch(component, /confirmBatchReparseDocument/)
})
