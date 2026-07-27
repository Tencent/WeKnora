import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'AgentEditorModal.vue'), 'utf8')

test('agent editor keeps its original modal dimensions', () => {
  assert.match(source, /\.settings-modal\s*{[\s\S]*?width:\s*90vw;/)
  assert.match(source, /\.settings-modal\s*{[\s\S]*?max-width:\s*1100px;/)
  assert.doesNotMatch(source, /\.settings-modal\s*{[\s\S]*?width:\s*min\(1180px/)
})
