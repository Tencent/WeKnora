import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { createRouter, createMemoryHistory, isNavigationFailure, NavigationFailureType } from 'vue-router'

const source = readFileSync(new URL('./creatChat.vue', import.meta.url), 'utf8')
const code = source.slice(source.indexOf('async function openCreatedSession('), source.indexOf('async function retryNavigation()'))
function build(router, route, clock = {}) {
  return vm.runInNewContext(ts.transpile(`${code}\nopenCreatedSession`), {
    router, route, leavingPage: false, isNavigationFailure, NavigationFailureType,
    setTimeout, clearTimeout, ...clock,
  })
}

test('a newer navigation cancels chat opening without replacing the user destination', async () => {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/new', component: {} }, { path: '/platform/chat/:id', component: {} }, { path: '/knowledge', component: {} },
  ] })
  await router.push('/new')
  let release, entered
  const started = new Promise(resolve => { entered = resolve })
  router.beforeEach(to => to.path.startsWith('/platform/chat/') ? new Promise(resolve => { release = resolve; entered() }) : undefined)
  const open = build(router, { fullPath: '/new' })('session')
  await started
  await router.push('/knowledge')
  release()
  await open
  assert.equal(router.currentRoute.value.path, '/knowledge')
})

test('only a timeout cancels the still-current lazy navigation', async () => {
  const replacements = []
  let expire
  const open = build({ push: () => new Promise(() => {}), replace: async path => replacements.push(path) }, { fullPath: '/new' }, {
    setTimeout: fn => { expire = fn; return 1 }, clearTimeout() {},
  })
  const pending = open('session')
  expire()
  await assert.rejects(pending, /timed out/)
  assert.deepEqual(replacements, ['/new'])
})

test('a rejected chunk does not replace the route', async () => {
  let replaced = false
  const open = build({ push: async () => { throw new Error('chunk failed') }, replace: async () => { replaced = true } }, { fullPath: '/new' })
  await assert.rejects(open('session'), /chunk failed/)
  assert.equal(replaced, false)
})

test('Send cannot create another session while an existing one is awaiting retry', async () => {
  const code = source.slice(source.indexOf('async function createNewSession('), source.indexOf('const navigateToSession ='))
  let created = false
  const create = vm.runInNewContext(ts.transpile(`${code}\ncreateNewSession`), {
    creatingSession: { value: false }, createdSessionId: { value: 'existing' }, createSessions: () => { created = true },
  })
  await create('question', 'model')
  assert.equal(created, false)
})

test('failed creation keeps the full pending media metadata and unlocks the preserved draft', async () => {
  const code = source.slice(source.indexOf('async function createNewSession('), source.indexOf('const navigateToSession ='))
  const pendingImages = { value: [] }, pendingAttachments = { value: [] }
  const creationError = { value: false }, creatingSession = { value: false }
  const create = vm.runInNewContext(ts.transpile(`${code}\ncreateNewSession`), {
    creatingSession, createdSessionId: { value: '' }, creationError, pendingImages, pendingAttachments,
    pendingQuestion: { value: '' }, pendingOptions: {}, leavingPage: false, clearPendingPreviews() {},
    URL: { createObjectURL: () => 'blob:photo' }, settingsStore: { settings: {}, agentConfig: {} },
    selectedProjectDir: { value: '' }, withOptionalProjectDir: data => data,
    createSessions: async () => { throw new Error('Unavailable') }, console: { error() {} },
  })
  await create('question', 'model', [{ id: 'mention' }], [{ name: 'photo.png' }], [{ name: 'book.pdf', documentId: 'attachment' }])
  assert.equal(creationError.value, true)
  assert.equal(creatingSession.value, false)
  assert.equal(pendingImages.value[0].name, 'photo.png')
  assert.equal(pendingImages.value[0].url, 'blob:photo')
  assert.equal(pendingAttachments.value[0].name, 'book.pdf')
})
