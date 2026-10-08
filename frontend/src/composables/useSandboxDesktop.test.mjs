import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const src = readFileSync(new URL('./useSandboxDesktop.ts', import.meta.url), 'utf8')

test('AUTH_REVOKED close reason maps to unauthorized and does not reconnect', () => {
  assert.match(src, /case 'AUTH_REVOKED':\s*return 'unauthorized'/)
  assert.match(src, /mapped === 'idle' \|\| mapped === 'needs_provision' \|\| mapped === 'paused'/)
  assert.match(src, /mapped !== 'start_failed'/)
})

test('panel lookup is provision-opt-in and paused stays on the overlay', () => {
  assert.match(src, /allowProvision/)
  assert.match(src, /query\.set\('provision',\s*'1'\)/)
  assert.match(src, /allowProvision = false/)
  assert.match(src, /case 'SANDBOX_PAUSED':\s*return 'paused'/)
  assert.match(src, /mapped === 'paused'/)
  assert.match(src, /\| 'paused'/)
  assert.match(src, /connect:\s*\(target: HTMLElement,\s*options\?: \{ provision\?: boolean \}\)/)
})

test('local Cmd/Ctrl+V is bridged as RFB clipboard then Linux Ctrl+V', () => {
  assert.match(src, /clipboardPasteFrom/)
  assert.match(src, /function pasteLocalClipboard/)
  assert.match(src, /function attachDesktopClipboard/)
  assert.match(src, /navigator\.clipboard\.readText/)
  assert.match(src, /sendGuestChord\(client,\s*XK_v,\s*'KeyV'\)/)
  assert.match(src, /sendGuestChord\(client,\s*XK_c,\s*'KeyC'\)/)
  assert.match(src, /addEventListener\('paste'/)
  assert.match(src, /addEventListener\('clipboard'/)
})
