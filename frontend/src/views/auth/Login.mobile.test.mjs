import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./Login.vue', import.meta.url), 'utf8')
const tabletStyles = source.slice(
  source.indexOf('@media (max-width: 768px)'),
  source.indexOf('@media (max-width: 480px)'),
)
const phoneStyles = source.slice(source.indexOf('@media (max-width: 480px)'))

test('phone login puts the form before the showcase and reserves room for the header', () => {
  assert.match(tabletStyles, /\.login-layout\s*\{[\s\S]*?flex-direction:\s*column;[\s\S]*?padding-top:\s*64px;/)
  assert.match(tabletStyles, /\.form-section\s*\{[\s\S]*?order:\s*-1;/)
})

test('narrow login headers use icon-only links to avoid overlapping the logo', () => {
  assert.match(phoneStyles, /\.header-links\s*\{[\s\S]*?\.link-text\s*\{\s*display:\s*none;/)
})