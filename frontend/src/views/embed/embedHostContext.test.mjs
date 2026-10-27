import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const embedChat = readFileSync(new URL('./EmbedChatCore.vue', import.meta.url), 'utf8')

test('strips injected host context when rendering persisted embed user messages', () => {
  const userMessage = embedChat.match(/<EmbedUserMessage\b([\s\S]*?)\/>/)?.[1]

  assert.ok(userMessage, 'expected an EmbedUserMessage renderer')
  assert.match(userMessage, /:content="stripHostContextFromQuery\(String\(session\.content \|\| ''\)\)"/)
})
