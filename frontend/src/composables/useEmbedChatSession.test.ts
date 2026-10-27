import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import * as historyPagination from '../utils/historyPagination.ts'
import * as suspendedRun from '../utils/suspendedRun.ts'
import type { useEmbedChatSession as EmbedChatSession } from './useEmbedChatSession.ts'

const require = createRequire(import.meta.url)
const source = readFileSync(new URL('./useEmbedChatSession.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

const flush = async () => {
  await Promise.resolve()
  await Promise.resolve()
  await new Promise<void>((resolve) => setImmediate(resolve))
}

test('embedded history remains retryable after a transient page failure', async (t) => {
  const requests: string[] = []
  const page = (count: number, prefix: string) => Array.from({ length: count }, (_, index) => ({
    id: `${prefix}-${index}`,
    role: index % 2 === 0 ? 'user' : 'assistant',
    content: `${prefix} message ${index}`,
    created_at: `2026-10-12T04:${String(index).padStart(2, '0')}:00Z`,
  }))
  const responses = [
    () => Promise.resolve({ data: page(20, 'latest') }),
    () => Promise.reject(new Error('temporary network failure')),
    () => Promise.resolve({ data: page(19, 'older') }),
  ]

  const exports: { useEmbedChatSession?: typeof EmbedChatSession } = {}
  vm.runInNewContext(compiled, {
    exports,
    console,
    queueMicrotask,
    setTimeout: (callback: () => void) => {
      queueMicrotask(callback)
      return 1
    },
    clearTimeout: () => {},
    require: (id: string) => {
      if (id === 'vue') return { ...vue, onMounted: (callback: () => void) => callback(), onUnmounted: () => {} }
      if (id === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (id === '@/utils/historyPagination') return historyPagination
      if (id === '@/utils/suspendedRun') return suspendedRun
      if (id === '@/utils/embedToast') return { embedToast: () => {} }
      if (id === '@/utils/embedContext') return { buildQueryWithHostContext: (value: string) => value }
      if (id === '@/utils/embedFile') return { fileToDataURI: async () => '' }
      if (id === '@/composables/useStickyBottomOnResize') return { useStickyBottomOnResize: () => {} }
      if (id === '@/api/chat/streame') {
        return {
          useStream: () => ({
            onChunk: () => {},
            error: vue.ref(''),
            startStream: async () => {},
            stopStream: () => {},
          }),
        }
      }
      if (id === '@/api/embed') {
        return {
          getEmbedMessageList: (_channel: string, _token: string, sessionId: string) => {
            requests.push(sessionId)
            return responses[requests.length - 1]()
          },
          postEmbedMessageSent: () => {},
          postEmbedMessageReceived: () => {},
          relayEmbedWebhookEvent: () => {},
          stopEmbedSession: async () => {},
        }
      }
      if (id === '@/composables/useChatStreamHandler') {
        return {
          useChatStreamHandler: () => ({
            shouldRenderAssistantMessage: () => true,
            shouldShowGlobalTypingIndicator: vue.computed(() => false),
            handleMsgList: async () => {},
            processStreamChunk: () => {},
            prepareForNewOutgoingMessage: () => {},
            markInFlightAssistantStopped: () => {},
          }),
        }
      }
      throw new Error(`Unexpected import: ${id}`)
    },
  })

  const effects = vue.effectScope()
  t.after(() => effects.stop())
  const session = effects.run(() => exports.useEmbedChatSession!({
    sessionId: vue.ref('session-1'),
    sessionSig: vue.ref('signature'),
    visitorId: vue.ref('visitor'),
    channelId: 'channel',
    token: 'token',
    agentId: 'agent',
    kbIds: [],
  }))!
  session.scrollContainer.value = {
    scrollTop: 0,
    scrollHeight: 400,
    clientHeight: 200,
  } as HTMLElement

  await flush()
  assert.equal(requests.length, 1, 'initial history page should load')

  session.handleScroll()
  await flush()
  assert.equal(requests.length, 2, 'first top-scroll should reach the failed request')

  session.handleScroll()
  await flush()
  assert.equal(requests.length, 3, 'next top-scroll should retry after connectivity recovers')
})
