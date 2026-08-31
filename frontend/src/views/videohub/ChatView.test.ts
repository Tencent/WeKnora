import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'

const componentSource = readFileSync(resolve(import.meta.dirname, 'ChatView.vue'), 'utf8')

test('chat page disables protected image hydration in native agent display', () => {
  assert.match(componentSource, /:hydrate-protected-images="false"/)
})

test('chat page lays out assistant identity separately from the answer body', () => {
  assert.match(componentSource, /\.message--assistant\s*\{[^}]*display:\s*grid/s)
  assert.match(componentSource, /\.message--assistant \.message__bubble\s*\{[^}]*width:\s*100%/s)
})

test('chat page materializes pending sessions as soon as the backend creates them', () => {
  assert.match(componentSource, /function materializePendingSession\(pendingSession: ChatSession, createdSession: ChatSession\)/)
  assert.match(componentSource, /onSessionCreated: createdSession => materializePendingSession\(session, createdSession\)/)
})
