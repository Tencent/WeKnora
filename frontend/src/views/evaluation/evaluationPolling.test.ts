import assert from 'node:assert/strict'
import test from 'node:test'
import { createEvaluationPoller } from './evaluationPolling'
const flush = async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve() }
test('polling serializes calls, backs off failures and pauses after three failures', async () => {
  let callback: (() => void) | undefined, paused = 0, requests = 0
  const delays: number[] = []
  const poller = createEvaluationPoller({ hasRunning: () => true, refresh: async () => { requests++; throw new Error('offline') }, onPaused: () => { paused++ }, schedule: (cb, delay) => { callback = cb; delays.push(delay); return 1 as unknown as ReturnType<typeof setTimeout> }, cancel: () => {} })
  poller.start(); poller.start(); assert.deepEqual(delays, [3000])
  callback!(); await flush(); callback!(); await flush(); callback!(); await flush()
  assert.deepEqual(delays, [3000, 6000, 12000]); assert.equal(requests, 3); assert.equal(paused, 1)
})
test('stopping on tenant change or unmount invalidates in-flight publication and prevents rearming', async () => {
  let callback: (() => void) | undefined, resolve!: () => void, publishes = 0, scheduled = 0
  const wait = new Promise<void>(r => { resolve = r })
  const poller = createEvaluationPoller({ hasRunning: () => true, refresh: async isCurrent => { await wait; if (isCurrent()) publishes++ }, onPaused: () => {}, schedule: cb => { scheduled++; callback = cb; return 1 as unknown as ReturnType<typeof setTimeout> }, cancel: () => {} })
  poller.start(); callback!(); poller.stop(); resolve(); await flush()
  assert.equal(publishes, 0); assert.equal(scheduled, 1)
})
