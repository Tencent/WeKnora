import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'StreamRecoveryProgress.vue'), 'utf8')

test('stream recovery progress exposes a localized live status', () => {
  assert.match(source, /role="status"/)
  assert.match(source, /aria-live="polite"/)
  assert.match(source, /t\('chat\.restoringGenerationProgress'\)/)
})

test('stream recovery progress uses a reduced-motion-safe spinner', () => {
  assert.match(source, /stream-recovery-progress__spinner/)
  assert.match(source, /@media \(prefers-reduced-motion: reduce\)/)
})
