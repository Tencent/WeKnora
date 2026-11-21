import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { ref, reactive, watch } from 'vue'

const source = readFileSync(new URL('./AttachmentUpload.vue', import.meta.url), 'utf8')
const code = source.slice(source.indexOf('const emitFiles ='), source.indexOf('const formatFileSize ='))

function deferred() { let resolve, reject; const promise = new Promise((r, j) => { resolve = r; reject = j }); return { promise, resolve, reject } }
function harness() {
  const props = reactive({ sessionId: 'session-A' })
  const attachments = ref([{ id: 'local-1', file: {}, status: 'uploading' }])
  const pending = deferred(), pollPending = deferred(), calls = [], emitted = []
  let onProgress
  const context = { props, attachments, pollTimers: new Map(), disposed: false, sessionEpoch: 0, watch,
    emit: (...args) => emitted.push(args), t: x => x,
    uploadTemporaryAttachment: (sessionId, file, agent, tenant, parser, progress) => { calls.push(['upload', sessionId]); onProgress = progress; return pending.promise },
    deleteTemporaryAttachment: async (...args) => { calls.push(['delete', ...args]) },
    getTemporaryAttachment: (...args) => { calls.push(['get', ...args]); return pollPending.promise },
    setTimeout, clearTimeout,
  }
  const api = vm.runInNewContext(ts.transpile(`${code}\n({ uploadAttachment, removeAttachment, pollStatus })`), context)
  return { props, attachments, pending, pollPending, calls, api, context, emitted, progress: (...args) => onProgress(...args) }
}

test('late upload after switching sessions is cleaned up from its original session', async () => {
  const h = harness()
  const request = h.api.uploadAttachment(h.attachments.value[0])
  h.attachments.value = [] // composer clears pending uploads during navigation
  h.props.sessionId = 'session-B'
  h.pending.resolve({ data: { id: 'doc-A', status: 'ready' } })
  await request
  assert.deepEqual(h.calls, [['upload', 'session-A'], ['delete', 'session-A', 'doc-A']])
})

test('late upload cannot enter the new session even without route cleanup', async () => {
  const h = harness()
  const request = h.api.uploadAttachment(h.attachments.value[0])
  h.props.sessionId = 'session-B'
  h.pending.resolve({ data: { id: 'doc-A', status: 'processing' } })
  await request
  assert.equal(h.attachments.value.length, 0)
  assert.deepEqual(h.calls, [['upload', 'session-A'], ['delete', 'session-A', 'doc-A']])
})

test('a normal same-session upload remains available for sending', async () => {
  const h = harness()
  const request = h.api.uploadAttachment(h.attachments.value[0])
  h.pending.resolve({ data: { id: 'doc-A', status: 'ready' } })
  await request
  assert.equal(h.attachments.value[0].documentId, 'doc-A')
  assert.equal(h.attachments.value[0].status, 'ready')
  assert.deepEqual(h.calls, [['upload', 'session-A']])
})


test('late upload after unmount is cleaned up from its original session', async () => {
  const h = harness()
  const request = h.api.uploadAttachment(h.attachments.value[0])
  h.context.disposed = true
  h.props.sessionId = undefined
  h.pending.resolve({ data: { id: 'doc-A', status: 'ready' } })
  await request
  assert.deepEqual(h.calls, [['upload', 'session-A'], ['delete', 'session-A', 'doc-A']])
})

test('removing a pending upload cleans the returned document exactly once', async () => {
  const h = harness()
  const request = h.api.uploadAttachment(h.attachments.value[0])
  h.api.removeAttachment('local-1')
  h.pending.resolve({ data: { id: 'doc-A', status: 'ready' } })
  await request
  assert.deepEqual(h.calls, [['upload', 'session-A'], ['delete', 'session-A', 'doc-A']])
})


test('failed upload from old session is not surfaced in the new session', async () => {
  const h = harness()
  const request = h.api.uploadAttachment(h.attachments.value[0])
  h.props.sessionId = 'session-B'
  h.pending.reject(new Error('network error'))
  await request
  assert.equal(h.attachments.value.length, 0)
  assert.deepEqual(h.calls, [['upload', 'session-A']])
})


test('old session upload progress cannot update the new composer', async () => {
  const h = harness()
  const request = h.api.uploadAttachment(h.attachments.value[0])
  h.props.sessionId = 'session-B'
  const eventsBeforeProgress = h.emitted.length
  h.progress(80)
  assert.equal(h.emitted.length, eventsBeforeProgress)
  h.pending.resolve({ data: { id: 'doc-A', status: 'ready' } })
  await request
})


test('in-flight status response from A cannot become ready in B', async () => {
  const h = harness()
  Object.assign(h.attachments.value[0], { documentId: 'doc-A', status: 'processing' })
  const request = h.api.pollStatus(h.attachments.value[0], 'session-A', 0)
  h.props.sessionId = 'session-B'
  h.pollPending.resolve({ data: { status: 'ready' } })
  await request
  assert.equal(h.attachments.value.length, 0)
  assert.deepEqual(h.calls, [['get', 'session-A', 'doc-A']])
})

test('A to B to A after route cleanup never revives the removed attachment', async () => {
  const h = harness()
  Object.assign(h.attachments.value[0], { documentId: 'doc-A', status: 'processing' })
  const request = h.api.pollStatus(h.attachments.value[0], 'session-A', 0)
  h.attachments.value = []
  h.props.sessionId = 'session-B'
  h.props.sessionId = 'session-A'
  h.pollPending.resolve({ data: { status: 'ready' } })
  await request
  assert.equal(h.attachments.value.length, 0)
  assert.deepEqual(h.calls, [['get', 'session-A', 'doc-A']])
})


test('A to B to A before upload completion cannot revive the stale attachment', async () => {
  const h = harness()
  const request = h.api.uploadAttachment(h.attachments.value[0])
  h.props.sessionId = 'session-B'
  h.props.sessionId = 'session-A'
  h.pending.resolve({ data: { id: 'doc-A', status: 'ready' } })
  await request
  assert.equal(h.attachments.value.length, 0)
  assert.deepEqual(h.calls, [['upload', 'session-A'], ['delete', 'session-A', 'doc-A']])
})

test('cleared processing attachment is not deleted after it may have been sent', async () => {
  const h = harness()
  Object.assign(h.attachments.value[0], { documentId: 'doc-A', status: 'processing' })
  const request = h.api.pollStatus(h.attachments.value[0], 'session-A', 0)
  h.attachments.value = [] // successful send clears the composer while parsing continues
  h.props.sessionId = 'session-B'
  h.pollPending.resolve({ data: { status: 'ready' } })
  await request
  assert.deepEqual(h.calls, [['get', 'session-A', 'doc-A']])
})
