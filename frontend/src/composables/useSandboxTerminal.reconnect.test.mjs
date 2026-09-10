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

test('non-reattachable transport loss does not auto-create another shell', () => {
  const closeStart = source.indexOf('ws.onclose =')
  const closeEnd = source.indexOf('ws.onerror =', closeStart)
  assert.ok(closeStart >= 0 && closeEnd > closeStart)
  const closeHandler = source.slice(closeStart, closeEnd)
  assert.match(closeHandler, /if \(!reattachable\)/)
  assert.match(closeHandler, /status\.value = 'error'/)
  assert.match(closeHandler, /return/)
})
