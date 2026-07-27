import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'AgentStreamDisplay.vue'), 'utf8')

test('agent steps use compact muted timeline styling', () => {
  assert.match(source, /--agent-step-text-size:\s*14px/)
  assert.match(source, /--agent-step-summary-size:\s*13px/)
  assert.match(source, /--agent-step-icon-color:\s*var\(--td-text-color-placeholder\)/)
  assert.match(source, /max-height:\s*none/)
  assert.match(source, /overflow-y:\s*visible/)
  assert.match(source, /\.tree-root \.action-name\s*\{[\s\S]*font-size:\s*14px/)
  assert.match(source, /\.tree-child \.action-title-icon\s*\{[\s\S]*position:\s*absolute/)
  assert.match(source, /function maskIconStyle\(src: string, size = 18\)/)
  assert.match(source, /\.icon-mask\s*\{[\s\S]*background-color:\s*var\(--agent-step-icon-color\)/)
  assert.doesNotMatch(source, /\.action-title \.action-title-icon,\s*\n\s*\.icon-mask\s*\{/)
})

test('expanded agent step log keeps model thinking in the tool timeline', () => {
  assert.match(source, /visibleIntermediateEvents\s*=\s*computed\(\(\) => intermediateEvents\.value\)/)
  assert.match(source, /v-for="\(event, index\) in visibleIntermediateEvents"/)
})

test('streaming log renders reasoning alongside tool calls', () => {
  assert.match(source, /if \(!isConversationDone\.value\)\s*\{\s*return result;\s*\}/)
})

test('expanded model reasoning stays inline without a separate thinking title', () => {
  assert.match(source, /class="thinking-inline-content markdown-content"/)
  assert.match(source, /class="thinking-inline-markdown" v-html="renderMarkdownContent\(event\.content\)"/)
  assert.match(source, /event\.title && event\.content && isEventExpanded\(event\.event_id\)/)
  assert.match(source, /\.thinking-inline-title\s*\{[\s\S]*align-items:\s*flex-start/)
  assert.match(source, /\.thinking-inline-content\s*\{[\s\S]*margin-top:\s*0/)
  assert.doesNotMatch(source, /\.thinking-inline-title > \.action-title-icon/)
  assert.match(source, /\.tree-child \.thinking-event-card \.action-title\s*\{[\s\S]*position:\s*static/)
})

test('streaming tool log uses the same timeline structure', () => {
  assert.match(source, /'is-streaming-timeline': showStreamingTimeline/)
  assert.match(source, /'tree-child': isStreamingTimelineEvent\(event\)/)
  assert.match(source, /class="tree-child tree-child-last streaming-loading-node"/)
  assert.match(source, /chat-timeline-loading\.less/)
  assert.match(source, /lastStreamingTimelineEventIndex\s*=\s*computed/)
})

test('final done row uses an existing common translation key', () => {
  assert.match(source, /t\('common\.finish'\)/)
  assert.doesNotMatch(source, /\$t\('common\.done'\)/)
  assert.match(source, /'tree-child-last': !isConversationDone && index === visibleIntermediateEvents\.length - 1/)
})

test('tool rows use line icon names instead of legacy asset masks', () => {
  assert.match(source, /getAgentToolIconName/)
  assert.match(source, /:name="getToolIconName\(event\.tool_name\)"/)
  assert.match(source, /wiki_search: 'agentEditor\.tools\.wikiSearch'/)
  assert.match(source, /wiki_read_page: 'agentEditor\.tools\.wikiReadPage'/)
  assert.match(source, /wiki_read_source_doc: 'agentStream\.tools\.wikiReadSourceDoc'/)
  assert.match(source, /toolName === 'get_document_content' \|\| toolName === 'wiki_read_source_doc'/)
  assert.doesNotMatch(source, /getToolIcon\(event\.tool_name\)/)
})

test('rag mode delegates pre-answer loading to pipeline and adds no row after answer starts', () => {
  assert.match(source, /if \(props\.ragMode \|\| hasAnswerStarted\.value\) return false/)
  assert.match(source, /v-if="!ragMode \|\| displayEvents\.length > 0 \|\| showAgentActivityIndicator"/)
  assert.doesNotMatch(source, /ChatActivityIndicator/)
})

test('rag mode keeps model thinking out of the answer stream component', () => {
  const displayEventsBlock = source.slice(
    source.indexOf('const displayEvents = computed'),
    source.indexOf('// Get unique key for event'),
  )
  assert.match(displayEventsBlock, /if \(props\.ragMode\)\s*\{[\s\S]*e\.type === 'answer'/)
  assert.doesNotMatch(
    displayEventsBlock,
    /attachment_parsing/,
  )
  assert.doesNotMatch(
    displayEventsBlock,
    /if \(props\.ragMode\)\s*\{[\s\S]*e\.type === 'answer' \|\| e\.type === 'thinking'/,
  )
})

test('only the collapsed root summary shows an expand chevron', () => {
  assert.match(source, /tree-root-summary[\s\S]*class="action-show-icon"/)
  assert.match(source, /showIntermediateSteps \? 'chevron-down' : 'chevron-right'/)
  assert.doesNotMatch(source, /isEventExpanded\(event\.tool_call_id\) \? 'chevron/)
  assert.doesNotMatch(source, /isEventExpanded\(event\.event_id\) \? 'chevron/)
})

test('pending tool rows do not render an extra axis dot', () => {
  assert.doesNotMatch(source, /&\.action-pending\s*\{[\s\S]*&::after/)
})

test('agent mode shows a native placeholder before answer whenever nothing is pending', () => {
  assert.match(source, /if \(isConversationDone\.value\) return false/)
  assert.match(source, /return !hasPendingStreamingActivity\.value/)
  assert.match(source, /const hasPendingStreamingActivity = computed/)
  assert.match(source, /event\.thinking === true \|\| isThinkingActive\(event\.event_id\)/)
  assert.match(source, /event\.type === 'tool_approval_required' \|\| event\.type === 'mcp_oauth_required'/)
  assert.match(source, /class="action-card action-pending"/)
  assert.match(source, /t\('chat\.thinkingAlt'\)/)
  assert.match(source, /chat-timeline-loading\.less/)
})
