<template>
  <div v-if="visible" ref="rootElement" class="rag-pipeline-progress">
    <div v-if="showPrePipelineWait" class="tree-children">
      <div class="tree-child tree-child-last streaming-loading-node">
        <div class="tree-branch" />
        <div class="tree-child-content">
          <div class="action-card action-pending">
            <div class="action-header no-results">
              <div class="action-title">
                <t-icon class="action-title-icon" name="lightbulb" />
                <span class="action-name">{{ t('chat.thinkingAlt') }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div v-else-if="!showCollapsedRoot" class="tree-children">
      <div v-for="(step, index) in steps" :key="step.id" class="tree-child" :class="{
        'tree-child-last':
          !showDoneRow
          && !showThinkingStep
          && index === steps.length - 1,
      }">
        <div class="tree-branch" />
        <div class="tree-child-content">
          <div class="tool-event">
            <div
              class="action-card"
              :class="{ 'has-reference-trigger': step.canOpenReferences }"
              :role="step.canOpenReferences ? 'button' : undefined"
              :tabindex="step.canOpenReferences ? 0 : undefined"
              @click="handleStepClick(step)"
              @keydown.enter="handleStepClick(step)"
              @keydown.space.prevent="handleStepClick(step)"
            >
              <div
                class="action-header"
                :class="{ 'no-results': !step.canOpenReferences }"
              >
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
              <div v-if="thinkingContent && thinkingExpanded" class="thinking-detail-content">
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
        <div class="action-card tree-root">
          <div class="tree-root-toolbar">
            <button
              type="button"
              class="tree-root-expand"
              :aria-expanded="showExpandedTimeline"
              :aria-label="collapsedStatusText"
              @click="toggleExpanded"
            >
              <span class="tree-root-status">{{ collapsedStatusText }}</span>
              <span
                v-if="referenceSummaryText"
                class="tree-root-reference"
              >
                {{ referenceSummaryText }}
              </span>
              <t-icon
                class="tree-root-expand__icon"
                :name="showExpandedTimeline ? 'chevron-down' : 'chevron-right'"
              />
            </button>
          </div>
        </div>
      </div>

      <div v-if="showExpandedTimeline" class="tree-children tree-children-expanded">
        <div v-for="(step, index) in steps" :key="step.id" class="tree-child"
          :class="{ 'tree-child-last': index === steps.length - 1 && !showDoneRow && !showThinkingStep }">
          <div class="tree-branch" />
          <div class="tree-child-content">
            <div class="tool-event">
              <div
                class="action-card"
                :class="{ 'has-reference-trigger': step.canOpenReferences }"
                :role="step.canOpenReferences ? 'button' : undefined"
                :tabindex="step.canOpenReferences ? 0 : undefined"
                @click="handleStepClick(step)"
                @keydown.enter="handleStepClick(step)"
                @keydown.space.prevent="handleStepClick(step)"
              >
                <div
                  class="action-header"
                  :class="{ 'no-results': !step.canOpenReferences }"
                >
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
                <div v-if="thinkingContent && thinkingExpanded" class="thinking-detail-content">
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
import { getAgentToolIconName } from '@/utils/agent-tool-icons'
import {
  getKnowledgeSearchSummaryHtml,
  getRagPipelineStepTitle,
  getRetrievalSearchSource,
} from '@/utils/agent-tool-display'
import { getAttachmentParsingSummaryHtml } from '@/utils/attachmentParsingDisplay'
import { RAG_TIMELINE_TOOL_NAMES } from '@/utils/rag-pipeline-history'
import { useChatReferencesDrawer } from '@/composables/useChatReferencesDrawer'
import { buildReferenceSections } from '@/utils/referenceSources'

const props = defineProps<{
  session?: {
    id?: string | number
    agentEventStream?: Array<Record<string, unknown>>
    content?: string
    knowledge_references?: Array<{ chunk_type?: string; knowledge_id?: string; knowledge_title?: string }>
    is_completed?: boolean
  }
  embeddedMode?: boolean
}>()

const { t } = useI18n()
const referencesDrawer = useChatReferencesDrawer()
const userExpanded = ref(false)
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

const referenceSections = computed(() => buildReferenceSections(props.session?.knowledge_references))

