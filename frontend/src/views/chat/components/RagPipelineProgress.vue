<template>
  <div v-if="visible" ref="rootElement" class="rag-pipeline-progress">
    <div v-if="showPrePipelineWait" class="tree-children">
      <div class="tree-child tree-child-last streaming-loading-node">
        <div class="tree-branch" />
        <div class="tree-child-content">
          <div class="loading-indicator">
            <div class="loading-typing">
              <span />
              <span />
              <span />
            </div>
          </div>
        </div>
      </div>
    </div>

    <div v-else-if="!showCollapsedRoot" class="tree-children">
      <div v-for="(step, index) in steps" :key="step.id" class="tree-child" :class="{
        'tree-child-last':
          !showDoneRow
          && !hasReferences
          && !showThinkingStep
          && index === steps.length - 1,
      }">
        <div class="tree-branch" />
        <div class="tree-child-content">
          <div class="tool-event">
            <div class="action-card">
              <div class="action-header no-results">
                <div class="action-title">
                  <t-icon class="action-title-icon" :name="step.iconName" />
                  <span class="action-name" :class="{ 'is-running': step.pending }">{{ step.title }}</span>
                </div>
              </div>
              <div v-if="step.summaryHtml" class="search-results-summary-fixed">
                <div class="results-summary-text" v-html="step.summaryHtml" />
              </div>
            </div>
          </div>
        </div>
      </div>

      <div v-if="hasReferences" class="tree-child rag-ref-step"
        :class="{ 'tree-child-last': !showThinkingStep && !showDoneRow }">
        <div class="tree-branch" />
        <div class="tree-child-content">
          <div class="tool-event">
            <div class="action-card">
              <div class="action-header" @click="toggleReferences">
                <div class="action-title">
                  <t-icon class="action-title-icon" name="file-search" />
                  <span class="action-name">{{ referencesHeaderText }}</span>
                </div>
              </div>
              <DocInfo v-show="refsExpanded" :session="session" :embedded-mode="embeddedMode" timeline-mode
                content-only />
            </div>
          </div>
        </div>
      </div>

      <div v-if="showThinkingStep" class="tree-child rag-thinking-step"
        :class="{ 'tree-child-last': !showDoneRow }">
        <div class="tree-branch" />
        <div class="tree-child-content">
          <div class="tool-event">
            <div class="action-card" :class="{ 'action-pending': thinkingPending }">
              <div class="action-header" :class="{ 'no-results': !thinkingContent }" @click="toggleThinking">
                <div class="action-title">
                  <t-icon class="action-title-icon" name="lightbulb" />
                  <span class="action-name">{{ t('agent.think') }}</span>
                </div>
              </div>
              <div v-if="thinkingPending && !thinkingContent" class="thinking-loading">
                <div class="loading-typing">
                  <span />
                  <span />
                  <span />
                </div>
              </div>
              <div v-else-if="thinkingContent && thinkingExpanded" class="thinking-detail-content">
                {{ thinkingContent }}
              </div>
            </div>
          </div>
        </div>
      </div>

      <div v-if="showDoneRow" class="tree-child agent-step-done tree-child-last">
        <div class="tree-branch" />
        <div class="tree-child-content">
          <div class="tool-event">
            <div class="action-card">
              <div class="action-header no-results">
                <div class="action-title">
                  <t-icon class="action-title-icon" name="check-circle" />
                  <span class="action-name">{{ t('common.finish') }}</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div v-else class="tree-container">
      <div class="tool-event">
        <div class="action-card tree-root" @click="toggleExpanded">
          <div class="action-header">
            <div class="action-title">
              <span class="action-name tree-root-summary" v-html="collapsedSummaryHtml" />
              <div class="action-show-icon">
                <t-icon :name="showExpandedTimeline ? 'chevron-down' : 'chevron-right'" />
              </div>
            </div>
          </div>
        </div>
      </div>

      <div v-if="showExpandedTimeline" class="tree-children tree-children-expanded">
        <div v-for="(step, index) in steps" :key="step.id" class="tree-child"
          :class="{ 'tree-child-last': index === steps.length - 1 && !hasReferences && !showDoneRow && !showThinkingStep }">
          <div class="tree-branch" />
          <div class="tree-child-content">
            <div class="tool-event">
              <div class="action-card">
                <div class="action-header no-results">
                  <div class="action-title">
                    <t-icon class="action-title-icon" :name="step.iconName" />
                    <span class="action-name" :class="{ 'is-running': step.pending }">{{ step.title }}</span>
                  </div>
                </div>
                <div v-if="step.summaryHtml" class="search-results-summary-fixed">
                  <div class="results-summary-text" v-html="step.summaryHtml" />
                </div>
              </div>
            </div>
          </div>
        </div>

        <div v-if="hasReferences" class="tree-child rag-ref-step"
          :class="{ 'tree-child-last': !showThinkingStep && !showDoneRow }">
          <div class="tree-branch" />
          <div class="tree-child-content">
            <div class="tool-event">
              <div class="action-card">
                <div class="action-header" @click="toggleReferences">
                  <div class="action-title">
                    <t-icon class="action-title-icon" name="file-search" />
                    <span class="action-name">{{ referencesHeaderText }}</span>
                  </div>
                </div>
                <DocInfo v-show="refsExpanded" :session="session" :embedded-mode="embeddedMode" timeline-mode
                  content-only />
              </div>
            </div>
          </div>
        </div>

        <div v-if="showThinkingStep" class="tree-child rag-thinking-step" :class="{ 'tree-child-last': !showDoneRow }">
          <div class="tree-branch" />
          <div class="tree-child-content">
            <div class="tool-event">
              <div class="action-card" :class="{ 'action-pending': thinkingPending }">
                <div class="action-header" :class="{ 'no-results': !thinkingContent }" @click="toggleThinking">
                  <div class="action-title">
                    <t-icon class="action-title-icon" name="lightbulb" />
                    <span class="action-name">{{ t('agent.think') }}</span>
                  </div>
                </div>
                <div v-if="thinkingPending && !thinkingContent" class="thinking-loading">
                  <div class="loading-typing">
                    <span />
                    <span />
                    <span />
                  </div>
                </div>
                <div v-else-if="thinkingContent && thinkingExpanded" class="thinking-detail-content">
                  {{ thinkingContent }}
                </div>
              </div>
            </div>
          </div>
        </div>

        <div v-if="showDoneRow" class="tree-child agent-step-done tree-child-last">
          <div class="tree-branch" />
          <div class="tree-child-content">
            <div class="tool-event">
              <div class="action-card">
                <div class="action-header no-results">
                  <div class="action-title">
                    <t-icon class="action-title-icon" name="check-circle" />
                    <span class="action-name">{{ t('common.finish') }}</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import DocInfo from './docInfo.vue'
