import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const sourceRoot = resolve(here, '..')
const readSource = (...segments) => readFileSync(join(sourceRoot, ...segments), 'utf8')

const inputField = readSource('components', 'Input-field.vue')
const suggestedQuestions = readSource('components', 'css', 'suggested-questions.less')
const chatView = readSource('views', 'chat', 'index.vue')
const createChatView = readSource('views', 'creatChat', 'creatChat.vue')
const knowledgeBaseView = readSource('views', 'knowledge', 'KnowledgeBase.vue')
const platformView = readSource('views', 'platform', 'index.vue')
const uiStore = readSource('stores', 'ui.ts')

test('chat input is container-sized on desktop, tablet, and phone viewports', () => {
  assert.match(
    inputField,
    /\.answers-input\s*\{[^}]*width:\s*100%;[^}]*min-width:\s*0;[^}]*box-sizing:\s*border-box;/,
  )
  assert.match(
    inputField,
    /\.rich-input-container\s*\{[^}]*width:\s*100%;[^}]*max-width:\s*960px;[^}]*min-width:\s*0;[^}]*box-sizing:\s*border-box;/,
  )
  assert.match(
    inputField,
    /@media \(max-width: 768px\)[\s\S]*?\.control-left\s*\{[^}]*overflow-x:\s*auto;/,
  )
})

test('chat entry points no longer impose fixed widths or translations on InputField', () => {
  for (const source of [createChatView, knowledgeBaseView]) {
    assert.doesNotMatch(source, /\.answers-input\s*\{[^}]*translateX\(-\d+px\)/)
    assert.doesNotMatch(source, /:deep\(\.t-textarea__inner\)\s*\{[^}]*width:\s*\d+px\s*!important/)
  }
})

test('page shells can shrink below their former desktop minimum widths', () => {
  assert.match(platformView, /\.main\s*\{[^}]*min-width:\s*0;/)
  assert.doesNotMatch(platformView, /\.main\s*\{[^}]*min-width:\s*600px;/)
  assert.match(chatView, /\.chat\s*\{[^}]*min-width:\s*0;/)
  assert.doesNotMatch(chatView, /\.chat\s*\{[^}]*min-width:\s*400px;/)
})

test('suggested questions fit a narrow viewport without horizontal overflow', () => {
  assert.match(
    suggestedQuestions,
    /\.suggested-questions-container\s*\{[^}]*width:\s*100%;[^}]*min-width:\s*0;[^}]*box-sizing:\s*border-box;/,
  )
  assert.match(
    suggestedQuestions,
    /@media \(max-width: 600px\)[\s\S]*?grid-template-columns:\s*minmax\(0,\s*1fr\);/,
  )
})

test('first-time tablet and phone visits start with the sidebar collapsed', () => {
  assert.match(uiStore, /localStorage\.getItem\('sidebar_collapsed'\)/)
  assert.match(uiStore, /window\.matchMedia\('\(max-width: 768px\)'\)\.matches/)
  assert.match(uiStore, /sidebarCollapsed:\s*getInitialSidebarCollapsed\(\)/)
})
