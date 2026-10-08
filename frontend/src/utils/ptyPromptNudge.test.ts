import assert from 'node:assert/strict'
import test from 'node:test'
import { xtermBufferLooksEmpty } from './ptyPromptNudge'

test('xtermBufferLooksEmpty is true when every visible row is blank', () => {
  const rows = ['', '   ', '\t']
  assert.equal(xtermBufferLooksEmpty((i) => rows[i], rows.length), true)
})

test('xtermBufferLooksEmpty is false when any row has text', () => {
  const rows = ['', 'root@host:workspace#']
  assert.equal(xtermBufferLooksEmpty((i) => rows[i], rows.length), false)
})
