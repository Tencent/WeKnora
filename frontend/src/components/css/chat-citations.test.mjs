import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const css = readFileSync(join(here, 'chat-citations.less'), 'utf8')

test('citation pills use compact baseline-aligned source styling', () => {
  assert.match(css, /border-radius:\s*999px/)
  assert.match(css, /font-size:\s*0\.72em/)
  assert.match(css, /box-shadow:\s*inset 0 0 0 1px/)
})
