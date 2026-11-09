import assert from 'node:assert/strict'
import test from 'node:test'

import { waitForOneDriveAuthorization } from './oneDriveOAuthFlow.ts'

test('blocked popup completes through OAuth status polling', async () => {
  let calls = 0
  let clock = 1000
  const seen: boolean[] = []
  const status = await waitForOneDriveAuthorization({
    popup: null,
    getStatus: async () => ({
      authorized: ++calls >= 2,
      reauthorization_required: false,
    }),
    onStatus: value => seen.push(value.authorized),
    wait: async milliseconds => { clock += milliseconds },
    now: () => clock,
    notCompletedMessage: 'not completed',
  })

  assert.equal(status.authorized, true)
  assert.deepEqual(seen, [false, true])
})

test('connected status waits for callback popup to close', async () => {
  const popup = { closed: false }
  let clock = 1000
  let waits = 0
  await waitForOneDriveAuthorization({
    popup,
    getStatus: async () => ({ authorized: true, reauthorization_required: false }),
    onStatus: () => undefined,
    wait: async milliseconds => {
      waits++
      clock += milliseconds
      popup.closed = true
    },
    now: () => clock,
    notCompletedMessage: 'not completed',
  })

  assert.equal(waits, 1)
})

test('closed popup without authorization fails after the grace period', async () => {
  let clock = 1000
  await assert.rejects(
    waitForOneDriveAuthorization({
      popup: { closed: true },
      getStatus: async () => ({ authorized: false, reauthorization_required: false }),
      onStatus: () => undefined,
      wait: async milliseconds => { clock += milliseconds },
      now: () => clock,
      popupCloseGraceMs: 1000,
      notCompletedMessage: 'authorization not completed',
    }),
    /authorization not completed/,
  )
})

test('reauthorization-required status is not treated as connected', async () => {
  let clock = 1000
  await assert.rejects(
    waitForOneDriveAuthorization({
      popup: null,
      getStatus: async () => ({ authorized: true, reauthorization_required: true }),
      onStatus: () => undefined,
      wait: async milliseconds => { clock += milliseconds },
      now: () => clock,
      timeoutMs: 1000,
      notCompletedMessage: 'authorization not completed',
    }),
    /authorization not completed/,
  )
})
