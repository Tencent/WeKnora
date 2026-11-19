import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const theme = readFileSync(join(here, 'chat-hljs-theme.less'), 'utf8')
const tokens = readFileSync(join(here, 'chat-hljs.less'), 'utf8')

function varNames(block) {
  return [...block.matchAll(/--chat-code-[a-z-]+/g)].map((m) => m[0])
}

function themeBlock(selector) {
  const start = theme.indexOf(selector)
  const end = theme.indexOf('}', start)
  return theme.slice(start, end)
}

test('every token variable referenced by the mixin is defined per theme', () => {
  const light = new Set(varNames(themeBlock(':root')))
  const dark = new Set(varNames(themeBlock("html[theme-mode='dark']")))
  for (const name of new Set(varNames(tokens))) {
    assert.ok(light.has(name), `${name} missing from :root in chat-hljs-theme.less`)
    assert.ok(dark.has(name), `${name} missing from dark block in chat-hljs-theme.less`)
  }
})

test('light and dark define the same token variable set', () => {
  const light = new Set(varNames(themeBlock(':root')))
  const dark = new Set(varNames(themeBlock("html[theme-mode='dark']")))
  assert.deepEqual([...dark].sort(), [...light].sort())
})

test('token colors come from variables, not hardcoded hex', () => {
  // The light palette used to be hardcoded here and always beat the global
  // dark override on specificity (#5300); keep every color on variables.
  assert.doesNotMatch(tokens, /#[0-9a-f]{3,8}\b/i)
})
