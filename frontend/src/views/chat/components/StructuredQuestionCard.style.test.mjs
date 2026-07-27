import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'StructuredQuestionCard.vue'), 'utf8')
const style = readFileSync(join(here, 'structured-question-card.less'), 'utf8')

test('structured question shows mode, progress, and remaining count', () => {
	assert.match(source, /structuredQuestionProgress/)
  assert.match(source, /question-progress/)
  assert.match(source, /question-remaining/)
  assert.match(source, /questionIndex/)
  assert.match(source, /questionTotal/)
})

test('typed modes use stable TDesign controls', () => {
	assert.match(source, /<t-input /)
	assert.match(source, /<t-textarea/)
	assert.match(source, /<t-input-number/)
	assert.match(source, /<t-date-picker/)
	assert.match(style, /\.typed-control[\s\S]*min-height:\s*40px/)
})

test('single and multiple modes use native TDesign choice controls', () => {
  assert.match(source, /<t-radio-group/)
  assert.match(source, /<t-radio/)
  assert.match(source, /<t-checkbox-group/)
  assert.match(source, /<t-checkbox/)
  assert.match(source, /otherSelected/)
})

test('question option text wraps without horizontal overflow', () => {
  assert.match(style, /\.structured-question-card[\s\S]*max-width:\s*680px/)
  assert.match(style, /overflow-wrap:\s*anywhere/)
  assert.match(style, /word-break:\s*break-word/)
  assert.match(style, /min-width:\s*0/)
})

test('long option lists expand fully and use the chat page scroll container', () => {
  const optionsRule = style.match(/\.structured-question-options\s*\{([^}]*)\}/)?.[1] || ''
  assert.doesNotMatch(optionsRule, /max-height/)
  assert.doesNotMatch(optionsRule, /overflow-y/)
  assert.doesNotMatch(optionsRule, /overscroll-behavior/)
  const streamSource = readFileSync(join(here, 'AgentStreamDisplay.vue'), 'utf8')
  assert.match(streamSource, /hasActiveStructuredQuestion/)
  assert.match(streamSource, /!isConversationDone\s*&&\s*!hasActiveStructuredQuestion/)
})

test('question actions remain usable on mobile and keyboard focus is visible', () => {
  assert.match(style, /@media \(max-width:\s*600px\)/)
  assert.match(style, /\.question-actions[\s\S]*flex-wrap:\s*wrap/)
  assert.match(style, /:focus-visible/)
  assert.match(source, /focusFirstOption/)
})

test('question card supports submit locking and inline failures', () => {
  assert.match(source, /localSubmitted/)
  assert.match(source, /submitting/)
  assert.match(source, /submitError/)
  assert.match(source, /resolveUserInput/)
})
