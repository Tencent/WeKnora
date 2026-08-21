import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const frontendRoot = join(here, '..', '..')
const chatView = readFileSync(join(frontendRoot, 'views', 'chat', 'index.vue'), 'utf8')
const streamHandler = readFileSync(
  join(frontendRoot, 'composables', 'useChatStreamHandler.ts'),
  'utf8',
)

test('main chat replaces an incomplete assistant row with recovery progress after refresh', () => {
  assert.match(chatView, /<StreamRecoveryProgress v-if="session\.isRecoveringStream" \/>/)
  assert.match(chatView, /<botmsg v-else/)
  assert.match(streamHandler, /markLatestIncompleteAssistantForRecovery\(messagesList\)/)
})

test('the first resumed event hands rendering back to the existing stream pipeline', () => {
  const processStart = streamHandler.indexOf('const processStreamChunk')
  const agentQueryStart = streamHandler.indexOf("if (data.response_type === 'agent_query')", processStart)
  const recoveryClear = streamHandler.indexOf(
    'clearMessageStreamRecovery(resolveActiveAssistantMessage(data))',
    processStart,
  )

  assert.ok(processStart >= 0)
  assert.ok(recoveryClear > processStart)
  assert.ok(agentQueryStart > recoveryClear)
})

test('stream recovery suppresses the unrelated global typing indicator', () => {
  assert.match(
    streamHandler,
    /last\?\.role === 'assistant' && last\.isRecoveringStream[\s\S]*return false/,
  )
})
