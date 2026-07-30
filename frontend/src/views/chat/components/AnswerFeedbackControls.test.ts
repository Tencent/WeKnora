import assert from 'node:assert/strict'
import test from 'node:test'
import path from 'node:path'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { compileScript, parse } from '@vue/compiler-sfc'
import {
  createRenderer,
  defineComponent,
  h,
  nextTick,
  ref,
  type Component,
  type Ref,
} from 'vue'
import { transformWithEsbuild } from 'vite'

type FeedbackState = {
  type: 'like' | 'dislike'
  reason_code?: 'inaccurate' | 'irrelevant' | 'incomplete' | 'outdated' | 'other'
} | null

type FeedbackMessage = {
  id: string
  feedback_eligible: boolean
  my_feedback: FeedbackState
}

type HostNode = {
  type: string
  props: Record<string, any>
  children: HostNode[]
  parent: HostNode | null
  text: string
}

type FeedbackTransport = (
  sessionID: string,
  messageID: string,
  type: 'like' | 'dislike' | 'none',
  reason?: string,
) => Promise<any>

declare global {
  // Test-only Vite virtual modules call these functions at runtime.
  // eslint-disable-next-line no-var
  var __weknoraFeedbackTransport: FeedbackTransport
  // eslint-disable-next-line no-var
  var __weknoraFeedbackErrors: string[]
}

const frontendRoot = fileURLToPath(new URL('../../../../', import.meta.url))
let FeedbackControls: Component
let temporaryModuleDir: string

test.before(async () => {
  temporaryModuleDir = await mkdtemp(path.join(frontendRoot, '.answer-feedback-test-'))
  const filename = path.join(
    frontendRoot,
    'src/views/chat/components/AnswerFeedbackControls.vue',
  )
  const source = await readFile(filename, 'utf8')
  const { descriptor } = parse(source, { filename })
  const compiled = compileScript(descriptor, {
    id: 'answer-feedback-controls',
    inlineTemplate: true,
  })
  const mockedImports = compiled.content
    .replaceAll(`'@/api/chat'`, `'./api.mjs'`)
    .replaceAll(`"@/api/chat"`, `"./api.mjs"`)
    .replaceAll(`'tdesign-vue-next'`, `'./tdesign.mjs'`)
    .replaceAll(`"tdesign-vue-next"`, `"./tdesign.mjs"`)
    .replaceAll(`'vue-i18n'`, `'./i18n.mjs'`)
    .replaceAll(`"vue-i18n"`, `"./i18n.mjs"`)
  const transformed = await transformWithEsbuild(
    mockedImports,
    'AnswerFeedbackControls.ts',
    { loader: 'ts', format: 'esm', target: 'es2022' },
  )
  await Promise.all([
    writeFile(
      path.join(temporaryModuleDir, 'api.mjs'),
      `export const putMessageFeedback = (...args) =>
        globalThis.__weknoraFeedbackTransport(...args)`,
    ),
    writeFile(
      path.join(temporaryModuleDir, 'tdesign.mjs'),
      `export const MessagePlugin = {
        error: (message) => globalThis.__weknoraFeedbackErrors.push(message),
      }`,
    ),
    writeFile(
      path.join(temporaryModuleDir, 'i18n.mjs'),
      `export const useI18n = () => ({ t: (key) => key })`,
    ),
    writeFile(path.join(temporaryModuleDir, 'component.mjs'), transformed.code),
  ])
  FeedbackControls = (
    await import(pathToFileURL(path.join(temporaryModuleDir, 'component.mjs')).href)
  ).default
})

test.after(async () => {
  if (temporaryModuleDir) {
    await rm(temporaryModuleDir, { recursive: true, force: true })
  }
})

const createHostNode = (type: string, text = ''): HostNode => ({
  type,
  props: {},
  children: [],
  parent: null,
  text,
})

const hostRenderer = createRenderer<HostNode, HostNode>({
  patchProp(node, key, _previous, next) {
    node.props[key] = next
  },
  insert(child, parent, anchor) {
    child.parent = parent
    const index = anchor ? parent.children.indexOf(anchor) : -1
    if (index < 0) parent.children.push(child)
    else parent.children.splice(index, 0, child)
  },
  remove(child) {
    if (!child.parent) return
    const index = child.parent.children.indexOf(child)
    if (index >= 0) child.parent.children.splice(index, 1)
    child.parent = null
  },
  createElement(type) {
    return createHostNode(type)
  },
  createText(text) {
    return createHostNode('#text', text)
  },
  createComment(text) {
    return createHostNode('#comment', text)
  },
  setText(node, text) {
    node.text = text
  },
  setElementText(node, text) {
    const child = createHostNode('#text', text)
    child.parent = node
    node.children = [child]
  },
  parentNode(node) {
    return node.parent
  },
  nextSibling(node) {
    if (!node.parent) return null
    const index = node.parent.children.indexOf(node)
    return node.parent.children[index + 1] ?? null
  },
  querySelector() {
    return null
  },
  setScopeId() {},
  cloneNode(node) {
    return { ...node, props: { ...node.props }, children: [...node.children], parent: null }
  },
  insertStaticContent(content, parent, anchor) {
    const node = createHostNode('#static', content)
    node.parent = parent
    const index = anchor ? parent.children.indexOf(anchor) : -1
    if (index < 0) parent.children.push(node)
    else parent.children.splice(index, 0, node)
    return [node, node]
  },
})

