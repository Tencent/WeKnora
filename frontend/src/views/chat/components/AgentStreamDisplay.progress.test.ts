import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, ref } from 'vue'
import { useDeferredTimelineCompletion } from '../../../composables/useChatScrollFollow'
import { foldProgressIntoThinking } from '../../../utils/agentTimelinePresentation'

const source = readFileSync(new URL('./AgentStreamDisplay.vue', import.meta.url), 'utf8')

test('delegated work keeps progress and approvals in one chronological stream and restores the completed steps fold without duplicating child entry points', () => {
  const collapse = source.slice(source.indexOf('const shouldShowCollapsedSteps ='), source.indexOf('// Once the steps collapse'))
  const display = source.slice(source.indexOf('const displayEvents ='), source.indexOf('const isPageLinkEvent ='))
  const done = ref(false)
  const delegated = ref(false)
  const expanded = ref(false)
  const readingHistory = ref(false)
  const stream = ref<any[]>([
    { type: 'thinking', event_id: 'r1', content: 'Check agents' },
    { type: 'progress', event_id: 'p1', content: '两个智能体已经并行开工了。' },
    { type: 'tool_approval_required', pending_id: 'approval', resolved: false },
  ])
  const code = ts.transpileModule(`${collapse}\n${display}\n({ shouldShowCollapsedSteps, displayEvents })`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText
  const state = vm.runInNewContext(code, {
    computed, props: { ragMode: false }, hasCollaborationTasks: delegated, showIntermediateSteps: expanded,
    eventStream: stream, isConversationDone: done, intermediateStepsCount: ref(2), readingHistory, timelineCompleted: useDeferredTimelineCompletion(done, readingHistory),
    buildFullEventList: foldProgressIntoThinking, finalContent: ref(null),
  })
  for (const hasChildren of [false, true]) {
    delegated.value = hasChildren
    assert.equal(state.shouldShowCollapsedSteps.value, false)
    assert.deepEqual(Array.from(state.displayEvents.value, (e: any) => e.type), ['thinking', 'tool_approval_required'])
    assert.equal(state.displayEvents.value[0].title, '两个智能体已经并行开工了。')
    assert.equal(state.displayEvents.value[1].resolved, false)
  }
  stream.value[2].resolved = true
  assert.equal(state.displayEvents.value[1].resolved, true)
  stream.value.push({ type: 'answer', content: '完整结果' })
  stream.value.splice(2, 0, { type: 'child_task', event_id: 'child-a', task: { id: 'a' } })
  readingHistory.value = true
  done.value = true
  assert.equal(state.shouldShowCollapsedSteps.value, false, 'completion must not fold history under the reader')
  assert.ok(state.displayEvents.value.some((event: any) => event.type === 'thinking'))
  readingHistory.value = false
  assert.equal(state.shouldShowCollapsedSteps.value, true)
  assert.deepEqual(Array.from(state.displayEvents.value, (e: any) => e.type), ['answer'])
  readingHistory.value = true
  assert.equal(state.shouldShowCollapsedSteps.value, true, 'older completed timelines stay folded when reading history')
  readingHistory.value = false
  expanded.value = true
  assert.deepEqual(Array.from(state.displayEvents.value, (e: any) => e.type), ['answer'])
  expanded.value = false
  stream.value = stream.value.filter(e => e.type !== 'answer')
  assert.equal(state.displayEvents.value.length, 0)
  expanded.value = true
  assert.equal(state.displayEvents.value.length, 0)
  stream.value = [{ type: 'answer', content: '完整结果' }]
  delegated.value = false
  assert.equal(state.shouldShowCollapsedSteps.value, true)
  assert.deepEqual(Array.from(state.displayEvents.value, (e: any) => e.content), ['完整结果'])
})

test('the template has no second latest-progress outlet above the timeline', () => {
  assert.doesNotMatch(source, /CollaborationProgress|latestParentProgress/)
})


test('live child waiting uses the timeline tail icon and existing pending shimmer', () => {
  const template = source.slice(0, source.indexOf('</template>\n\n<script'))
  const tail = template.slice(template.indexOf('<!-- One live tail node'), template.indexOf('<!-- 引用 hover'))
  assert.match(tail, /v-if="showAgentActivityIndicator \|\| childActivity"/)
  assert.match(tail, /class="tree-child tree-child-last streaming-loading-node"/)
  assert.match(tail, /role="status" aria-live="polite"/)
  assert.match(tail, /class="action-card action-pending"/)
  assert.match(tail, /<ChatTimelineIcon class="action-title-icon" name="lightbulb"/)
  assert.match(tail, /class="action-name"/)
  assert.match(template, /!showAgentActivityIndicator && !childActivity && index === lastStreamingTimelineEventIndex/)
  assert.doesNotMatch(template, /agent-child-activity|child-wait-spinner|child_wait/)
})

test('thinking resumes after child waiting and resolved approval', () => {
  const snippet = source.slice(source.indexOf('const hasPendingStreamingActivity ='), source.indexOf('const isStreamingTimelineEvent ='))
  const events = ref<any[]>([]), done = ref(false), wait = ref('finished'), answering = ref(false)
  const code = ts.transpileModule(`${snippet}\n({ showAgentActivityIndicator })`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText
  const state = vm.runInNewContext(code, {
    computed, displayEvents: events, isSegmentDone: done, hasCollaborationTasks: ref(true),
    props: { ragMode: false }, hasAnswerStarted: answering, collaborationWait: wait,
    isThinkingActive: () => false,
    isUnresolvedCollaborationPrompt: (event: any) => event.type === 'tool_approval_required' && !event.resolved,
  })
  assert.equal(state.showAgentActivityIndicator.value, true)
  events.value = [{ type: 'tool_approval_required', resolved: true }]
  assert.equal(state.showAgentActivityIndicator.value, true)
  events.value[0].resolved = false
  assert.equal(state.showAgentActivityIndicator.value, false)
  events.value = []
  wait.value = 'waiting'
  assert.equal(state.showAgentActivityIndicator.value, false)
  wait.value = 'finished'
  answering.value = true
  assert.equal(state.showAgentActivityIndicator.value, false)
  answering.value = false
  done.value = true
  assert.equal(state.showAgentActivityIndicator.value, false)
})
