import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'

const componentSource = readFileSync(resolve(import.meta.dirname, 'AgentStreamDisplay.vue'), 'utf8')

test('agent stream display only mounts image preview when a preview url exists', () => {
  assert.match(componentSource, /<picturePreview\s+v-if="imagePreviewVisible && imagePreviewUrl"/)
})

test('agent stream display exposes an opt-out for protected image hydration', () => {
  assert.match(componentSource, /hydrateProtectedImages\?: boolean/)
  assert.match(componentSource, /props\.hydrateProtectedImages !== false/)
})