const ButtonStub = defineComponent({
  name: 'TButton',
  inheritAttrs: false,
  emits: ['click'],
  setup(_props, { attrs, emit, slots }) {
    return () => h('button', {
      ...attrs,
      onClick: (event: any) => {
        if (!attrs.disabled) emit('click', event)
      },
    }, slots.default?.())
  },
})

const PopupStub = defineComponent({
  name: 'TPopup',
  setup(_props, { slots }) {
    return () => h('popup', {}, [slots.default?.(), slots.content?.()])
  },
})

const IconStub = defineComponent({
  name: 'TIcon',
  setup() {
    return () => h('icon')
  },
})

const nodeText = (node: HostNode): string =>
  node.text + node.children.map(nodeText).join('')

const findNodes = (node: HostNode, predicate: (candidate: HostNode) => boolean): HostNode[] => {
  const found = predicate(node) ? [node] : []
  return found.concat(node.children.flatMap((child) => findNodes(child, predicate)))
}

const findButton = (root: HostNode, label: string): HostNode => {
  const button = findNodes(root, (node) =>
    node.type === 'button' && (node.props.title === label || nodeText(node).includes(label)),
  )[0]
  assert.ok(button, `button ${label} should be mounted`)
  return button
}

const click = async (button: HostNode) => {
  button.props.onClick?.({ stopPropagation() {} })
  await nextTick()
}

const flushPromises = async () => {
  await Promise.resolve()
  await Promise.resolve()
  await nextTick()
}

