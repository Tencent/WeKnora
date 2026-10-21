import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

// Regression guard for #4545: the composer (.answers-input) is absolutely
// positioned above the transcript, so the inline selected-tags row must not
// grow without bound when an agent has many knowledge bases selected — the
// input box would keep growing upward and occlude the conversation.
const inputField = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')

const tagsCss = () => {
  const start = inputField.indexOf('.selected-tags-inline {')
  assert.ok(start >= 0, 'expected a .selected-tags-inline style block')
  const end = inputField.indexOf('.rich-input-container:has(.selected-tags-inline)', start)
  assert.ok(end > start, 'expected the tags style block to be followed by the textarea padding rule')
  return inputField.slice(start, end)
}

test('selected tags area is height-capped so many chips cannot occlude the conversation', () => {
  const max = tagsCss().match(/max-height:\s*(\d+)px/)
  assert.ok(max, '.selected-tags-inline must declare a max-height')
  const px = Number(max[1])
  assert.ok(px > 0 && px <= 120, `max-height ${px}px must stay within a few chip rows (<= 120px)`)
})

test('selected tags area scrolls internally instead of pushing the composer taller', () => {
  assert.match(tagsCss(), /overflow-y:\s*auto/)
})
