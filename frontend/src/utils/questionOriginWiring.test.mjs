import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

// A picked suggestion's origin must travel with that one send. Shared
// "pending" state was lost when a send returned early (agent not ready,
// upload in progress, steering a running turn) and was overwritten when a
// second suggestion was picked before the first send went out.
const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8')
const inputField = read('../components/Input-field.vue')
const homePage = read('../views/creatChat/creatChat.vue')
const chatPage = read('../views/chat/index.vue')

test('the composer forwards send options from triggerSend to send-msg', () => {
  assert.match(inputField, /triggerSend\(text: string, options: SendMessageOptions = \{\}\)/)
  assert.match(inputField, /nextTick\(\(\) => createSession\(text, 'after', options\)\)/)
  const emits = inputField.match(/emit\('send-msg',[^\n]*\)/g) || []
  assert.ok(emits.length >= 2)
  for (const call of emits) {
    assert.match(call, /, options\)$/, `send-msg must forward options: ${call}`)
  }
})

test('pages pass the origin through the send instead of shared pending state', () => {
  for (const [name, source] of [['creatChat.vue', homePage], ['chat/index.vue', chatPage]]) {
    assert.doesNotMatch(source, /pendingQuestionOrigin/, `${name} must not keep a pending origin`)
    assert.match(source, /triggerSend\(item\.question, \{ questionOrigin: questionOriginFromSuggestion\(item\) \}\)|triggerSend\(item\.question, options\)/, name)
  }
  assert.match(homePage, /changeFirstQuery\([^)]*options\.questionOrigin \?\? null\)/)
  assert.match(chatPage, /@send-msg="\([^"]*options\) => sendMsg\([^"]*options\)"/)
  assert.match(chatPage, /question_origin: options\?\.questionOrigin/)
  assert.match(chatPage, /questionOrigin: firstQuestionOrigin\.value \|\| undefined/)
})
