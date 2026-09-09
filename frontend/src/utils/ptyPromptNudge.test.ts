import assert from 'node:assert/strict'
import test from 'node:test'
import {
  PTY_PROMPT_NUDGE,
  xtermBufferLooksEmpty,
} from './ptyPromptNudge'

test('prompt nudge is readline clear-screen, not enter', () => {
  assert.equal(PTY_PROMPT_NUDGE, '\x0c')
  assert.notEqual(PTY_PROMPT_NUDGE, '\r')
  assert.notEqual(PTY_PROMPT_NUDGE, '\n')
})

test('xtermBufferLooksEmpty is true when every visible row is blank', () => {
  const rows = ['', '   ', '\t']
  assert.equal(xtermBufferLooksEmpty((i) => rows[i], rows.length), true)
})

test('xtermBufferLooksEmpty is false when any row has text', () => {
  const rows = ['', 'root@host:workspace#']
  assert.equal(xtermBufferLooksEmpty((i) => rows[i], rows.length), false)
})
