import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

/**
  * Entry-style guard: the embed page and the main page are two separate vite
  * entries (index.html -> main.ts, embed.html -> embed-main.ts), and global CSS
  * only arrives through the import lines of whichever entry builds it — sharing a
  * component does not carry a stylesheet across. A missing import is silent: the
  * card loses its host layer (no background, border or shadow, the library's own
  * 36px radius, body text with no --n-* token), the compiler says nothing, and
  * the build stays green.
 */
const A2UI_LIB_SHEET = 'a2ui-vue/dist/a2ui-vue.css'
const HOST_SHEET = 'assets/theme/a2ui-card.css'

const read = (rel) => readFileSync(new URL(rel, import.meta.url), 'utf8')
const entries = () => [['main.ts', read('../../main.ts')], ['embed-main.ts', read('../../embed-main.ts')]]
const lineIndexOf = (src, needle) => src.split('\n').findIndex((line) => line.trimStart().startsWith('import') && line.includes(needle))

test('both entries load the A2UI card stylesheets', () => {
  for (const [name, src] of entries()) {
    for (const sheet of [A2UI_LIB_SHEET, HOST_SHEET]) {
      assert.ok(lineIndexOf(src, sheet) >= 0, `${name} must import ${sheet} — embed cards lose the host style layer without it`)
    }
  }
})

test('the host layer is imported after the library stylesheet, never before', () => {
  for (const [name, src] of entries()) {
    const lib = lineIndexOf(src, A2UI_LIB_SHEET)
    const host = lineIndexOf(src, HOST_SHEET)
    assert.ok(lib >= 0 && host > lib, `${name}: ${HOST_SHEET} must sit after ${A2UI_LIB_SHEET} (source order is the only tiebreaker)`)
  }
})
