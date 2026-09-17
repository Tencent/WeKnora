import assert from 'node:assert/strict'
import test from 'node:test'
import { rewindPrefillText, shouldApplyRewindLocally } from './rewindView'

test('applies rewind UI only while still on the source session', () => {
  assert.equal(shouldApplyRewindLocally('sess-1', 'sess-1'), true)
  assert.equal(shouldApplyRewindLocally('sess-2', 'sess-1'), false)
  assert.equal(shouldApplyRewindLocally('', 'sess-1'), false)
})

test('prefills only the user rewind point', () => {
  assert.equal(rewindPrefillText('user', 'rewrite this'), 'rewrite this')
  assert.equal(rewindPrefillText('assistant', 'keep this answer'), '')
  assert.equal(rewindPrefillText('user', undefined), '')
})
