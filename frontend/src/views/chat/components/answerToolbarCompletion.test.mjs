import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const botMessage = readFileSync(new URL('./botmsg.vue', import.meta.url), 'utf8')
const agentStream = readFileSync(new URL('./AgentStreamDisplay.vue', import.meta.url), 'utf8')
const chatView = readFileSync(new URL('../index.vue', import.meta.url), 'utf8')
const embedChat = readFileSync(new URL('../../embed/EmbedChatCore.vue', import.meta.url), 'utf8')
const sharedStyles = readFileSync(
  new URL('../../../components/css/chat-message-shared.less', import.meta.url),
  'utf8',
)

test('non-agent actions wait for the typewriter buffer to finish', () => {
  assert.match(botMessage, /const answerFullyRendered = computed/)
  assert.match(botMessage, /typedAnswer\.value\.length >= answerText\.value\.length/)
  assert.match(botMessage, /v-if="answerFullyRendered && \(content \|\| session\.content\)"/)
})

test('agent actions reuse the fully-rendered answer state', () => {
  assert.match(agentStream, /const answerFullyRendered = computed/)
  assert.match(agentStream, /typedAnswer\.value\.length >= activeAnswerMarkdown\.value\.length/)
  assert.match(agentStream, /v-if="answerFullyRendered && event\.done/)
})

test('artifact count is a top-right overlay instead of t-badge', () => {
  assert.match(botMessage, /class="answer-toolbar__artifact"/)
  assert.match(agentStream, /class="answer-toolbar__artifact"/)
  assert.match(botMessage, /class="answer-toolbar__artifact-count"/)
  assert.match(agentStream, /class="answer-toolbar__artifact-count"/)
  assert.match(sharedStyles, /answer-toolbar__artifact-count/)
  assert.match(sharedStyles, /position: absolute/)
  assert.match(sharedStyles, /td-bg-color-secondarycontainer/)
  assert.doesNotMatch(botMessage, /<t-badge/)
  assert.doesNotMatch(agentStream, /<t-badge/)
})

test('artifact toolbar uses a folder icon and replaces it while collecting', () => {
  assert.match(botMessage, /answer-toolbar__artifact[\s\S]{0,800}name="folder"/)
  assert.match(agentStream, /answer-toolbar__artifact[\s\S]{0,800}name="folder"/)
  assert.match(botMessage, /class="answer-toolbar__artifact-spinner"/)
  assert.match(agentStream, /class="answer-toolbar__artifact-spinner"/)
  assert.match(sharedStyles, /answer-toolbar-artifact-spin/)
  assert.doesNotMatch(botMessage, /answer-toolbar__artifact[\s\S]{0,800}:loading=/)
  assert.doesNotMatch(agentStream, /answer-toolbar__artifact[\s\S]{0,800}:loading=/)
})

test('follow-up loading is shown compactly inside both answer toolbars', () => {
  assert.match(chatView, /:follow-up-loading="Boolean\(session\.suggestionLoading/)
  assert.match(botMessage, /class="answer-toolbar__follow-up-loading"/)
  assert.match(agentStream, /class="answer-toolbar__follow-up-loading"/)
  assert.match(botMessage, /class="answer-toolbar__follow-up-label"/)
  assert.match(botMessage, /transition name="follow-up-toolbar-loading"/)
  assert.match(agentStream, /transition name="follow-up-toolbar-loading"/)
  assert.match(sharedStyles, /border-left: 1px solid/)
  assert.match(sharedStyles, /font-size: 12px/)
  assert.match(sharedStyles, /followUpToolbarShimmer 1\.5s linear infinite/)
  assert.match(sharedStyles, /background-clip: text/)
  assert.match(sharedStyles, /follow-up-toolbar-loading-leave-to/)
})

test('conversation timestamps insert into the message flow instead of each bubble', () => {
  assert.match(chatView, /shouldShowConversationTimestamp\(messagesList, index\)/)
  assert.match(embedChat, /shouldShowConversationTimestamp\(messagesList, index\)/)
  assert.doesNotMatch(chatView, /align="end"/)
  assert.doesNotMatch(chatView, /align="start"/)
  assert.doesNotMatch(embedChat, /align="end"/)
  assert.doesNotMatch(embedChat, /align="start"/)
})

test('follow-up suggestions wait until the answer is fully rendered', () => {
  assert.match(
    chatView,
    /@render-complete-change="\(ready\) => handleAnswerRenderComplete\(session, ready\)"/,
  )
  assert.match(
    chatView,
    /<FollowUpSuggestions v-if="session\.answerFullyRendered && !session\.suggestionsDismissed"/,
  )
  assert.match(botMessage, /emit\('render-complete-change', ready\)/)
  assert.match(agentStream, /emit\('render-complete-change', ready\)/)
})
