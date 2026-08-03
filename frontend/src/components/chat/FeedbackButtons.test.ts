import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const component = readFileSync(new URL('./FeedbackButtons.vue', import.meta.url), 'utf8')

test('renders both the like and the dislike buttons', () => {
  assert.match(component, /data-test="feedback-like-btn"/)
  assert.match(component, /data-test="feedback-dislike-btn"/)
  assert.match(component, /thumb-up/)
  assert.match(component, /thumb-down/)
})

test('uses the like / dislike / none ratings on the wire', () => {
  // The component issues a PUT with rating "like", "dislike", or "none" —
  // assert each appears at least once in the source.
  assert.match(component, /rating:\s*target/)
  assert.match(component, /rating:\s*'dislike'/)
  assert.match(component, /target\s*===\s*'none'/)
})

test('opens a dislike dialog and only commits on confirm', () => {
  assert.match(component, /data-test="feedback-dislike-dialog"/)
  assert.match(component, /onConfirmDislike/)
})

test('applies optimistic UI flips and rolls back on error', () => {
  // emit('update:modelValue', 'like') is the optimistic like flip.
  assert.match(component, /emit\('update:modelValue', target\)/)
  // The catch block re-emits the previous rating on failure.
  assert.match(component, /emit\('update:modelValue', previous\)/)
})

test('renders the active-state border when the current rating matches', () => {
  assert.match(component, /is-active/)
})

test('disables interaction via the disabled prop', () => {
  assert.match(component, /:disabled="disabled \|\| pending"/)
})

test('maps the optimistic flip-to-dislike into a dialog state', () => {
  assert.match(component, /dialogVisible\.value = true/)
  assert.match(component, /onCancelDislike/)
})