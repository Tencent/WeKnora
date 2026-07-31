import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const read = (relativePath) =>
  readFileSync(new URL(relativePath, import.meta.url), 'utf8')

test('mobile viewport can shrink below the former desktop minimums', () => {
  const platform = read('./platform/index.vue')
  const chat = read('./chat/index.vue')

  assert.match(platform, /\.main\s*\{[\s\S]*?min-width:\s*0;/)
  assert.doesNotMatch(platform, /\.main\s*\{[\s\S]*?min-width:\s*600px;/)
  assert.match(chat, /\.chat\s*\{[\s\S]*?min-width:\s*0;/)
  assert.doesNotMatch(chat, /\.chat\s*\{[\s\S]*?min-width:\s*400px;/)
})

test('input and create-chat layouts use responsive widths instead of fixed phone widths', () => {
  const input = read('../components/Input-field.vue')
  const createChat = read('./creatChat/creatChat.vue')
  const chat = read('./chat/index.vue')

  assert.match(input, /width:\s*min\(960px,\s*100%\)/)
  assert.match(input, /@media screen and \(max-width:\s*767px\)/)
  assert.match(input, /\.control-left\s*\{[\s\S]*?overflow-x:\s*auto;/)
  assert.match(createChat, /@media screen and \(max-width:\s*767px\)/)
  assert.doesNotMatch(createChat, /width:\s*(?:300|340|500|654)px\s*!important/)
  assert.doesNotMatch(createChat, /translateX\(-(?:164|182|250|329)px\)/)
  assert.match(chat, /\.chat:not\(\.is-embedded\)\s*\{[\s\S]*?max-width:\s*calc\(100vw - 60px\)/)
})

test('phone navigation preserves usable Android space', () => {
  const menu = read('../components/menu.vue')

  assert.match(menu, /@media screen and \(max-width:\s*767px\)/)
  assert.match(menu, /\.aside_box\s*\{[\s\S]*?min-width:\s*60px;[\s\S]*?width:\s*60px;/)
  assert.match(menu, /\.logo_row\s*\{[\s\S]*?justify-content:\s*center;/)
  assert.doesNotMatch(menu, /\.logo_row,\s*:deep\(\.tenant-selector\)/)
})