import { getAgentToolIconName } from '@/utils/agent-tool-icons'
import {
  getKnowledgeSearchSummaryHtml,
  getRagPipelineStepTitle,
} from '@/utils/agent-tool-display'
import { RAG_PIPELINE_TOOL_NAMES } from '@/utils/rag-pipeline-history'

const props = defineProps<{
  session?: {
    agentEventStream?: Array<Record<string, unknown>>
    content?: string
    knowledge_references?: Array<{ chunk_type?: string; knowledge_id?: string; knowledge_title?: string }>
    is_completed?: boolean
  }
  embeddedMode?: boolean
}>()

const { t } = useI18n()
const userExpanded = ref(false)
const refsExpanded = ref(false)
const thinkingExpanded = ref(true)
const rootElement = ref<HTMLElement | null>(null)

const thinkingContent = computed(() => {
  const stream = props.session?.agentEventStream
  if (!Array.isArray(stream)) return ''
  return stream
    .filter((event) => event.type === 'thinking')
    .map((event) => String(event.content || ''))
    .join('')
})

const hasThinking = computed(() => thinkingContent.value.trim().length > 0)

const hasThinkingEvent = computed(() => {
  const stream = props.session?.agentEventStream
  if (!Array.isArray(stream)) return false
  return stream.some((event) => event.type === 'thinking')
})

const hasAnswer = computed(() => {
  const sessionContent = props.session?.content
  if (typeof sessionContent === 'string' && sessionContent.trim().length > 0) return true

  const stream = props.session?.agentEventStream
  if (!stream?.length) return false
  return stream.some((event) => {
    if (event.type !== 'answer' || event.superseded) return false
    const content = event.content
    return typeof content === 'string' && content.trim().length > 0
  })
})

const hasReferences = computed(
  () => (props.session?.knowledge_references?.length ?? 0) > 0,
)

const steps = computed(() => {
  const stream = props.session?.agentEventStream
  if (!stream?.length) return []

  return stream
    .filter((event) => {
      return (
        event.type === 'tool_call' &&
        typeof event.tool_name === 'string' &&
        RAG_PIPELINE_TOOL_NAMES.has(event.tool_name)
      )
    })
    .map((event) => {
      const toolName = String(event.tool_name)
      const pending = event.pending === true
      const toolData =
        event.tool_data && typeof event.tool_data === 'object'
          ? (event.tool_data as Record<string, unknown>)
          : null

      const isSearchTool = toolName === 'knowledge_search' || toolName === 'search_knowledge'
      const summaryHtml =
        !pending && isSearchTool && toolData
          ? getKnowledgeSearchSummaryHtml(t, toolData)
          : ''

      return {
        id: String(event.tool_call_id || `${toolName}-${event.timestamp || 0}`),
        pending,
        iconName: getAgentToolIconName(toolName),
        title: getRagPipelineStepTitle(t, {
          tool_name: toolName,
          pending,
          success: event.success as boolean | undefined,
          arguments: event.arguments,
          tool_data: toolData,
        }),
        summaryHtml,
      }
    })
})

