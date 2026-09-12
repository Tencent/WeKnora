import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./WikiBrowser.vue', import.meta.url), 'utf8')

test('bloom merge replaces stale personal learning state', () => {
  assert.match(source, /existing\.familiar = Boolean\(n\.familiar\)/)
  assert.match(source, /else delete existing\.learning/)
  assert.doesNotMatch(source, /if \(n\.familiar\) existing\.familiar = true/)
})
