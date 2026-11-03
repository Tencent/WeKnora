import assert from 'node:assert/strict'
import test from 'node:test'

import { HOST_APPROVAL_MODES, modeAfterLoad, parseHostApprovalMode, readModeResponse } from './hostApprovalMode'

test('only ask and auto can be chosen', () => {
  assert.deepEqual([...HOST_APPROVAL_MODES], ['ask', 'auto'])
})

test('anything unknown reads as ask', () => {
  assert.equal(parseHostApprovalMode('auto'), 'auto')
  assert.equal(parseHostApprovalMode('ask'), 'ask')
  assert.equal(parseHostApprovalMode('full'), 'ask')
  assert.equal(parseHostApprovalMode(undefined), 'ask')
})

test('the API response is read defensively', () => {
  assert.deepEqual(readModeResponse({ data: { mode: 'auto', default: 'ask' } }), { mode: 'auto', defaultMode: 'ask' })
  assert.deepEqual(readModeResponse({}), { mode: 'ask', defaultMode: 'ask' })
})

test('a stale or failed load does not replace auto with ask', () => {
  const current = { sessionId: 'sess-b', generation: 2, mode: 'auto' as const, established: true }
  assert.equal(modeAfterLoad(current, { sessionId: 'sess-a', generation: 1, mode: 'ask' }), 'auto')
  assert.equal(modeAfterLoad(current, { sessionId: 'sess-b', generation: 1, mode: 'ask' }), 'auto')
  assert.equal(modeAfterLoad(current, { sessionId: 'sess-b', generation: 2, mode: null }), 'auto')
})

test('a failed first load still defaults to ask', () => {
  const current = { sessionId: 'sess-a', generation: 1, mode: 'ask' as const, established: false }
  assert.equal(modeAfterLoad(current, { sessionId: 'sess-a', generation: 1, mode: null }), 'ask')
})

test('a current load applies the returned mode', () => {
  const current = { sessionId: 'sess-a', generation: 1, mode: 'ask' as const, established: false }
  assert.equal(modeAfterLoad(current, { sessionId: 'sess-a', generation: 1, mode: 'auto' }), 'auto')
})
