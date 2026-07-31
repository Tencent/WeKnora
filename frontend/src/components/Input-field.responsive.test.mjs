import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const inputField = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')
const createChat = readFileSync(new URL('../views/creatChat/creatChat.vue', import.meta.url), 'utf8')
const chat = readFileSync(new URL('../views/chat/index.vue', import.meta.url), 'utf8')
const platform = readFileSync(new URL('../views/platform/index.vue', import.meta.url), 'utf8')
const knowledgeBase = readFileSync(new URL('../views/knowledge/KnowledgeBase.vue', import.meta.url), 'utf8')

test('chat composer uses container-driven sizing on narrow viewports', () => {
  assert.match(inputField, /\.answers-input \{[\s\S]*min-width: 0;[\s\S]*box-sizing: border-box;/)
  assert.match(inputField, /\.rich-input-container \{[\s\S]*min-width: 0;[\s\S]*box-sizing: border-box;/)
  assert.match(inputField, /@media \(max-width: 1024px\) \{[\s\S]*padding-inline: 16px;/)
  assert.match(inputField, /@media \(max-width: 600px\) \{[\s\S]*padding-inline: 12px;/)
  assert.match(inputField, /\.control-left \{[\s\S]*overflow-x: auto;/)
})

test('host pages do not reintroduce fixed-width composer offsets', () => {
  assert.doesNotMatch(createChat, /\.answers-input \{\s*transform: translateX\(-\d+px\)/)
  assert.doesNotMatch(createChat, /t-textarea__inner\) \{\s*width: \d+px !important/)
  assert.doesNotMatch(knowledgeBase, /\.answers-input \{\s*transform: translateX\(-\d+px\)/)
  assert.doesNotMatch(knowledgeBase, /t-textarea__inner\) \{\s*width: \d+px !important/)
})

test('platform and chat shells release desktop minimum widths on compact tablets', () => {
  assert.match(platform, /@media \(max-width: 768px\) \{[\s\S]*\.main \{[\s\S]*min-width: 0;/)
  assert.match(chat, /@media \(max-width: 768px\) \{[\s\S]*\.chat,[\s\S]*max-width: 100%;[\s\S]*min-width: 0;/)
})
