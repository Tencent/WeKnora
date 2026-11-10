import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import { ref } from 'vue'

const source = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')
const handlers = ts.transpile(source.slice(
  source.indexOf('let dragCounter = 0'),
  source.indexOf('// 组件挂载时添加全局事件监听器'),
))

function fixture(name, { kbId, settingsOpen = false } = {}) {
  const route = { name, params: { kbId } }
  const uiStore = { showSettingsModal: settingsOpen }
  const ismask = ref(false)
  const messages = []
  const requests = []
  const dispatched = []
  let collected = 0
  const api = runInNewContext(`${handlers}; ({
    handleGlobalDragEnter, handleGlobalDragOver, handleGlobalDragLeave, handleGlobalDrop,
  })`, {
    route, uiStore, ismask,
    t: key => key,
    MessagePlugin: {
      error: message => messages.push(message),
      warning: message => messages.push(message),
    },
    collectDroppedFiles: async event => { collected++; return event.dataTransfer.files },
    getKnowledgeBaseById: async id => {
      requests.push(id)
      return { data: { summary_model_id: 'chat-model', embedding_model_id: 'embedding-model' } }
    },
    CustomEvent: class {
      constructor(type, options) { this.type = type; this.detail = options.detail }
    },
    window: { dispatchEvent: event => dispatched.push(event) },
  })
  return { ...api, route, uiStore, ismask, messages, requests, dispatched, collected: () => collected }
}

function fileDrag(types = ['Files']) {
  return {
    dataTransfer: { types, files: [{ name: 'skill.zip' }] },
    defaultPrevented: false,
    propagationStopped: false,
    preventDefault() { this.defaultPrevented = true },
    stopPropagation() { this.propagationStopped = true },
  }
}

test('skill ZIP drops on settings never enter knowledge upload or consume the local drop', async () => {
  const f = fixture('settings')
  for (let attempt = 0; attempt < 3; attempt++) {
    const event = fileDrag()
    f.handleGlobalDragEnter(event)
    f.handleGlobalDragOver(event)
    await f.handleGlobalDrop(event)
    assert.equal(f.ismask.value, false)
    assert.equal(event.propagationStopped, false)
    assert.deepEqual(f.messages, [])
    assert.deepEqual(f.requests, [])
    assert.deepEqual(f.dispatched, [])
    assert.equal(f.collected(), 0)
  }
})

test('settings opened over a knowledge base or chat own their file drops', async () => {
  for (const name of ['knowledgeBaseDetail', 'chat', 'globalCreatChat', 'kbCreatChat']) {
    const f = fixture(name, { kbId: 'kb-1', settingsOpen: true })
    const event = fileDrag()
    f.handleGlobalDragEnter(event)
    await f.handleGlobalDrop(event)
    assert.equal(f.ismask.value, false)
    assert.equal(event.propagationStopped, false)
    assert.deepEqual(f.messages, [])
    assert.deepEqual(f.requests, [])
    assert.deepEqual(f.dispatched, [])
    assert.equal(f.collected(), 0)
  }
})

test('unrelated pages and a knowledge route without an ID ignore global upload', async () => {
  for (const name of ['knowledgeBaseList', 'agentList', 'organizationList', 'knowledgeBaseDetail']) {
    const f = fixture(name)
    f.handleGlobalDragEnter(fileDrag())
    await f.handleGlobalDrop(fileDrag())
    assert.equal(f.ismask.value, false)
    assert.deepEqual(f.messages, [])
    assert.equal(f.collected(), 0)
  }
})

test('knowledge file drops still validate initialization and reach the knowledge page', async () => {
  const f = fixture('knowledgeBaseDetail', { kbId: 'kb-1' })
  const event = fileDrag()
  f.handleGlobalDragEnter(event)
  assert.equal(f.ismask.value, true)
  await f.handleGlobalDrop(event)
  assert.equal(f.ismask.value, false)
  assert.deepEqual(f.requests, ['kb-1'])
  assert.equal(f.dispatched[0].type, 'weknora:knowledge-file-drop')
  assert.equal(f.dispatched[0].detail.kbId, 'kb-1')
  assert.equal(f.dispatched[0].detail.files, event.dataTransfer.files)
})

test('chat file drops still dispatch attachments without knowledge initialization', async () => {
  for (const name of ['chat', 'globalCreatChat', 'kbCreatChat']) {
    const f = fixture(name, { kbId: 'kb-1' })
    const event = fileDrag()
    await f.handleGlobalDrop(event)
    assert.deepEqual(f.requests, [])
    assert.deepEqual(f.messages, [])
    assert.equal(f.dispatched[0].type, 'weknora:chat-file-drop')
    assert.equal(f.dispatched[0].detail.files, event.dataTransfer.files)
  }
})

test('in-app text drags do not activate upload', async () => {
  const f = fixture('knowledgeBaseDetail', { kbId: 'kb-1' })
  const event = fileDrag(['text/plain'])
  f.handleGlobalDragEnter(event)
  await f.handleGlobalDrop(event)
  assert.equal(event.defaultPrevented, false)
  assert.equal(f.ismask.value, false)
  assert.equal(f.collected(), 0)
})

test('opening settings during a drag clears the global upload mask', async () => {
  const f = fixture('chat')
  f.handleGlobalDragEnter(fileDrag())
  assert.equal(f.ismask.value, true)
  f.uiStore.showSettingsModal = true
  f.handleGlobalDragOver(fileDrag())
  assert.equal(f.ismask.value, false)
  await f.handleGlobalDrop(fileDrag())
  assert.deepEqual(f.dispatched, [])
})