const allStepsDone = computed(
  () => steps.value.length > 0 && steps.value.every((step) => !step.pending),
)

const showCollapsedRoot = computed(
  () =>
    (hasAnswer.value || Boolean(props.session?.is_completed)) &&
    (steps.value.length > 0 || hasThinking.value),
)

const showExpandedTimeline = computed(() => {
  if (!showCollapsedRoot.value) return true
  return userExpanded.value
})

const showDoneRow = computed(() => {
  const turnDone = hasAnswer.value || Boolean(props.session?.is_completed)
  if (!turnDone) return false
  if (steps.value.length > 0 && !allStepsDone.value) return false
  return true
})

const showPrePipelineWait = computed(() => {
  if (hasAnswer.value || props.session?.is_completed || steps.value.length > 0 || hasThinking.value) {
    return false
  }
  return true
})

// Only show the thinking row once the backend actually streams thinking events.
// Do not pre-empt during the model phase — that flashes "思考" even when thinking is disabled.
const showThinkingStep = computed(() => hasThinkingEvent.value)

const thinkingPending = computed(
  () =>
    showThinkingStep.value &&
    !hasThinking.value &&
    !hasAnswer.value &&
    !props.session?.is_completed,
)

const isThinkingStreaming = computed(
  () =>
    showThinkingStep.value &&
    thinkingExpanded.value &&
    !hasAnswer.value &&
    !props.session?.is_completed,
)

const visible = computed(
  () => steps.value.length > 0 || showPrePipelineWait.value || showThinkingStep.value,
)

const referenceDocCount = computed(() => {
  const refs = props.session?.knowledge_references ?? []
  const keys = new Set<string>()
  for (const item of refs) {
    if (item.chunk_type === 'web_search') continue
    keys.add(item.knowledge_id || item.knowledge_title || 'doc')
  }
  return keys.size
})

const referenceWebCount = computed(() => {
  const refs = props.session?.knowledge_references ?? []
  return refs.filter((item) => item.chunk_type === 'web_search').length
})

const referencesHeaderText = computed(() => {
  const docCount = referenceDocCount.value
  const webCount = referenceWebCount.value
  const total = props.session?.knowledge_references?.length ?? 0

  if (docCount > 0 && webCount > 0) {
    return t('chat.referencesDocAndWebCount', { docCount, webCount })
  }
  if (docCount > 0) {
    return t('chat.referencesDocCount', { count: docCount })
  }
  return t('chat.referencesTitle', { count: total })
})

const collapsedSummaryHtml = computed(() => {
  if (steps.value.length === 0) {
    return hasThinking.value ? t('agentStream.toolStatus.thinkingDone') : ''
  }

  const parts: string[] = [t('agentStream.ragPipeline.searchDone')]
  const docCount = referenceDocCount.value
  const webCount = referenceWebCount.value

  if (docCount > 0 && webCount > 0) {
    parts.push(
      t('agentStream.ragPipeline.referencedDocAndWeb', {
        docCount: `<strong>${docCount}</strong>`,
        webCount: `<strong>${webCount}</strong>`,
      }),
    )
  } else if (docCount > 0) {
    parts.push(
      t('agentStream.ragPipeline.referencedDocs', {
        count: `<strong>${docCount}</strong>`,
      }),
    )
  } else if (webCount > 0) {
    parts.push(
      t('agentStream.ragPipeline.referencedWebs', {
        count: `<strong>${webCount}</strong>`,
      }),
    )
  }

  return parts.join(t('agent.stepSummarySeparator'))
})

function toggleExpanded() {
  userExpanded.value = !userExpanded.value
}

function toggleReferences() {
  refsExpanded.value = !refsExpanded.value
}

function toggleThinking() {
  if (!showThinkingStep.value || !thinkingContent.value) return
  thinkingExpanded.value = !thinkingExpanded.value
}

function scrollThinkingDetailToBottom() {
  nextTick(() => {
    if (!rootElement.value) return
    rootElement.value.querySelectorAll('.thinking-detail-content').forEach((el) => {
      const htmlEl = el as HTMLElement
      htmlEl.scrollTop = htmlEl.scrollHeight
    })
  })
}

