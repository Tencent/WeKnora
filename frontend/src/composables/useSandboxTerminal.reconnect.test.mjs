import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const source = readFileSync(new URL('./useSandboxTerminal.ts', import.meta.url), 'utf8')

test('only reattach-capable providers persist and submit a PTY id', () => {
  assert.match(source, /reattachable\s*&&\s*lastPid/)
  assert.match(source, /reattachable\s*=\s*frame\.reattachable\s*!==\s*false/)
  assert.match(
    source,
    /reattachable\s*&&\s*typeof frame\.pty_id === 'number'\s*\?\s*frame\.pty_id\s*:\s*null/,
  )
})

test('automatic reconnect requires ready from a reattach-capable provider', () => {
  const openStart = source.indexOf('async function openSocket()')
  const messageStart = source.indexOf('ws.onmessage =', openStart)
  const closeStart = source.indexOf('ws.onclose =')
  const closeEnd = source.indexOf('ws.onerror =', closeStart)
  assert.ok(openStart >= 0 && messageStart > openStart)
  assert.ok(closeStart > messageStart && closeEnd > closeStart)

  const openSetup = source.slice(openStart, messageStart)
  const messageHandler = source.slice(messageStart, closeStart)
  const closeHandler = source.slice(closeStart, closeEnd)

  assert.match(openSetup, /let readyReceived = false/)
  assert.match(messageHandler, /frame\?\.type === 'ready'\) readyReceived = true/)
  assert.match(closeHandler, /if \(!readyReceived \|\| !reattachable\) return/)
  assert.match(closeHandler, /status\.value = 'error'/)
  assert.ok(
    closeHandler.indexOf("status.value !== 'exited'")
      < closeHandler.indexOf('if (!readyReceived || !reattachable) return'),
    'terminal states must be preserved before reconnect eligibility is checked',
  )
})
