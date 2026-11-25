import assert from 'node:assert/strict'
import test from 'node:test'
import { highlightText } from './useHighlight.ts'

const mark = (text: string) => `<mark class="search-highlight">${text}</mark>`

test('matches keywords only in the input text', () => {
  assert.equal(highlightText('diagram class search mark', 'diagram class search mark'),
    [mark('diagram'), mark('class'), mark('search'), mark('mark')].join(' '))
})

test('escapes both matched and unmatched text without matching HTML entities', () => {
  assert.equal(highlightText('<b>& amp</b>', 'amp &'), `&lt;b&gt;${mark('&amp;')} ${mark('amp')}&lt;/b&gt;`)
})

test('treats query words as literals and handles shared prefixes', () => {
  assert.equal(highlightText('C++ and CLASS', 'c c++ class'), `${mark('C++')} and ${mark('CLASS')}`)
})
