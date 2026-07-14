import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'docInfo.vue'), 'utf8')

test('timeline references neutralize brand colors via local css variables', () => {
  assert.match(source, /&\.refer-timeline \{[\s\S]*--td-brand-color:\s*var\(--td-text-color-placeholder\)/)
  assert.match(source, /\.doc-chunk-item \.doc-chunk-text:hover/)
})

test('doc header uses right/down chevron on the outer title only', () => {
  assert.match(source, /showReferBox \? 'chevron-down' : 'chevron-right'/)
  assert.doesNotMatch(source, /class="doc-group-arrow"/)
})
