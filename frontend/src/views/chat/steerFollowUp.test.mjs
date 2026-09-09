import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')
const steerApi = readFileSync(new URL('../../api/chat/steer.ts', import.meta.url), 'utf8')

function attachSteerFollowUpSource() {
  const start = source.indexOf('const attachSteerFollowUp = async')
  const end = source.indexOf('const sendMsg = async', start)
  assert.notEqual(start, -1)
  assert.notEqual(end, -1)
  return source.slice(start, end)
}

test('refresh restores the overlay queue from the live run', () => {
  assert.match(steerApi, /export async function listSteerSession/)
  assert.match(source, /listSteerSession/)
  assert.match(source, /hydrateSteerQueue/)
  assert.match(source, /if \(!steerQueue\.value\.length\)/)
})

// A turn that absorbed a mid-run message is split into several assistant
// segments, and every segment after the first gets a synthetic id. Resuming
// or stopping with that id hits a row the server has never heard of: the
// stream 404s and the UI looks like the agent died on refresh.
test('resume and stop address the persisted assistant id, not a fork id', () => {
  const fork = readFileSync(new URL('../../utils/steerStreamFork.ts', import.meta.url), 'utf8')
  assert.match(fork, /export function persistedAssistantId/)

  const start = source.indexOf('onAfterMsgList: async ()')
  const end = source.indexOf('onAgentQuery:', start)
  assert.notEqual(start, -1)
  assert.notEqual(end, -1)
  const hook = source.slice(start, end)

  assert.match(hook, /persistedAssistantId\(lastMessage\)/)
  assert.match(hook, /currentAssistantMessageId\.value = resumeId/)
  assert.match(hook, /query: resumeId/)
  assert.doesNotMatch(hook, /query: lastMessage\.id/)

  // An injected user row can be the last entry in the list. Keying the resume
  // off the tail row would then skip continue-stream and the still-running
  // agent would produce nothing visible.
  assert.match(hook, /findLastMessage\(\s*\(message\) => message\.role === 'assistant' && !message\.is_completed\s*\)/)
  assert.doesNotMatch(hook, /messagesList\[messagesList\.length - 1\]/)
})

// Stopping means stopping. Firing off the queued messages as a fresh run is
// the opposite of what the button says. The overlay stays until /stop
// succeeds, so a failed request can restore the composer without losing chips.
test('stop clears the queue only after the stop API succeeds', () => {
  const start = source.indexOf('const handleStopGeneration = ')
  const confirmedStart = source.indexOf('const handleStopConfirmed = ')
  const failedStart = source.indexOf('const handleStopFailed = ')
  const end = source.indexOf('const dropSteerQueueItem', start)
  assert.notEqual(start, -1)
  assert.notEqual(confirmedStart, -1)
  assert.notEqual(failedStart, -1)
  assert.notEqual(end, -1)

  const gen = source.slice(start, confirmedStart)
  assert.doesNotMatch(gen, /steerQueue\.value = \[\]/)
  assert.doesNotMatch(gen, /attachSteerFollowUp/)

  const confirmed = source.slice(confirmedStart, failedStart)
  assert.match(confirmed, /steerQueue\.value = \[\]/)

  const failed = source.slice(failedStart, end)
  assert.match(failed, /isReplying\.value = true/)
})

// Matching queued text against the message list attaches the wrong bubble as
// soon as the user sends the same thing twice. The follow-up run persists its
// query under its own request_id, which identifies the rows exactly.
test('follow-up attaches persisted users by request_id', () => {
  const fn = attachSteerFollowUpSource()
  assert.match(fn, /persisted\.request_id !== newAssistant\.request_id/)
  assert.match(fn, /hydrateSteerQueue\(\{ onlyWhenLive: true \}\)/)
  assert.doesNotMatch(fn, /m\.content === q\.content/)
})

