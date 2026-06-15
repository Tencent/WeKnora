import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const css = readFileSync(join(here, 'chat-timeline-loading.less'), 'utf8')

test('timeline loading uses shared gray bounce dots on the axis', () => {
  assert.match(css, /\.streaming-loading-node\s*\{[\s\S]*left:\s*-42px/)
  assert.match(css, /width:\s*4px/)
  assert.match(css, /background:\s*var\(--td-text-color-placeholder\)/)
  assert.match(css, /animation:\s*chatTimelineTypingBounce/)
  assert.match(css, /animation-delay:\s*0\.2s/)
})
