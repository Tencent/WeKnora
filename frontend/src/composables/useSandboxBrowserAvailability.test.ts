import assert from 'node:assert/strict'
import test from 'node:test'
import { effectScope, reactive } from 'vue'
import { useSandboxBrowserAvailability, type BrowserCapabilityContext } from './useSandboxBrowserAvailability'

const flush = () => new Promise(resolve => setImmediate(resolve))

test('office hides browser, full image shows it, and late responses cannot cross sessions', async () => {
  const context = reactive<BrowserCapabilityContext>({ sessionId: 'full', visible: true })
  const pending = new Map<string, (value: boolean) => void>()
  const scope = effectScope()
  const available = scope.run(() => useSandboxBrowserAvailability(() => ({ ...context }),
    state => new Promise<boolean>(resolve => { pending.set(state.sessionId, resolve) })))!
  try {
    context.sessionId = 'office'
    pending.get('full')!(true)
    await flush()
    assert.equal(available.value, false)
    pending.get('office')!(false)
    await flush()
    assert.equal(available.value, false)
    context.sessionId = 'full-again'
    pending.get('full-again')!(true)
    await flush()
    assert.equal(available.value, true)
    context.agentId = 'different-agent'
    assert.equal(available.value, false, 'clear stale capability before the new request resolves')
  } finally { scope.stop() }
})

test('closed panel does not request capabilities and disposal ignores pending replies', async () => {
  const context = reactive<BrowserCapabilityContext>({ sessionId: 'full', visible: false })
  let calls = 0
  let resolve!: (value: boolean) => void
  const scope = effectScope()
  const available = scope.run(() => useSandboxBrowserAvailability(() => ({ ...context }), () => {
    calls++
    return new Promise<boolean>(done => { resolve = done })
  }))!
  assert.equal(calls, 0)
  context.visible = true
  assert.equal(calls, 1)
  scope.stop()
  resolve(true)
  await flush()
  assert.equal(available.value, false)
})