watch(thinkingPending, (pending) => {
  if (pending) {
    thinkingExpanded.value = true
  }
})

watch(hasAnswer, (answered) => {
  if (answered && hasThinking.value) {
    thinkingExpanded.value = false
  }
})

watch(thinkingContent, () => {
  if (!isThinkingStreaming.value) return
  scrollThinkingDetailToBottom()
})

watch(thinkingExpanded, (expanded) => {
  if (!expanded || !isThinkingStreaming.value) return
  scrollThinkingDetailToBottom()
})
</script>

<style scoped lang="less">
@import '@/components/css/chat-timeline-loading.less';

.rag-pipeline-progress {
  --agent-step-text-size: 14px;
  --agent-step-summary-size: 13px;
  --agent-step-line-color: color-mix(in srgb, var(--td-text-color-primary) 16%, transparent);
  --agent-step-icon-color: var(--td-text-color-placeholder);

  margin: 0;
}

.tree-container {
  margin: 0 0 16px;
  position: relative;
}

.tree-root {
  cursor: pointer;
  color: var(--td-text-color-secondary);
  margin-bottom: 0;

  .action-header {
    display: flex;
    align-items: center;
    min-height: 24px;
    padding: 0;
  }

  .action-title {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    flex: 0 1 auto;
    min-width: 0;
  }

  .tree-root-summary {
    font-size: 14px;
    line-height: 1.55;
    color: var(--td-text-color-secondary);

    :deep(strong) {
      font-weight: 600;
      color: var(--td-text-color-primary);
    }
  }

  .action-show-icon {
    color: var(--td-text-color-placeholder);
    font-size: 14px;
    flex-shrink: 0;
  }
}

.tree-children {
  position: relative;
  padding-left: 0;
  margin-top: 0;
  margin-left: 10px;
}

.tree-children-expanded {
  margin-top: 14px;
}

.tree-child {
  position: relative;
  padding-left: 42px;
  padding-bottom: 0;
  margin-bottom: 18px;

  &::before {
    content: '';
    position: absolute;
    left: 9px;
    top: 22px;
    bottom: -18px;
    width: 0;
    border-left: 1px solid var(--agent-step-line-color);
  }

  .tree-branch {
    display: none;
  }

  &.tree-child-last {
    margin-bottom: 0;

    &::before {
      content: none;
    }
  }
}

.rag-ref-step {
  .action-header {
    width: 100%;
    gap: 8px;
  }

  .action-title {
    gap: 12px;
  }

  .action-show-icon {
    color: var(--td-text-color-placeholder);
    font-size: 14px;
    flex-shrink: 0;
  }

  :deep(.refer-timeline.refer) {
    margin-top: 4px;
    padding-left: 0;
  }

  :deep(.refer-timeline .doc-group-chunks) {
    padding-left: 18px;
  }
}

.tool-event {
  .action-card {
    position: relative;
    background: transparent;
    border: 0;
    box-shadow: none;
  }

  .action-header {
    display: flex;
    align-items: center;
    min-height: 24px;
    padding: 0;
    cursor: pointer;
    user-select: none;

    &.no-results {
      cursor: default;
    }
  }

  .action-title {
    display: flex;
    align-items: center;
    gap: 12px;
    position: relative;
    flex: 0 1 auto;
    min-width: 0;

    .action-show-icon {
      flex-shrink: 0;
      margin-left: 2px;
    }
  }

  .action-title-icon {
    position: absolute;
    left: -42px;
    top: 3px;
    width: 18px;
    height: 18px;
    flex-shrink: 0;
    color: var(--agent-step-icon-color);
  }

  .action-name {
    font-size: var(--agent-step-text-size);
    line-height: 1.55;
    font-weight: 400;
    color: var(--td-text-color-secondary);
    word-break: break-word;
    max-width: min(680px, 100%);
  }
}

.search-results-summary-fixed {
  padding: 2px 0 0 0;

  .results-summary-text {
    font-size: var(--agent-step-summary-size);
    font-weight: 400;
    color: var(--td-text-color-secondary);
    line-height: 1.5;

    :deep(strong) {
      color: var(--td-text-color-secondary);
      font-weight: 500;
    }
  }
}

.rag-thinking-step {
  .thinking-loading {
    padding: 4px 0 0;
  }

  .thinking-detail-content {
    margin-top: 4px;
    padding: 0;
    font-size: var(--agent-step-summary-size);
    font-weight: 400;
    color: var(--td-text-color-placeholder);
    line-height: 1.55;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 200px;
    overflow-y: auto;
  }

  .action-pending .action-name {
    color: var(--td-text-color-secondary);
  }
}
</style>
