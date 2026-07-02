import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '..')

const inputField = readFileSync(join(root, 'components', 'Input-field.vue'), 'utf8')
const chatView = readFileSync(join(root, 'views', 'chat', 'index.vue'), 'utf8')
const platformView = readFileSync(join(root, 'views', 'platform', 'index.vue'), 'utf8')
const createChatView = readFileSync(join(root, 'views', 'creatChat', 'creatChat.vue'), 'utf8')
const knowledgeBaseView = readFileSync(join(root, 'views', 'knowledge', 'KnowledgeBase.vue'), 'utf8')
const uiStore = readFileSync(join(root, 'stores', 'ui.ts'), 'utf8')

test('input field width is constrained by its container instead of fixed textarea widths', () => {
  assert.match(inputField, /\.answers-input\s*\{[\s\S]*width:\s*100%;[\s\S]*box-sizing:\s*border-box;[\s\S]*padding:\s*0 16px;/)
  assert.match(inputField, /\.rich-input-container\s*\{[\s\S]*width:\s*100%;[\s\S]*max-width:\s*800px;[\s\S]*box-sizing:\s*border-box;/)
  assert.doesNotMatch(inputField, /width:\s*min\(800px,\s*95vw\)/)
})

test('parent chat entry points do not override input field with fixed mobile widths', () => {
  for (const source of [createChatView, knowledgeBaseView]) {
    assert.doesNotMatch(source, /:deep\(\.t-textarea__inner\)\s*\{[\s\S]*width:\s*\d+px\s*!important/)
    assert.doesNotMatch(source, /\.answers-input\s*\{[\s\S]*translateX\(-\d+px\)/)
  }
})

test('chat page releases desktop minimum width on phone-sized viewports', () => {
  assert.match(chatView, /@media \(max-width:\s*600px\)\s*\{[\s\S]*\.chat\s*\{[\s\S]*min-width:\s*0;/)
})

test('platform shell does not keep desktop width constraints on phone-sized viewports', () => {
  assert.match(platformView, /@media \(max-width:\s*600px\)\s*\{[\s\S]*\.main\s*\{[\s\S]*min-width:\s*0;/)
  assert.match(uiStore, /window\.innerWidth\s*<=\s*600/)
  assert.match(uiStore, /storedSidebarCollapsed\s*===\s*null\s*\?\s*isPhoneViewport/)
})
