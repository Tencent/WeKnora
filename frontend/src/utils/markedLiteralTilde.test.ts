import assert from 'node:assert/strict'
import test from 'node:test'

import { Marked, marked } from 'marked'

import {
  ensureLiteralSingleTildeOnGlobalMarked,
  literalSingleTildeExtension,
} from './markedLiteralTilde.ts'

test('literalSingleTildeExtension keeps ~~strikethrough~~ and leaves single tildes literal', () => {
  const instance = new Marked({ gfm: true })
  instance.use(literalSingleTildeExtension)
  const html = instance.parse('~~a~~ and ~b~ and 2020~2035', { async: false }) as string
  assert.equal((html.match(/<del>/g) || []).length, 1)
  assert.match(html, /<del>a<\/del>/)
  assert.match(html, /~b~/)
  assert.match(html, /2020~2035/)
})

test('ensureLiteralSingleTildeOnGlobalMarked registers on the global marked only once', () => {
  ensureLiteralSingleTildeOnGlobalMarked()
  // Each marked.use wraps tokenizer.del again; a stable reference means no re-wrap.
  const del = marked.defaults.tokenizer?.del
  assert.equal(typeof del, 'function')
  ensureLiteralSingleTildeOnGlobalMarked()
  ensureLiteralSingleTildeOnGlobalMarked()
  assert.equal(marked.defaults.tokenizer?.del, del)
})
