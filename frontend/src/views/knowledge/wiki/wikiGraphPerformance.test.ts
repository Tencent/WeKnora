import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const filename = fileURLToPath(new URL('./WikiBrowser.vue', import.meta.url))
const source = readFileSync(filename, 'utf8')

test('graph zoom removes collision-hidden node labels from SVG layout', () => {
  const updateLabelsVisibility = source.slice(
    source.indexOf('function updateLabelsVisibility()'),
    source.indexOf('refreshGraphLabels = updateLabelsVisibility'),
  )

  assert.match(updateLabelsVisibility, /apply:\s*\(\)\s*=>\s*\{\s*text\.setAttribute\('font-size'/)
  assert.match(updateLabelsVisibility, /if \(visible\.has\(candidate\.slug\)\) candidate\.apply\(\)/)
  assert.match(updateLabelsVisibility, /text\.style\.display = shown \? '' : 'none'/)
  assert.doesNotMatch(updateLabelsVisibility, /text\.style\.opacity =/)
})