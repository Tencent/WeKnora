import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const knowledgeBase = readFileSync(new URL('./KnowledgeBase.vue', import.meta.url), 'utf8')
const chat = readFileSync(new URL('../chat/index.vue', import.meta.url), 'utf8')
const settings = readFileSync(new URL('../../stores/settings.ts', import.meta.url), 'utf8')

test('questions started in a folder use that folder and knowledge base as retrieval scope', () => {
  assert.match(knowledgeBase, /settingsStore\.selectKnowledgeBases\(\[kbId\.value\]\)/)
  assert.match(knowledgeBase, /settingsStore\.clearFolders\(\)/)
  assert.match(knowledgeBase, /id: currentFolderId\.value/)
  assert.match(knowledgeBase, /kbId: kbId\.value/)
})

test('restored folder scope remains usable after reloading a session', () => {
  assert.match(settings, /restoredKbIds\.length === 1 \? restoredKbIds\[0\] : ""/)
  assert.match(chat, /!f\.kbId \|\| kbIdSet\.has\(f\.kbId\)/)
})
