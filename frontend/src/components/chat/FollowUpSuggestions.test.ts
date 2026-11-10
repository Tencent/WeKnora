import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const component = readFileSync(new URL('./FollowUpSuggestions.vue', import.meta.url), 'utf8')

test('does not insert a separate status row while the first suggestions are loading', () => {
  assert.match(component, /<transition name="follow-up-card">/)
  assert.match(component, /v-if="suggestionSet\?\.status === 'ready'"/)
  assert.doesNotMatch(component, /follow-ups-loading/)
  assert.doesNotMatch(component, /follow-ups__skeletons/)
})

test('keeps the existing follow-up card visible while regenerating', () => {
  assert.match(component, /:disabled="loading"/)
  assert.match(component, /loading \? 'loading' : 'refresh'/)
  assert.match(component, /class="follow-ups__list"/)
})

test('carries the lightbulb into the expanding question card', () => {
  assert.match(component, /class="follow-ups__title"/)
  assert.match(component, /name="lightbulb"/)
  assert.match(component, /\.follow-up-card-enter-from/)
  assert.match(component, /clip-path: inset\(0 0 55%/)
})