const steps = computed(() => {
  const stream = props.session?.agentEventStream
  if (!stream?.length) return []

  return stream
    .filter((event) => {
      return (
        event.type === 'tool_call' &&
        typeof event.tool_name === 'string' &&
        RAG_TIMELINE_TOOL_NAMES.has(event.tool_name)
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
      const isAttachmentTool = toolName === 'attachment_parsing' || toolName === 'image_analysis'
      const searchSource = isSearchTool
        ? getRetrievalSearchSource(event.arguments, toolData)
        : undefined
      let summaryHtml = ''
      if (!pending && isSearchTool && toolData) {
        summaryHtml = getKnowledgeSearchSummaryHtml(t, toolData)
      } else if (!pending && isAttachmentTool) {
        summaryHtml = getAttachmentParsingSummaryHtml(t, event)
      }
      const canOpenReferences = !pending && isSearchTool && hasReferences.value

      return {
        id: String(event.tool_call_id || `${toolName}-${event.timestamp || 0}`),
        pending,
        iconName: getAgentToolIconName(toolName, searchSource),
        title: getRagPipelineStepTitle(t, {
          tool_name: toolName,
          pending,
          success: event.success as boolean | undefined,
          arguments: event.arguments,
          tool_data: toolData,
        }),
        summaryHtml,
        canOpenReferences,
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

const collapsedStatusText = computed(() => {
  if (steps.value.length === 0) {
    return hasThinking.value ? t('agentStream.toolStatus.thinkingDone') : ''
  }
  return t('agentStream.ragPipeline.searchDone')
})

const referenceSummaryText = computed(() => {
  const docCount = referenceSections.value.find((section) => section.id === 'documents')?.items.length ?? 0
  const webCount = referenceSections.value.find((section) => section.id === 'web')?.items.length ?? 0

  if (docCount > 0 && webCount > 0) {
    return t('chat.referencesDocAndWebCount', { docCount, webCount })
  }
  if (docCount > 0) {
    return t('chat.referencesDocCount', { count: docCount })
  }
  if (webCount > 0) {
    return t('chat.referencesWebCount', { count: webCount })
  }

  return ''
})

function toggleReferencesDrawer() {
  const refs = props.session?.knowledge_references
  if (!referencesDrawer || !refs?.length) return
  referencesDrawer.toggle({
    references: refs,
    highlight: null,
    messageId: props.session?.id ? String(props.session.id) : '',
    sourceKey: `rag:${props.session?.id || refs.map((item) => item.knowledge_id || item.knowledge_title).join('|')}`,
  })
}

function handleStepClick(step: { canOpenReferences?: boolean }) {
  if (!step.canOpenReferences) return
  toggleReferencesDrawer()
}

function toggleExpanded() {
  userExpanded.value = !userExpanded.value
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
  margin: 0 0 8px;
  position: relative;
}

.tree-root {
  margin-bottom: 0;

  .tree-root-toolbar {
    display: flex;
    align-items: center;
    justify-content: flex-start;
    width: 100%;
    min-width: 0;
  }

  .tree-root-expand {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    margin: 0;
    padding: 0;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--td-text-color-secondary);
    font-size: 14px;
    line-height: 22px;
    cursor: pointer;
    flex: 0 1 auto;
    min-width: 0;
    max-width: 100%;

    &:hover {
      background: transparent;
      color: var(--td-text-color-primary);
    }
  }

  .tree-root-status,
  .tree-root-reference {
    flex: 0 1 auto;
    min-width: 0;
    white-space: nowrap;
  }

  .tree-root-reference {
    display: inline-flex;
    align-items: center;
    gap: 6px;

    &::before {
      content: '';
      width: 3px;
      height: 3px;
      border-radius: 50%;
      background: currentColor;
      opacity: 0.65;
      flex-shrink: 0;
    }
  }

  .tree-root-expand__icon {
    flex-shrink: 0;
    font-size: 14px;
    color: currentColor;
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

.tool-event {
  .action-card {
    position: relative;
    background: transparent;
    border: 0;
    box-shadow: none;

    &.has-reference-trigger {
      cursor: pointer;

      &:hover {
        .action-name,
        .results-summary-text {
          color: var(--td-text-color-primary);
        }
      }
    }
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

@media (max-width: 640px) {
  .tree-root {
    .tree-root-toolbar {
      gap: 8px;
    }

    .tree-root-expand {
      max-width: 100%;
    }
  }
}
</style>
