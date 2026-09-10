import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const inputField = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')
const createChat = readFileSync(new URL('../views/creatChat/creatChat.vue', import.meta.url), 'utf8')
const chat = readFileSync(new URL('../views/chat/index.vue', import.meta.url), 'utf8')
const platform = readFileSync(new URL('../views/platform/index.vue', import.meta.url), 'utf8')
const uiStore = readFileSync(new URL('../stores/ui.ts', import.meta.url), 'utf8')

test('platform and chat shells can shrink to a mobile viewport', () => {
  assert.match(platform, /@media \(max-width: 768px\)[\s\S]*?\.main\s*\{[\s\S]*?min-width:\s*0/)
  assert.match(chat, /@media \(max-width: 768px\)[\s\S]*?\.chat[\s\S]*?min-width:\s*0/)
})

test('new chat input follows its container instead of fixed viewport widths', () => {
  assert.match(createChat, /\.dialogue-wrap\s*\{[\s\S]*?min-width:\s*0/)
  assert.match(createChat, /\.dialogue-answers\s*\{[\s\S]*?min-width:\s*0/)
  assert.doesNotMatch(createChat, /width:\s*(?:654|500|340|300)px\s*!important/)
  assert.doesNotMatch(createChat, /translateX\(-(?:329|250)px\)/)
})

test('mobile input controls stay on one compact scrollable row', () => {
  assert.match(inputField, /@media \(max-width: 768px\)[\s\S]*?\.answers-input\s*\{[\s\S]*?padding:\s*0 12px/)
  assert.match(inputField, /\.control-left\s*\{[\s\S]*?overflow-x:\s*auto/)
  assert.match(inputField, /\.agent-mode-text\s*\{[\s\S]*?text-overflow:\s*ellipsis/)
})

test('mobile model selector shrinks without covering the send button', () => {
  assert.match(inputField, /class="model-selector-trigger" :title="selectedModelDisplayName"/)
  assert.match(inputField, /\.model-display\s*\{[\s\S]*?flex:\s*1 1 auto;[\s\S]*?min-width:\s*0/)
  assert.match(inputField, /\.model-selector-trigger\s*\{[\s\S]*?width:\s*100%;[\s\S]*?min-width:\s*0/)
  assert.match(inputField, /\.model-selector-name\s*\{[\s\S]*?min-width:\s*0/)
})

test('mobile visits start with a collapsed sidebar even after a desktop preference', () => {
  assert.match(uiStore, /localStorage\.getItem\(SIDEBAR_COLLAPSED_KEY\)/)
  assert.match(
    uiStore,
    /if \(window\.matchMedia\('\(max-width: 768px\)'\)\.matches\)\s*\{\s*return true/,
  )
})
