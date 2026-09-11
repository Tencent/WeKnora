import assert from 'node:assert/strict'
import test from 'node:test'
import {
  appendTerminalPtyId,
  decideTerminalReconnect,
  terminalReadyPtyId,
} from './sandboxTerminalReconnect'

test('close before ready enters error without automatic reconnect', () => {
  const decision = decideTerminalReconnect({
    readyReceived: false,
    reattachable: true,
    status: 'connecting',
  })

  assert.deepEqual(decision, { status: 'error', shouldReconnect: false })
})

test('non-reattachable ready clears the PTY id and does not reconnect', () => {
  const storedPtyId = terminalReadyPtyId(false, 999)
  const decision = decideTerminalReconnect({
    readyReceived: true,
    reattachable: false,
    status: 'ready',
  })
  const query = new URLSearchParams()
  appendTerminalPtyId(query, false, storedPtyId)

  assert.equal(storedPtyId, null)
  assert.deepEqual(decision, { status: 'error', shouldReconnect: false })
  assert.equal(query.has('pty_id'), false)
})

test('reattachable ready reconnects with its persisted PTY id', () => {
  const storedPtyId = terminalReadyPtyId(true, 123)
  const decision = decideTerminalReconnect({
    readyReceived: true,
    reattachable: true,
    status: 'ready',
  })
  const query = new URLSearchParams()
  appendTerminalPtyId(query, true, storedPtyId)

  assert.equal(storedPtyId, 123)
  assert.deepEqual(decision, { status: 'error', shouldReconnect: true })
  assert.equal(query.get('pty_id'), '123')
})

test('terminal close states remain stable and never reconnect', () => {
  for (const status of ['needs_provision', 'paused', 'exited', 'idle', 'unauthorized'] as const) {
    const decision = decideTerminalReconnect({
      readyReceived: true,
      reattachable: true,
      status,
    })
    assert.deepEqual(decision, { status, shouldReconnect: false })
  }
})