// A 503 from the steer lookup must toast, not fall through to sendMsg. Only
// an explicit new_run (no live turn) starts a normal AgentQA.
test('steer lookup failure does not start a second turn', () => {
  const start = source.indexOf('const handleSteerMsg = async')
  const end = source.indexOf('const handlePromoteSteer = async', start)
  assert.notEqual(start, -1)
  assert.notEqual(end, -1)
  const fn = source.slice(start, end)
  const newRun = fn.indexOf("if (res?.status === 'new_run')")
  const catchIdx = fn.indexOf('} catch (e) {')
  assert.notEqual(newRun, -1)
  assert.notEqual(catchIdx, -1)
  assert.ok(newRun < catchIdx, 'new_run fallback must sit in the try, not the catch')
  const newRunBlock = fn.slice(newRun, catchIdx)
  assert.match(newRunBlock, /awaitingIdleSend/)
  assert.match(newRunBlock, /isReplying/)
  assert.match(newRunBlock, /await sendMsg\(/)
  assert.doesNotMatch(fn.slice(catchIdx), /sendMsg\(/)
})

test('new_run while a stream is live does not abort it', () => {
  const start = source.indexOf('const handleSteerMsg = async')
  const end = source.indexOf('const handlePromoteSteer = async', start)
  const fn = source.slice(start, end)
  const newRun = fn.indexOf("if (res?.status === 'new_run')")
  const catchIdx = fn.indexOf('} catch (e) {')
  const block = fn.slice(newRun, catchIdx)
  assert.match(block, /isStreaming/)
  assert.doesNotMatch(block, /stopStream\(/)
})

test('turn complete flushes idle new_run sends before attaching a follow-up', () => {
  assert.match(source, /const flushSteerAfterTurn = async/)
  assert.match(source, /awaitingIdleSend/)
  const start = source.indexOf('onTurnComplete:')
  const end = source.indexOf('});', start)
  const hook = source.slice(start, end)
  assert.match(hook, /flushSteerAfterTurn\(persistedAssistantId\(message\)\)/)
  assert.doesNotMatch(hook, /attachSteerFollowUp\(persistedAssistantId\(message\)\)/)
})

test('remaining overlay after-items chain after a follow-up stream ends', () => {
  const fn = attachSteerFollowUpSource()
  const finallyStart = fn.indexOf('} finally {')
  assert.notEqual(finallyStart, -1, 'attachSteerFollowUp must have a finally block')
  const finallyBlock = fn.slice(finallyStart)

  // startStream awaits the whole SSE. The follow-up's onTurnComplete therefore
  // runs while attachingSteerFollowUp is still true and would no-op. Remaining
  // after-items have to be retried once that guard drops.
  assert.match(finallyBlock, /steerQueue\.value\.length/)
  assert.match(finallyBlock, /void attachSteerFollowUp\(/)
})

// Switching sessions while the follow-up poll is in flight used to keep
// using session_id.value, so continue-stream landed on the new chat.
test('follow-up attach aborts when the session changes', () => {
  const fn = attachSteerFollowUpSource()
  assert.match(fn, /const sessionId = session_id\.value/)
  assert.match(fn, /session_id\.value !== sessionId/)
  assert.match(fn, /getMessageList\(\{ session_id: sessionId/)
  assert.match(fn, /session_id: sessionId/)
})

// After a mid-run inject the completing bubble is a steer-cont-* segment.
// Passing that id into attachSteerFollowUp cannot exclude the persisted
// row, so the finishing turn was picked as the follow-up.
test('follow-up attach keys off the persisted assistant id, not a fork id', () => {
  const start = source.indexOf('onTurnComplete:')
  const end = source.indexOf('});', start)
  assert.notEqual(start, -1)
  assert.notEqual(end, -1)
  const hook = source.slice(start, end)
  assert.match(hook, /flushSteerAfterTurn\(persistedAssistantId\(message\)\)/)
  assert.doesNotMatch(hook, /attachSteerFollowUp\(message\?\.id\)/)
  const flushStart = source.indexOf('const flushSteerAfterTurn = async')
  const flushEnd = source.indexOf('const attachSteerFollowUp = async', flushStart)
  assert.match(source.slice(flushStart, flushEnd), /attachSteerFollowUp\(completedAssistantId\)/)
})

// A stop that arrives over SSE (other tab, /stop API) discards the server
// queue. The overlay has to drop too, not only the local Stop button.
test('remove drops overlay when the server already injected the item', () => {
  const start = source.indexOf('const handleRemoveSteer = async')
  const end = source.indexOf('let attachingSteerFollowUp', start)
  assert.notEqual(start, -1)
  assert.notEqual(end, -1)
  const fn = source.slice(start, end)
  assert.match(fn, /already_injected/)
  assert.match(fn, /dropSteerQueueItem\(steerId\)/)
  assert.match(fn, /removed === false/)
})

test('remote stop clears the overlay queue', () => {
  assert.match(source, /onGenerationStopped/)
  const start = source.indexOf('onGenerationStopped')
  assert.notEqual(start, -1)
  const snippet = source.slice(start, start + 180)
  assert.match(snippet, /steerQueue\.value = \[\]/)
})