const deferred = <T>() => {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

const mountFeedback = (
  initialMessage: FeedbackMessage,
  transport: FeedbackTransport,
  updateParent = true,
) => {
  globalThis.__weknoraFeedbackTransport = transport
  globalThis.__weknoraFeedbackErrors = []
  const sessionID = ref('session-a')
  const message: Ref<FeedbackMessage> = ref(initialMessage)
  const updates: FeedbackState[] = []
  const Harness = defineComponent({
    setup() {
      return () => h(FeedbackControls, {
        sessionId: sessionID.value,
        message: message.value,
        'onUpdate:feedback': (feedback: FeedbackState) => {
          updates.push(feedback)
          if (updateParent) {
            message.value = { ...message.value, my_feedback: feedback }
          }
        },
      })
    },
  })
  const root = createHostNode('root')
  const app = hostRenderer.createApp(Harness)
  app.component('t-button', ButtonStub)
  app.component('t-popup', PopupStub)
  app.component('t-icon', IconStub)
  app.mount(root)
  return { app, root, sessionID, message, updates }
}

test('mounts canonical feedback and disables ineligible messages', async () => {
  const mounted = mountFeedback(
    { id: 'message-a', feedback_eligible: true, my_feedback: { type: 'like' } },
    async () => ({ data: null }),
  )
  assert.equal(findButton(mounted.root, 'feedback.like').props['aria-pressed'], true)
  assert.equal(findButton(mounted.root, 'feedback.dislike').props['aria-pressed'], false)

  mounted.message.value = { ...mounted.message.value, feedback_eligible: false }
  await nextTick()
  assert.equal(findButton(mounted.root, 'feedback.like').props.disabled, true)
  assert.equal(findButton(mounted.root, 'feedback.dislike').props.disabled, true)
  mounted.app.unmount()
})

test('like uses server canonical state, emits it, and never mutates the prop', async () => {
  const request = deferred<any>()
  const original: FeedbackMessage = {
    id: 'message-a', feedback_eligible: true, my_feedback: null,
  }
  const mounted = mountFeedback(original, async () => request.promise, false)

  await click(findButton(mounted.root, 'feedback.like'))
  assert.equal(original.my_feedback, null)
  assert.equal(findButton(mounted.root, 'feedback.like').props.disabled, true)
  assert.equal(findButton(mounted.root, 'feedback.dislike').props.disabled, true)

  mounted.message.value = {
    ...mounted.message.value,
    my_feedback: { type: 'dislike', reason_code: 'other' },
  }
  await nextTick()
  request.resolve({ data: { type: 'like' } })
  await flushPromises()

  assert.deepEqual(mounted.updates, [{ type: 'like' }])
  assert.equal(findButton(mounted.root, 'feedback.like').props['aria-pressed'], true)
  assert.equal(original.my_feedback, null)
  mounted.app.unmount()
})

test('dislike with a reason and clear both use canonical responses', async () => {
  const calls: Array<{ type: string; reason?: string }> = []
  const responses = [
    { data: { type: 'dislike', reason_code: 'inaccurate' } },
    { data: null },
  ]
  const mounted = mountFeedback(
    { id: 'message-a', feedback_eligible: true, my_feedback: null },
    async (_session, _message, type, reason) => {
      calls.push({ type, reason })
      return responses.shift()
    },
  )

  await click(findButton(mounted.root, 'feedback.reasons.inaccurate'))
  await flushPromises()
  assert.deepEqual(calls[0], { type: 'dislike', reason: 'inaccurate' })
  assert.equal(findButton(mounted.root, 'feedback.dislike').props['aria-pressed'], true)

  await click(findButton(mounted.root, 'feedback.dislike'))
  await flushPromises()
  assert.deepEqual(calls[1], { type: 'none', reason: undefined })
  assert.equal(findButton(mounted.root, 'feedback.dislike').props['aria-pressed'], false)
  assert.deepEqual(mounted.updates, [
    { type: 'dislike', reason_code: 'inaccurate' },
    null,
  ])
  mounted.app.unmount()
})

test('failed mutation rolls back local state and displays an error', async () => {
  const request = deferred<any>()
  const original: FeedbackMessage = {
    id: 'message-a', feedback_eligible: true, my_feedback: { type: 'like' },
  }
  const mounted = mountFeedback(original, async () => request.promise, false)

  await click(findButton(mounted.root, 'feedback.like'))
  request.reject(new Error('failed'))
  await flushPromises()

  assert.equal(findButton(mounted.root, 'feedback.like').props['aria-pressed'], true)
  assert.deepEqual(original.my_feedback, { type: 'like' })
  assert.deepEqual(globalThis.__weknoraFeedbackErrors, ['feedback.saveFailed'])
  assert.deepEqual(mounted.updates, [])
  mounted.app.unmount()
})

test('pending like or dislike serializes every action for that answer', async () => {
  const likeRequest = deferred<any>()
  const likeCalls: string[] = []
  const likeMounted = mountFeedback(
    { id: 'message-a', feedback_eligible: true, my_feedback: null },
    async (_session, _message, type) => {
      likeCalls.push(type)
      return likeRequest.promise
    },
  )
  await click(findButton(likeMounted.root, 'feedback.like'))
  await click(findButton(likeMounted.root, 'feedback.dislike'))
  await click(findButton(likeMounted.root, 'feedback.reasons.inaccurate'))
  assert.deepEqual(likeCalls, ['like'])
  likeRequest.resolve({ data: { type: 'like' } })
  await flushPromises()
  likeMounted.app.unmount()

  const dislikeRequest = deferred<any>()
  const dislikeCalls: string[] = []
  const dislikeMounted = mountFeedback(
    { id: 'message-a', feedback_eligible: true, my_feedback: null },
    async (_session, _message, type) => {
      dislikeCalls.push(type)
      return dislikeRequest.promise
    },
  )
  await click(findButton(dislikeMounted.root, 'feedback.reasons.inaccurate'))
  await click(findButton(dislikeMounted.root, 'feedback.dislike'))
  await click(findButton(dislikeMounted.root, 'feedback.like'))
  assert.deepEqual(dislikeCalls, ['dislike'])
  dislikeRequest.resolve({ data: { type: 'dislike', reason_code: 'inaccurate' } })
  await flushPromises()
  dislikeMounted.app.unmount()
})

test('message identity change isolates stale responses and pending state', async () => {
  const oldRequest = deferred<any>()
  const calls: string[] = []
  const mounted = mountFeedback(
    { id: 'message-a', feedback_eligible: true, my_feedback: null },
    async (_session, messageID, type) => {
      calls.push(`${messageID}:${type}`)
      if (messageID === 'message-a') return oldRequest.promise
      return { data: null }
    },
  )

  await click(findButton(mounted.root, 'feedback.like'))
  mounted.message.value = {
    id: 'message-b',
    feedback_eligible: true,
    my_feedback: { type: 'dislike', reason_code: 'other' },
  }
  await nextTick()
  assert.equal(findButton(mounted.root, 'feedback.dislike').props['aria-pressed'], true)
  assert.notEqual(findButton(mounted.root, 'feedback.dislike').props.disabled, true)

  oldRequest.resolve({ data: { type: 'like' } })
  await flushPromises()
  assert.equal(findButton(mounted.root, 'feedback.dislike').props['aria-pressed'], true)

  await click(findButton(mounted.root, 'feedback.dislike'))
  await flushPromises()
  assert.deepEqual(calls, ['message-a:like', 'message-b:none'])
  assert.equal(findButton(mounted.root, 'feedback.dislike').props['aria-pressed'], false)
  mounted.app.unmount()
})
