import assert from 'node:assert/strict'
import test from 'node:test'

import { computed, createRenderer, nextTick, reactive } from 'vue'
import { nextTypewriterReveal, useTypewriter } from './useTypewriter.ts'
import { applyFinalArtifactContent } from '../utils/finalArtifactContent.ts'

test('reveals Chinese text in compact phrase groups', () => {
  const text = '这是一个自然流畅的回答。'
  assert.equal(nextTypewriterReveal(text, 0, 1), 0)
  assert.equal(nextTypewriterReveal(text, 0, 2), 2)
  assert.equal(nextTypewriterReveal(text, 0, 20), 4)
})

test('waits for a complete English word instead of revealing letter by letter', () => {
  const text = 'Hello world'
  assert.equal(nextTypewriterReveal(text, 0, 3), 0)
  assert.equal(nextTypewriterReveal(text, 0, 6), 6)
  assert.equal(text.slice(0, nextTypewriterReveal(text, 0, 6)), 'Hello ')
})

test('uses punctuation as a natural reveal boundary', () => {
  const text = '你好，接下来继续。'
  assert.equal(nextTypewriterReveal(text, 0, 3), 3)
})

test('never splits a surrogate pair', () => {
  const text = '🙂 hello'
  assert.equal(nextTypewriterReveal(text, 0, 1), 2)
})

test('artifact reconciliation replaces completed text without replaying the typewriter', async () => {
  const originalRequest = globalThis.requestAnimationFrame
  const originalCancel = globalThis.cancelAnimationFrame
  const frames = new Map<number, FrameRequestCallback>()
  let id = 0
  globalThis.requestAnimationFrame = (callback) => { frames.set(++id, callback); return id }
  globalThis.cancelAnimationFrame = (frame) => { frames.delete(frame) }
  const message = reactive<any>({ content: '', agentEventStream: [{ type: 'answer', content: '', done: false }] })
  const answer = computed(() => message.agentEventStream[0])
  let displayed!: ReturnType<typeof useTypewriter>['displayed']
  // Mount the real composable with a minimal renderer so watches and lifecycle
  // cleanup run normally, without a browser-emulation dependency.
  const renderer = createRenderer<any, any>({
    patchProp() {}, insert() {}, remove() {}, setText() {}, setElementText() {},
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    parentNode: () => null, nextSibling: () => null,
  })
  const app = renderer.createApp({
    setup() {
      displayed = useTypewriter(() => answer.value.content, () => answer.value.done).displayed
      return () => null
    },
  })
  try {
    app.mount({})
    answer.value.content = 'PPT 已生成。![比赛文件](sandbox:很长的比赛信息文件名.pptx)'
    await nextTick()
    assert.equal(displayed.value, '', 'live content must still animate')
    answer.value.done = true
    await nextTick()
    assert.equal(displayed.value, '', 'normal completion must let the remaining animation finish')
    for (let time = 16; frames.size && time < 10000; time += 16) {
      const pending = [...frames.values()]
      frames.clear()
      pending.forEach(callback => callback(time))
    }
    assert.equal(displayed.value, answer.value.content)
    for (const content of [
      'PPT 已生成。![比赛文件](resource://short)',
      'PPT 已生成。![比赛文件](resource://a-much-longer-persistent-resource-reference)',
      'PPT 已生成。![比赛文件](resource://a-much-longer-persistent-resource-reference)\n历史版本说明',
    ]) {
      applyFinalArtifactContent(message, content)
      await nextTick()
      assert.equal(displayed.value, content, 'completed corrections must appear immediately')
      assert.equal(frames.size, 0, 'completed corrections must not restart animation')
    }
  } finally {
    app.unmount()
    globalThis.requestAnimationFrame = originalRequest
    globalThis.cancelAnimationFrame = originalCancel
  }
})
