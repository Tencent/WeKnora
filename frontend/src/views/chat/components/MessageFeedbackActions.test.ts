import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const component = readFileSync(new URL('./MessageFeedbackActions.vue', import.meta.url), 'utf8')

test('renders the dislike reason dialog above the chat scroll container', () => {
  assert.match(component, /<t-dialog[\s\S]*attach="body"/)
  assert.match(component, /<t-dialog[\s\S]*:z-index="2500"/)
})

test('only offers feedback for completed answers with knowledge references', () => {
  assert.match(component, /props\.session\?\.is_completed/)
  assert.match(component, /props\.session\.knowledge_references\.length > 0/)
})
