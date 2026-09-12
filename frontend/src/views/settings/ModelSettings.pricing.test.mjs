import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./ModelSettings.vue', import.meta.url), 'utf8')

test('chat model updates explicitly persist disabled pricing', () => {
  assert.match(source, /\.\.\.\(editingModel\.value\?\.parameters\?\.extra_config \|\| \{\}\)/)
  assert.match(source, /if \(saveType === 'chat'\) \{/)
  assert.match(source, /extraConfig\.pricing_enabled = String\(modelData\.pricingEnabled === true\)/)
  assert.match(source, /if \(modelData\.pricingEnabled\) \{/)
})
