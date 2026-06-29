<template>
  <div ref="rootElement" class="agent-stream-display" :class="{ 'is-embedded': embeddedMode, 'is-rag-mode': ragMode }">

    <!-- Collapsed intermediate steps (tree root) -->
    <div v-if="shouldShowCollapsedSteps" class="tree-container">
      <div class="tool-event">
        <div class="action-card tree-root" @click="toggleIntermediateSteps">
          <div class="action-header">
            <div class="action-title">
              <span class="action-title-icon icon-mask" :style="maskIconStyle(agentIcon)" aria-hidden="true" />
              <span class="action-name tree-root-summary" v-html="intermediateStepsSummaryHtml"></span>
              <div class="action-show-icon">
                <t-icon :name="showIntermediateSteps ? 'chevron-down' : 'chevron-right'" />
              </div>
            </div>
          </div>
        </div>
      </div>
      <!-- Tree children (intermediate steps) -->
      <div v-if="showIntermediateSteps" class="tree-children">
        <template v-for="(event, index) in visibleIntermediateEvents" :key="getEventKey(event, index)">
          <div v-if="event && event.type" class="tree-child"
            :class="{ 'tree-child-last': !isConversationDone && index === visibleIntermediateEvents.length - 1 }">
            <div class="tree-branch"></div>
            <div class="tree-child-content">
              <!-- Plan Task Change Event -->
              <div v-if="event.type === 'plan_task_change'" class="plan-task-change-event">
                <div class="plan-task-change-card">
                  <div class="plan-task-change-content">
                    <strong>{{ $t('agent.taskLabel') }}</strong> {{ event.task }}
                  </div>
                </div>
              </div>

              <!-- Thinking Event (streaming / merged). When a round's retracted
                   preamble was folded in, it becomes the card title and the
                   reasoning is the expandable body. -->
              <div v-if="event.type === 'thinking'" class="tool-event">
                <div class="action-card" :class="{ 'action-pending': isThinkingActive(event.event_id) }">
                  <div class="action-header" @click="toggleEvent(event.event_id)">
                    <div class="action-title">
                      <span class="action-title-icon icon-mask" :style="maskIconStyle(thinkingIcon)"
                        aria-hidden="true" />
                      <span v-if="event.title" class="action-name action-preamble-title">{{ event.title }}</span>
                      <span v-else-if="isEventExpanded(event.event_id)" class="action-name">{{ $t('agent.think')
                      }}</span>
                      <span v-else-if="getThinkingSummary(event)" class="action-summary">{{ getThinkingSummary(event)
                        }}</span>
                    </div>
                  </div>
                  <div v-if="event.content && isEventExpanded(event.event_id)" class="action-details">
                    <div class="thinking-detail-content markdown-content">
                      <div v-html="renderMarkdownContent(event.content)"></div>
                    </div>
                  </div>
                </div>
              </div>

              <!-- Thinking Tool Call -->
              <div v-else-if="event.type === 'tool_call' && event.tool_name === 'thinking'" class="tool-event">
                <div class="action-card"
                  :class="{ 'action-pending': event.pending || isThinkingActive(event.tool_call_id) }">
                  <div class="action-header" @click="toggleEvent(event.tool_call_id)">
                    <div class="action-title">
                      <span class="action-title-icon icon-mask" :style="maskIconStyle(thinkingIcon)"
                        aria-hidden="true" />
                      <span class="action-name">{{ $t('agent.think') }}</span>
                      <span v-if="event.tool_data?.thought_number" class="action-badge">{{
                        event.tool_data.thought_number }}/{{ event.tool_data.total_thoughts }}</span>
                      <span v-if="getThinkingSummary(event) && !isEventExpanded(event.tool_call_id)"
                        class="action-summary">{{ getThinkingSummary(event) }}</span>
                    </div>
                  </div>
                  <div v-if="event.tool_data?.thought && isEventExpanded(event.tool_call_id)" class="action-details">
                    <div class="thinking-detail-content markdown-content">
                      <div v-html="renderMarkdownContent(event.tool_data.thought)"></div>
                    </div>
                  </div>
                </div>
              </div>

              <!-- MCP tool human approval (issue #1173) -->
              <div v-else-if="event.type === 'tool_approval_required'" class="tool-event">
                <ToolApprovalCard :pending-id="event.pending_id" :service-name="event.service_name || ''"
                  :mcp-tool-name="event.mcp_tool_name || ''" :description="event.description"
                  :args-json="event.args_json" :timeout-seconds="event.timeout_seconds"
                  :requested-at="event.requested_at" :resolved="event.resolved" :approved="event.approved"
                  :resolve-reason="event.resolve_reason" />
              </div>

              <!-- MCP OAuth in-conversation authorization prompt -->
              <div v-else-if="event.type === 'mcp_oauth_required'" class="tool-event">
                <McpOAuthCard :pending-id="event.pending_id" :service-id="event.service_id || ''"
                  :service-name="event.service_name || ''" :mcp-tool-name="event.mcp_tool_name || ''"
                  :timeout-seconds="event.timeout_seconds" :requested-at="event.requested_at"
                  :resolved="event.resolved" :authorized="event.authorized"
                  :resolve-reason="event.resolve_reason" :timed-out="event.timed_out" :canceled="event.canceled" />
              </div>

              <!-- Tool Call Event (non-thinking) -->
              <div v-else-if="event.type === 'tool_call'" class="tool-event">
                <div class="action-card" :class="{
                  'action-pending': event.pending,
                  'action-error': event.success === false
                }">
                  <div class="action-header" @click="handleActionHeaderClick(event)"
                    :class="{ 'no-results': !hasResults(event) }">
                    <div class="action-title">
                      <t-icon v-if="event.tool_name" class="action-title-icon"
                        :name="getToolIconName(event.tool_name)" />
                      <t-tooltip v-if="event.tool_name === 'todo_write' && event.tool_data?.steps"
                        :content="t('agent.updatePlan')" placement="top">
                        <span class="action-name">{{ $t('agent.updatePlan') }}</span>
                      </t-tooltip>
                      <t-tooltip v-else :content="getToolTitle(event)" placement="top">
                        <span class="action-name">{{ getToolTitle(event) }}</span>
                      </t-tooltip>
                    </div>
                  </div>

                  <div v-if="!event.pending && event.tool_name === 'todo_write' && event.tool_data?.steps"
                    class="plan-status-summary-fixed">
                    <div class="plan-status-text">
                      <template v-for="(part, partIndex) in getPlanStatusItems(event)" :key="partIndex">
                        <t-icon :name="part.icon" :class="['status-icon', part.class]" />
                        <span>{{ part.label }} {{ part.count }}</span>
                        <span v-if="partIndex < getPlanStatusItems(event).length - 1" class="separator">·</span>
                      </template>
                    </div>
                  </div>

                  <div
                    v-if="!event.pending && (event.tool_name === 'search_knowledge' || event.tool_name === 'knowledge_search') && event.tool_data"
                    class="search-results-summary-fixed">
                    <div class="results-summary-text" v-html="getSearchResultsSummary(event)"></div>
                  </div>

                  <div v-if="!event.pending && event.tool_name === 'web_search' && event.tool_data"
                    class="search-results-summary-fixed">
                    <div class="results-summary-text"
                      v-html="t('agent.webSearchFound', { count: getResultsCount(event.tool_data) })">
                    </div>
                  </div>

                  <div v-if="!event.pending && event.tool_name === 'grep_chunks' && event.tool_data"
                    class="search-results-summary-fixed grep-summary">
                    <div class="results-summary-text" v-html="getGrepResultsSummary(event.tool_data)"></div>
                  </div>

                  <div v-if="!event.pending && event.tool_name === 'list_knowledge_chunks' && event.tool_data"
                    class="search-results-summary-fixed knowledge-chunks-summary">
                    <div class="results-summary-text" v-html="getKnowledgeChunksSummary(event.tool_data)"></div>
                  </div>

                  <div v-if="isEventExpanded(event.tool_call_id) && !event.pending && hasResults(event)"
                    class="action-details">
                    <div v-if="event.display_type && event.tool_data" class="tool-result-wrapper">
                      <ToolResultRenderer :display-type="event.display_type" :tool-data="event.tool_data"
                        :output="event.output" :arguments="event.arguments" />
                    </div>
                    <div v-else-if="event.output" class="tool-output-wrapper">
                      <div class="fallback-header">
                        <span class="fallback-label">{{ $t('chat.rawOutputLabel') }}</span>
                      </div>
                      <div class="detail-output-wrapper">
                        <div class="detail-output">{{ event.output }}</div>
                      </div>
                    </div>
                    <!-- Raw arguments hidden for user-friendly display -->
                  </div>
                </div>
              </div>
            </div>
          </div>
        </template>
        <div v-if="isConversationDone" class="tree-child tree-child-last agent-step-done">
          <div class="tree-branch"></div>
          <div class="tree-child-content">
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

    <!-- Event Stream (non-tree mode: before answer starts, or answer events) -->
    <div v-if="!ragMode || displayEvents.length > 0 || showAgentActivityIndicator" ref="streamingStepsContainer"
      class="streaming-steps-container" :class="{
        'streaming-steps-constrained': !answerEverStarted && !isConversationDone,
        'is-streaming-timeline': showStreamingTimeline
      }">
      <template v-for="(event, index) in displayEvents" :key="getEventKey(event, index)">
        <div v-if="event && event.type" class="event-item" :class="{
          'event-answer': event.type === 'answer',
          'tree-child': isStreamingTimelineEvent(event),
          'tree-child-last': isStreamingTimelineEvent(event) && !showAgentActivityIndicator && index === lastStreamingTimelineEventIndex
        }">
          <div v-if="isStreamingTimelineEvent(event)" class="tree-branch"></div>
          <div :class="{ 'tree-child-content': isStreamingTimelineEvent(event) }">

            <!-- Plan Task Change Event -->
            <div v-if="event.type === 'plan_task_change'" class="plan-task-change-event">
              <div class="plan-task-change-card">
                <div class="plan-task-change-content">
                  <strong>{{ $t('agent.taskLabel') }}</strong> {{ event.task }}
                </div>
              </div>
            </div>

            <!-- Thinking Event (streaming / merged). A folded preamble (retracted
             from the answer area) is shown as the card title; the reasoning is
             the expandable body. -->
            <div v-if="event.type === 'thinking'" class="tool-event">
              <div class="action-card" :class="{ 'action-pending': isThinkingActive(event.event_id) }">
                <div class="action-header" @click="toggleEvent(event.event_id)">
                  <div class="action-title">
                    <span class="action-title-icon icon-mask" :style="maskIconStyle(thinkingIcon)" aria-hidden="true" />
                    <span v-if="event.title" class="action-name action-preamble-title">{{ event.title }}</span>
                    <span v-else class="action-name">{{ $t('agent.think') }}</span>
                    <span v-if="!event.title && getThinkingSummary(event) && !isEventExpanded(event.event_id)"
                      class="action-summary">{{ getThinkingSummary(event) }}</span>
                  </div>
                </div>
                <div v-if="event.content && isEventExpanded(event.event_id)" class="action-details">
                  <div class="thinking-detail-content markdown-content">
                    <div v-html="renderMarkdownContent(event.content)"></div>
                  </div>
                </div>
              </div>
            </div>

            <!-- MCP tool human approval -->
            <div v-else-if="event.type === 'tool_approval_required'" class="tool-event">
              <ToolApprovalCard :pending-id="event.pending_id" :service-name="event.service_name || ''"
                :mcp-tool-name="event.mcp_tool_name || ''" :description="event.description" :args-json="event.args_json"
                :timeout-seconds="event.timeout_seconds" :requested-at="event.requested_at" :resolved="event.resolved"
                :approved="event.approved" :resolve-reason="event.resolve_reason" />
            </div>

            <!-- MCP OAuth in-conversation authorization prompt -->
            <div v-else-if="event.type === 'mcp_oauth_required'" class="tool-event">
              <McpOAuthCard :pending-id="event.pending_id" :service-id="event.service_id || ''"
                :service-name="event.service_name || ''" :mcp-tool-name="event.mcp_tool_name || ''"
                :timeout-seconds="event.timeout_seconds" :requested-at="event.requested_at" :resolved="event.resolved"
                :authorized="event.authorized" :resolve-reason="event.resolve_reason" :timed-out="event.timed_out"
                :canceled="event.canceled" />
            </div>

            <!-- Thinking Tool Call -->
            <div v-else-if="event.type === 'tool_call' && event.tool_name === 'thinking'" class="tool-event">
              <div class="action-card"
                :class="{ 'action-pending': event.pending || isThinkingActive(event.tool_call_id) }">
                <div class="action-header" @click="toggleEvent(event.tool_call_id)">
                  <div class="action-title">
                    <span class="action-title-icon icon-mask" :style="maskIconStyle(thinkingIcon)" aria-hidden="true" />
                    <span class="action-name">{{ $t('agent.think') }}</span>
                    <span v-if="event.tool_data?.thought_number" class="action-badge">{{ event.tool_data.thought_number
                    }}/{{ event.tool_data.total_thoughts }}</span>
                    <span v-if="getThinkingSummary(event) && !isEventExpanded(event.tool_call_id)"
                      class="action-summary">{{ getThinkingSummary(event) }}</span>
                  </div>
                </div>
                <div v-if="event.tool_data?.thought && isEventExpanded(event.tool_call_id)" class="action-details">
                  <div class="thinking-detail-content markdown-content">
                    <div v-html="renderMarkdownContent(event.tool_data.thought)"></div>
                  </div>
                </div>
              </div>
            </div>

            <!-- Answer Event -->
            <div v-else-if="event.type === 'answer' && (event.done || (event.content && event.content.trim()))"
              class="answer-event">
              <div v-if="event.content && event.content.trim()" class="answer-content markdown-content">
                <div v-stable-html="renderAnswerContent(event === activeAnswerEventRef ? typedAnswer : event.content)">
                </div>
              </div>
              <div v-if="event.done && event.content && event.content.trim() && !embeddedMode" class="answer-toolbar">
                <t-button size="small" variant="outline" shape="round" @click.stop="handleCopyAnswer(event)"
                  :title="$t('agent.copy')">
                  <t-icon name="copy" />
                </t-button>
                <t-button size="small" variant="outline" shape="round" @click.stop="handleAddToKnowledge(event)"
                  :title="$t('agent.addToKnowledgeBase')">
                  <t-icon name="bookmark-add" />
                </t-button>
                <t-tooltip v-if="event.is_fallback" :content="$t('chat.fallbackHint')" placement="top">
                  <t-button size="small" variant="outline" shape="round" class="fallback-icon-btn">
                    <t-icon name="info-circle" />
                  </t-button>
                </t-tooltip>
                <ChatRequestInfoButton v-if="showRequestInfo && isConversationDone" :session="session"
                  :session-id="sessionId" />
              </div>
            </div>

            <!-- Tool Call Event (non-thinking) -->
            <div v-else-if="event.type === 'tool_call'" class="tool-event">
              <div class="action-card" :class="{
                'action-pending': event.pending,
                'action-error': event.success === false
              }">
                <div class="action-header" @click="handleActionHeaderClick(event)"
                  :class="{ 'no-results': !hasResults(event) }">
                  <div class="action-title">
                    <t-icon v-if="event.tool_name" class="action-title-icon" :name="getToolIconName(event.tool_name)" />
                    <t-tooltip v-if="event.tool_name === 'todo_write' && event.tool_data?.steps"
                      :content="t('agent.updatePlan')" placement="top">
                      <span class="action-name">
                        {{ $t('agent.updatePlan') }}
                      </span>
                    </t-tooltip>
                    <t-tooltip v-else :content="getToolTitle(event)" placement="top">
                      <span class="action-name">{{ getToolTitle(event) }}</span>
                    </t-tooltip>
                  </div>
                </div>

                <div v-if="!event.pending && event.tool_name === 'todo_write' && event.tool_data?.steps"
                  class="plan-status-summary-fixed">
                  <div class="plan-status-text">
                    <template v-for="(part, partIndex) in getPlanStatusItems(event)" :key="partIndex">
                      <t-icon :name="part.icon" :class="['status-icon', part.class]" />
                      <span>{{ part.label }} {{ part.count }}</span>
                      <span v-if="partIndex < getPlanStatusItems(event).length - 1" class="separator">·</span>
                    </template>
                  </div>
                </div>

                <div
                  v-if="!event.pending && (event.tool_name === 'search_knowledge' || event.tool_name === 'knowledge_search') && event.tool_data"
                  class="search-results-summary-fixed">
                  <div class="results-summary-text" v-html="getSearchResultsSummary(event)"></div>
                </div>

                <div v-if="!event.pending && event.tool_name === 'web_search' && event.tool_data"
                  class="search-results-summary-fixed">
                  <div class="results-summary-text"
                    v-html="t('agent.webSearchFound', { count: getResultsCount(event.tool_data) })">
                  </div>
                </div>

                <div v-if="!event.pending && event.tool_name === 'grep_chunks' && event.tool_data"
                  class="search-results-summary-fixed grep-summary">
                  <div class="results-summary-text" v-html="getGrepResultsSummary(event.tool_data)"></div>
                </div>

                <div v-if="!event.pending && event.tool_name === 'list_knowledge_chunks' && event.tool_data"
                  class="search-results-summary-fixed knowledge-chunks-summary">
                  <div class="results-summary-text" v-html="getKnowledgeChunksSummary(event.tool_data)"></div>
                </div>

                <div v-if="isEventExpanded(event.tool_call_id) && !event.pending && hasResults(event)"
                  class="action-details">
                  <div v-if="event.display_type && event.tool_data" class="tool-result-wrapper">
                    <ToolResultRenderer :display-type="event.display_type" :tool-data="event.tool_data"
                      :output="event.output" :arguments="event.arguments" />
                  </div>

                  <div v-else-if="event.output" class="tool-output-wrapper">
                    <div class="fallback-header">
                      <span class="fallback-label">{{ $t('chat.rawOutputLabel') }}</span>
                    </div>
                    <div class="detail-output-wrapper">
                      <div class="detail-output">{{ event.output }}</div>
                    </div>
                  </div>

                  <!-- Raw arguments hidden for user-friendly display -->
                </div>
              </div>
            </div>
          </div>
        </div>
      </template>
      <div v-if="showRequestInfo && isConversationDone && !hasDoneAnswerContent" class="answer-toolbar">
        <ChatRequestInfoButton :session="session" :session-id="sessionId" />
      </div>
      <!-- Loading Indicator (inside container so it scrolls into view) -->
      <div v-if="showAgentActivityIndicator" class="tree-child tree-child-last streaming-loading-node">
        <div class="tree-branch"></div>
        <div class="tree-child-content">
          <div class="loading-indicator">
            <div class="loading-typing">
              <span></span>
              <span></span>
              <span></span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
  <!-- 引用 hover 浮层（与历史消息共用同一组件） -->
  <ChatCitationFloat :float="citationFloat" :on-enter="cancelCitationClose" :on-leave="scheduleCitationClose" />

  <!-- Image Preview -->
  <picturePreview :reviewImg="imagePreviewVisible" :reviewUrl="imagePreviewUrl" @closePreImg="closeImagePreview" />

  <!-- Wiki Page Detail Drawer -->
  <t-drawer v-model:visible="wikiDrawerVisible" :header="wikiDrawerPage?.title || ''" size="480px" :footer="false"
    placement="right" attach="body" :show-overlay="true" :close-btn="true" :close-on-overlay-click="true"
    class="wiki-graph-drawer">
    <template v-if="wikiDrawerPage">
      <div class="wiki-reader-meta"
        style="margin-bottom: 16px; display: flex; justify-content: space-between; align-items: center;">
        <div style="display: flex; align-items: center; gap: 12px;">
          <t-tag size="small" :theme="getTypeTheme(wikiDrawerPage.page_type)" variant="light-outline">
            {{ getTypeLabel(wikiDrawerPage.page_type) }}
          </t-tag>
          <span class="wiki-reader-meta-text">{{ $t('knowledgeEditor.wikiBrowser.version', {
            ver: wikiDrawerPage.version
              || 1
          }) }}</span>
        </div>
        <t-link theme="primary" hover="color" @click="navigateToWikiGraph">
          <template #prefixIcon><t-icon name="chart-bubble" /></template>
          {{ $t('knowledgeEditor.wikiBrowser.viewInGraph') }}
        </t-link>
      </div>
      <div ref="wikiDrawerBodyRef" class="wiki-reader-body" v-html="wikiDrawerContent" @click="handleWikiDrawerClick">
      </div>
    </template>
  </t-drawer>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onBeforeUnmount, onUpdated, nextTick } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { marked } from 'marked';
import 'katex/dist/katex.min.css';
import ToolResultRenderer from './ToolResultRenderer.vue';
import ToolApprovalCard from './ToolApprovalCard.vue';
import McpOAuthCard from './McpOAuthCard.vue';
import ChatRequestInfoButton from '@/components/ChatRequestInfoButton.vue';
import ChatCitationFloat from '@/components/ChatCitationFloat.vue';
import picturePreview from '@/components/picture-preview.vue';
import { countGrepDocuments } from '@/utils/grepResultsGroup';
import { getKnowledgeChunksSummaryHtml } from '@/utils/knowledgeChunksDisplay';
import { useChatCitationPopover } from '@/composables/useChatCitationPopover';
import { getWikiPage, type WikiPage } from '@/api/wiki';
import { MessagePlugin } from 'tdesign-vue-next';
import { useUIStore } from '@/stores/ui';
import { useSettingsStore } from '@/stores/settings';
import { useAuthStore } from '@/stores/auth';
import { useI18n } from 'vue-i18n';
import i18n from '@/i18n';
import { hydrateProtectedFileImages, clearProtectedFileFailureCache, sanitizeMarkdownHTML } from '@/utils/security';
import { unwrapFinalAnswerWrappers, thinkingEqualsAnswer } from '@/utils/finalAnswer';
import { getAgentToolIconName } from '@/utils/agent-tool-icons';
import { getQueryText, getWikiPageText } from '@/utils/agent-tool-display';
import {
  buildManualMarkdown,
  copyTextToClipboard,
  formatManualTitle,
  replaceIncompleteMermaidWithPlaceholder,
  prepareStreamingMermaidMarkdown,
  extractFirstMermaidCode,
  injectCachedMermaidSvg,
} from '@/utils/chatMessageShared';
import {
  configureMarkedForChatMarkdown,
  renderChatMarkdown,
  wrapChatMarkdownTables,
} from '@/utils/chatMarkdownRenderer';
import {
  createMermaidCodeRenderer,
  ensureMermaidInitialized,
  enhanceMarkdownContainer,
  renderMermaidToSvg,
} from '@/utils/mermaidShared';
import { attachMarkdownEnhancementListeners, refreshMarkdownEnhancements } from '@/utils/markdownEnhancements';
import { useTypewriter } from '@/composables/useTypewriter';
import { vStableHtml } from '@/directives/stableHtml';

const getToolIconName = getAgentToolIconName;

const router = useRouter();
const route = useRoute();
const uiStore = useUIStore();
const settingsStore = useSettingsStore();
const authStore = useAuthStore();
const { t } = useI18n();

ensureMermaidInitialized();

const TOOL_NAME_KEYS: Record<string, string> = {
  search_knowledge: 'agentStream.tools.searchKnowledge',
  knowledge_search: 'agentStream.tools.searchKnowledge',
  grep_chunks: 'agentStream.tools.grepChunks',
  web_search: 'agentStream.tools.webSearch',
  web_fetch: 'agentStream.tools.webFetch',
  get_document_info: 'agentStream.tools.getDocumentInfo',
  list_knowledge_chunks: 'agentStream.tools.listKnowledgeChunks',
  get_related_documents: 'agentStream.tools.getRelatedDocuments',
  get_document_content: 'agentStream.tools.getDocumentContent',
  wiki_search: 'agentEditor.tools.wikiSearch',
  wiki_read_page: 'agentEditor.tools.wikiReadPage',
  wiki_read_source_doc: 'agentStream.tools.wikiReadSourceDoc',
  todo_write: 'agentStream.tools.todoWrite',
  knowledge_graph_extract: 'agentStream.tools.knowledgeGraphExtract',
  thinking: 'agentStream.tools.thinking',
  image_analysis: 'agentStream.tools.imageAnalysis',
  query_understand: 'agentStream.tools.queryUnderstand',
  query_knowledge_graph: 'agentStream.tools.queryKnowledgeGraph',
  read_skill: 'agentStream.tools.readSkill',
  execute_skill_script: 'agentStream.tools.executeSkillScript',
  data_analysis: 'agentStream.tools.dataAnalysis',
  data_schema: 'agentStream.tools.dataSchema',
  database_query: 'agentStream.tools.databaseQuery',
};

const getLocalizedToolName = (toolName?: string | null): string => {
  if (!toolName) return t('agent.toolFallback');
  const key = TOOL_NAME_KEYS[toolName];
  if (key) return t(key);

  // Format MCP tool names: "mcp_my_server_search_docs" → "My Server: search docs"
  if (toolName.startsWith('mcp_')) {
    return formatMCPToolName(toolName);
  }

  return toolName;
};

/**
 * Format MCP tool name for friendly display.
 * Input:  "mcp_{service_name}_{tool_name}" (all lowercase, underscores)
 * Output: "Service Name: tool name"
 */
const formatMCPToolName = (rawName: string): string => {
  // Strip "mcp_" prefix
  const rest = rawName.slice(4);

  // Try to find the tool's original name from the event's tool_data or description.
  // Since we only have the sanitized composite name, split heuristically:
  // The service name comes first, tool name second, separated by "_".
  // We look for common MCP tool name patterns at the end.
  const parts = rest.split('_');
  if (parts.length <= 1) return rest;

  // Heuristic: tool names from MCP servers are typically 1-3 words like
  // "search", "get_weather", "list_bugs". We try to find a reasonable split.
  // For now, treat everything as a readable phrase.
  const humanized = parts.map(p => p.charAt(0).toUpperCase() + p.slice(1)).join(' ');
  return humanized;
};

const UUID_RE = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi;
const ID_LABEL_RE = /\b(knowledge_base_id|knowledge_id|chunk_id|knowledge_base_ids)\s*[:=]\s*/gi;

const sanitizeForDisplay = (text: string): string => {
  if (!text) return text;
  let result = text;
  for (const [name, i18nKey] of Object.entries(TOOL_NAME_KEYS)) {
    result = result.replaceAll(name, i18n.global.t(i18nKey));
  }
  // Format any remaining mcp_ tool names inline
  result = result.replace(/\bmcp_([a-z0-9_]+)/g, (_match, rest) => {
    const parts = rest.split('_');
    return parts.map((p: string) => p.charAt(0).toUpperCase() + p.slice(1)).join(' ');
  });
  result = result.replace(ID_LABEL_RE, '');
  result = result.replace(UUID_RE, '');
  // Remove empty inline code like `` or ` ` while preserving triple-backtick
  // fenced code blocks (```). Without the lookaround the greedy pair match
  // would eat two of the three fence backticks and break code block rendering.
  result = result.replace(/(?<!`)`[ \t]*`(?!`)/g, '');
  result = result.replace(/\(\s*\)/g, '');
  return result;
};

// 根元素引用
const rootElement = ref<HTMLElement | null>(null);

function escapeHtml(value: string): string {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

const streamingStepsContainer = ref<HTMLElement | null>(null);

// 图片预览状态
const imagePreviewVisible = ref(false);
const imagePreviewUrl = ref('');

const openImagePreview = (url: string) => {
  imagePreviewUrl.value = url;
  imagePreviewVisible.value = true;
};

const closeImagePreview = () => {
  imagePreviewVisible.value = false;
};

// Wiki Drawer 状态
const wikiDrawerVisible = ref(false);
const wikiDrawerPage = ref<WikiPage | null>(null);
const wikiDrawerBodyRef = ref<HTMLElement | null>(null);
const currentWikiKbId = ref<string>('');

function getTypeTheme(type: string): string {
  const map: Record<string, string> = {
    summary: 'primary', entity: 'success', concept: 'warning',
    synthesis: 'primary', comparison: 'danger', index: 'default', log: 'default',
  };
  return map[type] || 'default';
}

function getTypeLabel(type: string): string {
  const map: Record<string, string> = {
    summary: t('knowledgeEditor.wikiBrowser.filterSummary'),
    entity: t('knowledgeEditor.wikiBrowser.filterEntity'),
    concept: t('knowledgeEditor.wikiBrowser.filterConcept'),
    synthesis: t('knowledgeEditor.wikiBrowser.filterSynthesis'),
    comparison: t('knowledgeEditor.wikiBrowser.filterComparison'),
    index: 'Index',
    log: 'Log',
  };
  return map[type] || type;
}

const wikiDrawerContent = computed(() => {
  if (!wikiDrawerPage.value) return '';
  const content = wikiDrawerPage.value.content || '';

  // Pre-process wiki links [[slug|name]] to custom HTML tags for the drawer
  let preprocessed = content.replace(/\[\[([^\]]+)\]\]/g, (_, inner: string) => {
    const pipeIdx = inner.indexOf('|');
    const slug = pipeIdx > 0 ? inner.substring(0, pipeIdx).trim() : inner.trim();
    let display = slug;
    if (pipeIdx > 0) {
      display = inner.substring(pipeIdx + 1).trim();
    } else {
      const parts = slug.split('/');
      display = parts.length > 1 ? parts.slice(1).join('/') : slug;
    }
    return `<a href="#" class="wiki-content-link citation-wiki" data-slug="${escapeHtml(slug)}">${escapeHtml(display)}</a>`;
  });

  return wrapChatMarkdownTables(marked.parse(preprocessed, { breaks: true, async: false }) as string);
});

watch(wikiDrawerContent, async () => {
  await nextTick();
  if (wikiDrawerBodyRef.value) {
    await hydrateProtectedFileImages(wikiDrawerBodyRef.value);
  }
});

const openWikiDrawer = async (kbId: string, slug: string) => {
  if (!kbId || !slug) return;
  try {
    currentWikiKbId.value = kbId;
    const res = await getWikiPage(kbId, slug);
    wikiDrawerPage.value = (res as any).data || res as any;
    wikiDrawerVisible.value = true;
  } catch (e) {
    console.error(`Failed to load page ${slug}:`, e);
    MessagePlugin.warning(t('agentStream.citation.loadFailed'));
  }
};

const navigateToWikiGraph = () => {
  if (currentWikiKbId.value && wikiDrawerPage.value?.slug) {
    wikiDrawerVisible.value = false;
    try {
      router.push(`/platform/knowledge-bases/${currentWikiKbId.value}?tab=graph&slug=${encodeURIComponent(wikiDrawerPage.value.slug)}`);
    } catch (error) {
      console.error('Failed to navigate to wiki graph:', error);
    }
  }
};

const handleWikiDrawerClick = (e: MouseEvent) => {
  const target = e.target as HTMLElement;
  if (target.closest('.citation-wiki')) {
    e.preventDefault();
    e.stopPropagation();
    const slug = target.closest('.citation-wiki')?.getAttribute('data-slug');
    if (slug) openWikiDrawer(currentWikiKbId.value, slug);
  } else if (target.tagName.toLowerCase() === 'img') {
    e.preventDefault();
    const src = target.getAttribute('src');
    if (src) openImagePreview(src);
  } else {
    // allow link navigation inside drawer
    const aEl = target.closest?.('a') as HTMLAnchorElement | null;
    // @ts-ignore
    if (aEl && aEl.href && window.runtime && window.runtime.BrowserOpenURL) {
      if (aEl.href.startsWith('http://') || aEl.href.startsWith('https://')) {
        e.preventDefault();
        // @ts-ignore
        window.runtime.BrowserOpenURL(aEl.href);
      }
    }
  }
};

// Import icons
import agentIcon from '@/assets/img/agent.svg';
import thinkingIcon from '@/assets/img/Frame3718.svg';

interface SessionData {
  id?: string;
  request_id?: string;
  debugRequest?: Record<string, unknown>;
  isAgentMode?: boolean;
  agentEventStream?: any[];
  knowledge_references?: any[];
}

const props = defineProps<{
  session: SessionData;
  sessionId?: string;
  userQuery?: string;
  embeddedMode?: boolean;
  embedChannelId?: string;
  embedToken?: string;
  ragMode?: boolean;
}>();

const showRequestInfo = computed(
  () => !props.embeddedMode && !!(props.session?.request_id || props.session?.id),
);

const {
  float: citationFloat,
  rebind: rebindCitations,
  cancelClose: cancelCitationClose,
  scheduleClose: scheduleCitationClose,
} = useChatCitationPopover(rootElement, {
  getKnowledgeReferences: () => props.session?.knowledge_references,
  embedChannelId: () => (props.embeddedMode ? props.embedChannelId : undefined),
  embedToken: () => (props.embeddedMode ? props.embedToken : undefined),
});

configureMarkedForChatMarkdown();

// Event stream
const eventStream = computed(() => props.session?.agentEventStream || []);

// Expanded events tracking (for tool calls and thinking events)
const expandedEvents = ref<Set<string>>(new Set());

// Track IDs of thinking events that are currently "active" (latest, not yet followed by non-thinking)
const activeThinkingIds = ref<Set<string>>(new Set());
// Reactive version number to force template re-evaluation when activeThinkingIds changes
const activeThinkingVersion = ref(0);

const isThinkingActive = (eventId: string): boolean => {
  // Reference version to create reactive dependency
  void activeThinkingVersion.value;
  return activeThinkingIds.value.has(eventId);
};

// Watch event stream to auto-expand thinking events and auto-collapse when non-thinking follows
watch(eventStream, (stream) => {
  if (!stream || !Array.isArray(stream)) return;

  // Scan stream to find thinking events to expand and collapse
  const newActiveIds = new Set<string>();

  // Walk backwards to find the trailing thinking block
  let inTrailingThinking = true;
  for (let i = stream.length - 1; i >= 0; i--) {
    const event = stream[i];
    if (!event) continue;

    const isThinking = event.type === 'thinking' ||
      (event.type === 'tool_call' && event.tool_name === 'thinking');
    const id = event.type === 'thinking' ? event.event_id : event.tool_call_id;

    if (inTrailingThinking && isThinking && id) {
      newActiveIds.add(id);
      // Auto-expand if not yet known
      expandedEvents.value.add(id);
    } else if (!isThinking) {
      inTrailingThinking = false;
    }
  }

  // Collapse thinking events that were active before but are no longer trailing
  for (const oldId of activeThinkingIds.value) {
    if (!newActiveIds.has(oldId)) {
      expandedEvents.value.delete(oldId);
    }
  }

  activeThinkingIds.value = newActiveIds;
  activeThinkingVersion.value++;

  nextTick(async () => {
    await hydrateProtectedFileImages(rootElement.value);
    await enhanceMarkdownContainer(rootElement.value);
    // Auto-scroll thinking detail content to bottom during streaming
    if (newActiveIds.size > 0 && rootElement.value) {
      const els = rootElement.value.querySelectorAll('.thinking-detail-content');
      els.forEach((el: Element) => {
        const htmlEl = el as HTMLElement;
        if (htmlEl.scrollHeight > htmlEl.clientHeight) {
          htmlEl.scrollTop = htmlEl.scrollHeight;
        }
      });
    }
    // Auto-scroll the steps container to the bottom while it is still height-
    // capped (steps-only phase). Once answer text appears the cap is released
    // and the container grows with the page, so internal scrolling is moot.
    if (!answerEverStarted.value && streamingStepsContainer.value) {
      const el = streamingStepsContainer.value;
      if (el.scrollHeight > el.clientHeight) {
        el.scrollTop = el.scrollHeight;
      }
    }
  });
}, { immediate: true, deep: true });

// State for intermediate steps collapse
const showIntermediateSteps = ref(false);

// Track whether a non-superseded answer is streaming. Plain content streams
// optimistically as an `answer` event (rendered answer-style in the answer
// area). If the round turns out to be a tool round, that event is marked
// `superseded` and retracted into the steps — so a superseded segment must NOT
// count as "answer started", otherwise the answer-only view would stick after
// the preamble was retracted.
const hasAnswerStarted = computed(() => {
  const stream = eventStream.value;
  if (!stream || !Array.isArray(stream)) return false;
  return stream.some((e: any) => e.type === 'answer' && !e.superseded && e.content && e.content.trim());
});

// Whether ANY answer text has ever appeared this turn — including a preamble
// that was later superseded (its content stays in the stream). Used to release
// the live container's height cap. Unlike hasAnswerStarted this is monotonic:
// it does not flip back when a preamble is retracted, so the container does not
// shrink back to the capped height (which would look like a jump). Once the
// model starts producing answer-style text, give it full height to breathe.
const answerEverStarted = computed(() => {
  const stream = eventStream.value;
  if (!stream || !Array.isArray(stream)) return false;
  return stream.some((e: any) => e.type === 'answer' && e.content && e.content.trim());
});

const agentDurationMs = ref<number>(0);
watch(eventStream, (stream) => {
  if (!stream || !Array.isArray(stream)) return;

  // Check for agent_complete event with authoritative duration from backend
  if (agentDurationMs.value === 0) {
    const completeEvent = stream.find((e: any) => e.type === 'agent_complete' && e.total_duration_ms);
    if (completeEvent) {
      agentDurationMs.value = completeEvent.total_duration_ms;
    }
  }
}, { deep: true, immediate: true });


// Check if conversation is done (based on answer event with done=true or stop event)
const isConversationDone = computed(() => {
  const stream = eventStream.value;
  if (!stream || stream.length === 0) {
    console.log('[Collapse] No stream or empty stream');
    return false;
  }

  // Check for stop event (user cancelled)
  const stopEvent = stream.find((e: any) => e.type === 'stop');
  if (stopEvent) {
    console.log('[Collapse] Found stop event, conversation done');
    return true;
  }

  const completeEvent = stream.find((e: any) => e.type === 'agent_complete');
  if (completeEvent) {
    console.log('[Collapse] Found complete event, conversation done');
    return true;
  }

  // Check for answer event with done=true. Exclude superseded preambles: a
  // retracted tool-round preamble is also closed with done=true, but the agent
  // keeps running, so it must not mark the whole conversation as finished.
  const answerEvents = stream.filter((e: any) => e.type === 'answer' && !e.superseded);
  const doneAnswer = answerEvents.find((e: any) => e.done === true);

  return !!doneAnswer;
});

const streamingMermaidSvgCache = ref<string | null>(null);
let streamingMermaidRenderTask: Promise<void> | null = null;
let streamingMermaidRenderId = 0;

const activeAnswerMarkdown = computed(() => {
  const stream = eventStream.value;
  if (!stream?.length) return '';
  const answers = stream.filter((e: any) => e.type === 'answer' && !e.superseded);
  const active = answers.find((e: any) => !e.done) ?? answers[answers.length - 1];
  return typeof active?.content === 'string' ? active.content : '';
});

// The answer event whose text is currently streaming. The template renders the
// smoothed typewriter text for this event and the raw content for any others.
const activeAnswerEventRef = computed(() => {
  const stream = eventStream.value;
  if (!stream?.length) return null;
  const answers = stream.filter((e: any) => e.type === 'answer' && !e.superseded);
  return answers.find((e: any) => !e.done) ?? answers[answers.length - 1] ?? null;
});

// Smooth the streamed answer into a steady typewriter cadence (shared with the
// non-Agent markdown path). History reloads arrive already complete and snap to
// full instead of replaying.
const { displayed: typedAnswer } = useTypewriter(
  () => activeAnswerMarkdown.value,
  () => isConversationDone.value,
);

const cacheStreamingMermaidSvg = async () => {
  if (streamingMermaidSvgCache.value) return;
  const code = extractFirstMermaidCode(activeAnswerMarkdown.value);
  if (!code) return;

  if (!streamingMermaidRenderTask) {
    streamingMermaidRenderTask = (async () => {
      const svg = await renderMermaidToSvg(code, `mermaid-agent-stream-${++streamingMermaidRenderId}`);
      if (svg) streamingMermaidSvgCache.value = svg;
    })().finally(() => {
      streamingMermaidRenderTask = null;
    });
  }

  await streamingMermaidRenderTask;
};

watch(isConversationDone, (done) => {
  if (!done) {
    streamingMermaidSvgCache.value = null;
    streamingMermaidRenderTask = null;
  }
});

watch(streamingMermaidSvgCache, () => {
  nextTick(() => refreshMarkdownEnhancements(rootElement.value));
});

watch(activeAnswerMarkdown, () => {
  if (isConversationDone.value || streamingMermaidSvgCache.value) return;
  void cacheStreamingMermaidSvg();
});

// When the turn finishes, clear the failed-fetch cooldown and re-hydrate once.
// Files referenced mid-stream (e.g. exported images) may only become available
// at completion; throttling stops the chunk-by-chunk 404 spam during streaming,
// and this final pass guarantees they load without waiting out the cooldown.
//
// Gate this on the typewriter having fully revealed the answer: when done flips,
// the smoothed text may still be catching up, so the <img> tag is not in the DOM
// yet. Hydrating too early would find nothing and leave a permanent placeholder
// (until a manual reload). Waiting for full reveal guarantees the image exists.
const answerFullyRendered = computed(
  () => isConversationDone.value && typedAnswer.value.length >= activeAnswerMarkdown.value.length,
);
watch(answerFullyRendered, (ready) => {
  if (!ready) return;
  // Clear before this reactive update renders, so a source that returned 404
  // mid-stream gets one real final-attempt <img> node instead of remaining
  // suppressed by the missing-source cache.
  clearProtectedFileFailureCache();
  nextTick(async () => {
    await hydrateProtectedFileImages(rootElement.value);
  });
});

// Agent: dots until the turn completes. RAG: pipeline dots before answer; answer stream dots after.
const showAgentActivityIndicator = computed(() => {
  if (isConversationDone.value) return false;
  if (props.ragMode) return hasAnswerStarted.value;
  return true;
});

const isStreamingTimelineEvent = (event: any): boolean => {
  return !isConversationDone.value && event?.type && event.type !== 'answer';
};

const showStreamingTimeline = computed(() => {
  return displayEvents.value.some((event: any) => isStreamingTimelineEvent(event)) || showAgentActivityIndicator.value;
});

const lastStreamingTimelineEventIndex = computed(() => {
  if (isConversationDone.value) return -1;
  for (let i = displayEvents.value.length - 1; i >= 0; i -= 1) {
    if (isStreamingTimelineEvent(displayEvents.value[i])) return i;
  }
  return -1;
});

// Whether a completed answer with content is rendered (its toolbar hosts the
// request-info button inline, so the standalone toolbar should not duplicate it)
const hasDoneAnswerContent = computed(() => {
  const stream = eventStream.value;
  if (!stream || stream.length === 0) return false;
  return stream.some(
    (e: any) => e.type === 'answer' && e.done && e.content && e.content.trim()
  );
});

// Find the final content to display (last thinking or answer)
const finalContent = computed(() => {
  const stream = eventStream.value;
  if (!stream || stream.length === 0) {
    return null;
  }

  if (!isConversationDone.value) {
    return null;
  }

  // Check if there's a (non-superseded) answer event with content. Superseded
  // preambles carry content too, but they were retracted into the steps and are
  // not the final answer, so they must not count here.
  const answerEvents = stream.filter((e: any) => e.type === 'answer' && !e.superseded);
  const hasAnswerContent = answerEvents.some((e: any) => e.content && e.content.trim());

  if (hasAnswerContent) {
    return { type: 'answer' };
  }

  // Do NOT fall back to re-rendering the last thinking event when the
  // intermediate-steps tree already shows it — that would duplicate the
  // thinking card below the tree. The fallback is only meaningful for
  // legacy conversations where the tree is absent. Also skip for
  // user-stopped conversations which have no final answer to fall back to.
  if (shouldShowCollapsedSteps.value) {
    return null;
  }
  const wasStopped = stream.some((e: any) => e.type === 'stop');
  if (wasStopped) {
    return null;
  }

  // Fallback: if no answer content (e.g. the model ended with only reasoning),
  // use last thinking as final content
  const thinkingEvents = stream.filter((e: any) => e.type === 'thinking' && e.content && e.content.trim());
  if (thinkingEvents.length > 0) {
    const lastThinking = thinkingEvents[thinkingEvents.length - 1];
    const doneAnswer = answerEvents.find((e: any) => e.done === true);
    return {
      type: 'thinking',
      event_id: lastThinking.event_id,
      showAnswerToolbar: !!doneAnswer
    };
  }

  return null;
});

// Count intermediate steps (after merging consecutive thinking events, matching what user sees in tree)
const intermediateStepsCount = computed(() => {
  if (!hasAnswerStarted.value && !isConversationDone.value) return 0;
  // Count only thinking and tool_call events (exclude plan_task_change, etc.)
  return intermediateEvents.value.filter(
    (e: any) => e.type === 'thinking' || e.type === 'tool_call'
  ).length;
});

// Number of reasoning rounds (thinking cards) and tool invocations. We report
// these separately instead of summing them into one opaque "step" count, which
// over-counts what the user perceives as agent loops (a single loop emits one
// thinking card plus its tool calls).
const reasoningRoundsCount = computed(() => {
  if (!hasAnswerStarted.value && !isConversationDone.value) return 0;
  return intermediateEvents.value.filter((e: any) => e.type === 'thinking').length;
});

const toolCallsCount = computed(() => {
  if (!hasAnswerStarted.value && !isConversationDone.value) return 0;
  return intermediateEvents.value.filter((e: any) => e.type === 'tool_call').length;
});

const intermediateStepsSummary = computed(() => {
  if (!eventStream.value) {
    return '';
  }

  const rounds = reasoningRoundsCount.value;
  const tools = toolCallsCount.value;
  const elapsed = agentDurationMs.value;

  const parts: string[] = [];
  if (rounds > 0) {
    parts.push(t('agent.reasoningRounds', { rounds }));
  }
  if (tools > 0) {
    parts.push(t('agent.toolCalls', { tools }));
  }
  // Fallback to a generic step count if neither bucket has anything (shouldn't
  // normally happen once the tree is shown).
  if (parts.length === 0) {
    parts.push(t('agent.stepsCompleted', { steps: intermediateStepsCount.value }));
  }

  if (elapsed > 0) {
    parts.push(t('agent.durationSuffix', { duration: formatDuration(elapsed) }));
  }

  return parts.join(t('agent.stepSummarySeparator'));
});

// HTML version of intermediate steps summary with colored numbers
const intermediateStepsSummaryHtml = computed(() => {
  return intermediateStepsSummary.value;
});

// Should show the collapsed steps indicator (tree root). Collapse ONLY once the
// conversation is done. RAG quick-answer mode never shows the tool tree —
// intermediate progress is handled by RagPipelineProgress and disappears once
// references or the answer arrive.
const shouldShowCollapsedSteps = computed(() => {
  if (props.ragMode) return false
  const hasSteps = intermediateStepsCount.value > 0;
  return hasSteps && isConversationDone.value;
});

// Check if event is a "deep thinking" type (either streaming thinking or thinking tool call)
const isThinkingLikeEvent = (event: any): boolean => {
  if (event.type === 'thinking') return true;
  if (event.type === 'tool_call' && event.tool_name === 'thinking') return true;
  return false;
};

// Extract thinking content from an event
const getThinkingContent = (event: any): string => {
  if (event.type === 'thinking') return event.content || '';
  if (event.type === 'tool_call' && event.tool_name === 'thinking') {
    return event.tool_data?.thought || event.output || '';
  }
  return '';
};

// Get a short summary snippet from thinking content for display in the header
const getThinkingSummary = (event: any): string => {
  const content = getThinkingContent(event);
  if (!content) return '';
  const cleaned = sanitizeForDisplay(content)
    .replace(/^#+\s+/gm, '')
    .replace(/\*\*/g, '')
    .replace(/\*/g, '')
    .replace(/`/g, '')
    .replace(/\n+/g, ' ')
    .trim();
  if (cleaned.length <= 50) return cleaned;
  return cleaned.slice(0, 50) + '...';
};

// Helper: build the full result list with plan_task_change injections and thinking merging
const buildFullEventList = (stream: any[]) => {
  const validStream = stream.filter((e: any) => e && typeof e === 'object' && e.type);
  let lastTask: string | null = null;
  const result: any[] = [];

  for (let i = 0; i < validStream.length; i++) {
    const event = validStream[i];
    if (event.type === 'tool_call' && event.tool_name === 'todo_write' && event.tool_data?.task) {
      const currentTask = event.tool_data.task;
      if (lastTask === null || currentTask !== lastTask) {
        result.push({
          type: 'plan_task_change',
          task: currentTask,
          event_id: `plan-task-change-${event.tool_call_id || i}`,
          timestamp: event.timestamp || Date.now()
        });
      }
      lastTask = currentTask;
    }

    // Merge consecutive thinking-like events
    if (isThinkingLikeEvent(event) && result.length > 0) {
      const prev = result[result.length - 1];
      if (isThinkingLikeEvent(prev)) {
        const prevContent = prev._mergedContent || getThinkingContent(prev);
        const curContent = getThinkingContent(event);

        // Deduplicate: when a tool_call thinking event's thought content was
        // already delivered via streaming thinking events (same text), skip it.
        if (curContent && prevContent && prevContent.includes(curContent)) {
          continue;
        }
        if (curContent && prevContent && curContent.includes(prevContent)) {
          // Current fully contains previous — replace instead of appending
          result[result.length - 1] = {
            type: 'thinking',
            event_id: prev.event_id,
            content: curContent,
            thinking: prev.thinking || event.thinking,
            timestamp: prev.timestamp,
            _mergedContent: curContent,
          };
          continue;
        }

        // Normal merge: combine non-overlapping content
        const merged = [prevContent, curContent].filter(Boolean).join('\n\n');
        result[result.length - 1] = {
          type: 'thinking',
          event_id: prev.event_id,
          content: merged,
          thinking: prev.thinking || event.thinking,
          timestamp: prev.timestamp,
          _mergedContent: merged,
        };
        continue;
      }
    }

    result.push(event);
  }

  // Relocate each retracted (superseded) answer — a tool round's optimistic
  // preamble that was pulled out of the answer area — into that round's
  // thinking card as its TITLE, with the reasoning as the body (one card per
  // round). A lone preamble (model has no separate reasoning channel) becomes a
  // title-only thinking card. Non-superseded answers stay as `answer` and are
  // rendered in the answer area, never here.
  const folded: any[] = [];
  for (const e of result) {
    if (e.type === 'answer' && e.superseded) {
      const preambleText = typeof e.content === 'string' ? e.content : '';
      const prev = folded[folded.length - 1];
      if (prev && prev.type === 'thinking' && !prev.title) {
        folded[folded.length - 1] = { ...prev, title: preambleText };
        continue;
      }
      // No reasoning channel: title-only thinking card (same chrome as merged
      // rounds). Rounds with reasoning_content merge preamble into prev.title.
      folded.push({
        type: 'thinking',
        event_id: e.event_id,
        title: preambleText,
        content: '',
        thinking: false,
        timestamp: e.timestamp,
      });
      continue;
    }
    folded.push(e);
  }

  // Drop thinking cards that are entirely empty (no title and no body). Some
  // models emit "\n\n" before a tool call (e.g. qwen3 blank lines between
  // [assistant] and tool_calls), which would otherwise show an empty "思考"
  // card. Keep cards that carry a title (a relocated preamble) even with no
  // reasoning body.
  return folded.filter((e: any) => {
    if (e.type !== 'thinking') return true;
    const content = typeof e.content === 'string' ? e.content : '';
    const title = typeof e.title === 'string' ? e.title : '';
    return content.trim().length > 0 || title.trim().length > 0;
  });
};

// IDs of thinking events that should NOT be rendered in the intermediate-
// steps tree because their content is already shown as the final answer.
// Two cases produce duplicates:
//   1. `promotedThinkingEventId` — agent loop ended via natural-stop with
//      no answer event at all; we promote the trailing thinking into a
//      virtual answer card (see displayEvents) and must hide the source
//      thinking from the tree.
//   2. Natural-stop path on the backend streams answer chunks as thought
//      events first, then re-emits the *same* content as one big answer
//      event. The merged thinking event in the tree would duplicate the
//      answer card, so detect content-equivalence and hide it.
const hiddenThinkingEventIds = computed<Set<string>>(() => {
  const hidden = new Set<string>();
  const stream = eventStream.value;
  if (!stream || !Array.isArray(stream)) return hidden;

  // Case 1: trailing thinking promoted to answer (no answer events present).
  const final = finalContent.value;
  if (final && final.type === 'thinking') {
    const hasRealAnswer = stream.some(
      (e: any) => e.type === 'answer' && !e.superseded && e.content && e.content.trim()
    );
    if (!hasRealAnswer && final.event_id) {
      hidden.add(final.event_id);
    }
  }

  // Case 2: natural-stop duplicates — answer events carry the same content
  // already streamed as thinking chunks. Compare merged thinking events
  // against the concatenated answer content and hide on match. Superseded
  // preambles are excluded: they are the retracted tool-round narration, not
  // the final answer, and are intentionally shown in the steps as titles.
  const answerContent = stream
    .filter((e: any) => e.type === 'answer' && !e.superseded && e.content)
    .map((e: any) => e.content)
    .join('');
  if (answerContent.trim()) {
    const merged = buildFullEventList(stream);
    for (const e of merged) {
      if (e.type !== 'thinking' || !e.event_id) continue;
      if (hidden.has(e.event_id)) continue;
      // Hide a step card that duplicates the final answer. Match the body, or a
      // title-only card (a relocated preamble) whose title equals the answer —
      // but keep cards that still carry a distinct reasoning body so the
      // reasoning stays visible.
      const bodyMatches = e.content && thinkingEqualsAnswer(e.content, answerContent);
      const titleOnlyMatches = e.title && !(e.content && e.content.trim()) &&
        thinkingEqualsAnswer(e.title, answerContent);
      if (bodyMatches || titleOnlyMatches) {
        hidden.add(e.event_id);
      }
    }
  }

  return hidden;
});

// Intermediate events (tree children: everything except answer)
const intermediateEvents = computed(() => {
  const stream = eventStream.value;
  if (!stream || !Array.isArray(stream)) return [];
  const result = buildFullEventList(stream);
  const hidden = hiddenThinkingEventIds.value;
  return result.filter((e: any) => {
    if (e.type === 'answer' || e.type === 'agent_complete') return false;
    if (e.type === 'thinking' && e.event_id && hidden.has(e.event_id)) return false;
    return true;
  });
});

const visibleIntermediateEvents = computed(() => {
  return intermediateEvents.value.filter((e: any) => {
    if (!e) return false;
    if (e.type === 'thinking') return false;
    if (e.type === 'tool_call' && e.tool_name === 'thinking') return false;
    return true;
  });
});

// Events to display (non-tree: before answer starts show all, after answer starts show only answer)
const displayEvents = computed(() => {
  const stream = eventStream.value;
  if (!stream || !Array.isArray(stream)) {
    return [];
  }

  const result = buildFullEventList(stream);

  // Quick-answer RAG: pipeline steps and model thinking live in RagPipelineProgress;
  // here we only render the final answer stream.
  if (props.ragMode) {
    return result.filter((e: any) => e.type === 'answer');
  }

  // While the conversation is still running, keep the same lightweight tool-log
  // surface as the completed tree. Raw thinking narration is noisy during
  // streaming; the active state is represented by the compact activity dots.
  if (!isConversationDone.value) {
    return result.filter((e: any) => {
      if (e.type === 'thinking') return false;
      if (e.type === 'tool_call' && e.tool_name === 'thinking') return false;
      return true;
    });
  }

  // Done: the steps live in the collapsed tree; show only the answer here.
  const answerEvents = result.filter((e: any) => e.type === 'answer');
  if (answerEvents.length > 0) {
    return answerEvents;
  }

  // If the intermediate-steps tree is active, all thinking/tool_call events
  // are already rendered there. Showing anything else here would duplicate
  // them. This covers both the user-stopped case and any completion path
  // that didn't produce an answer event.
  if (shouldShowCollapsedSteps.value) {
    return [];
  }

  // Fallback: if no answer events, show last thinking (legacy compatibility)
  const final = finalContent.value;
  if (!final) {
    return result;
  }

  if (final.type === 'thinking') {
    // The agent loop ended via natural-stop (the model wrote its answer as
    // free text). Synthesize a virtual
    // `answer` event from the trailing thinking content so it renders with
    // the answer card UI (expanded markdown + copy/add toolbar) rather than
    // the collapsed "思考" card. The original thinking event is still in
    // the intermediate-steps tree when applicable.
    const thinking = result.find((e: any) =>
      e.type === 'thinking' && e.event_id === final.event_id
    );
    if (!thinking || !thinking.content) return result;
    return [{
      type: 'answer',
      event_id: thinking.event_id,
      content: thinking.content,
      done: true,
      _promoted_from_thinking: true,
    }];
  }

  return result;
});

// Get unique key for event
const getEventKey = (event: any, index: number): string => {
  if (!event) return `event-${index}`;
  if (event.event_id) return `event-${event.event_id}`;
  if (event.tool_call_id) return `tool-${event.tool_call_id}`;
  if (event.type === 'tool_approval_required' && event.pending_id) {
    return `approval-${event.pending_id}`;
  }
  if (event.type === 'mcp_oauth_required' && event.pending_id) {
    return `mcp-oauth-${event.pending_id}`;
  }
  return `event-${index}-${event.type || 'unknown'}`;
};

const toggleIntermediateSteps = () => {
  showIntermediateSteps.value = !showIntermediateSteps.value;
  nextTick(async () => {
    if (rootElement.value) {
      await hydrateProtectedFileImages(rootElement.value);
    }
  });
};

const toggleEvent = (eventId: string) => {
  if (expandedEvents.value.has(eventId)) {
    expandedEvents.value.delete(eventId);
  } else {
    expandedEvents.value.add(eventId);
  }
};

const handleActionHeaderClick = (event: any) => {
  if (hasResults(event) && event.tool_call_id) {
    toggleEvent(event.tool_call_id);
  }
};

const isEventExpanded = (eventId: string): boolean => {
  return expandedEvents.value.has(eventId);
};

// Check if search/grep tools have results
const hasResults = (event: any): boolean => {
  if (!event || !event.tool_data) return true; // Default to true for other tools

  const toolName = event.tool_name;

  // For knowledge search tools
  if (toolName === 'search_knowledge' || toolName === 'knowledge_search') {
    const count = event.tool_data.results?.length || event.tool_data.count || 0;
    return count > 0;
  }

  // For web search tools
  if (toolName === 'web_search') {
    const count = event.tool_data.results?.length || event.tool_data.count || 0;
    return count > 0;
  }

  // For grep tools
  if (toolName === 'grep_chunks') {
    const totalMatches = event.tool_data.total_matches || 0;
    const resultCount = event.tool_data.result_count || 0;
    return totalMatches > 0 || resultCount > 0;
  }

  // list_knowledge_chunks: summary is inline below the header (no expandable body)
  if (toolName === 'list_knowledge_chunks') {
    return false;
  }

  // For other tools, always allow expansion
  return true;
};

// Delegated handlers for span-based citation clicks/keyboard
const handleCitationActivate = (el: HTMLElement) => {
  const url = el.getAttribute('data-url');
  if (!url) return;
  try {
    // @ts-ignore: Wails runtime check
    if (window.runtime && window.runtime.BrowserOpenURL) {
      // @ts-ignore
      window.runtime.BrowserOpenURL(url);
    } else {
      const newWindow = window.open(url, '_blank', 'noopener,noreferrer');
      if (!newWindow) {
        window.location.assign(url);
      }
    }
  } catch {
    window.location.assign(url);
  }
};

const getKbIdForWiki = (slug: string): string => {
  if (route.params.kbId) return route.params.kbId as string;

  // The backend ships `found_kbs` as a map<slug, string[]> — a single slug can
  // legitimately resolve to more than one KB when multiple wiki KBs are in
  // scope. For navigation we just pick the first one; cross-KB disambiguation
  // (if ever needed) can layer on top. We also defensively handle the legacy
  // string shape in case older tool outputs are still cached in a session.
  const pickKbId = (v: unknown): string => {
    if (!v) return '';
    if (typeof v === 'string') return v;
    if (Array.isArray(v)) {
      for (const item of v) {
        if (typeof item === 'string' && item) return item;
      }
    }
    return '';
  };

  // Try to extract from agent event stream (retrieval pipeline). Walk
  // backwards so we prefer the most recent tool call's mapping.
  if (props.session?.agentEventStream) {
    for (let i = props.session.agentEventStream.length - 1; i >= 0; i--) {
      const event = props.session.agentEventStream[i];
      const foundKbs = event?.tool_data?.found_kbs;
      if (event.type === 'tool_call' && foundKbs) {
        const hit = pickKbId(foundKbs[slug]);
        if (hit) return hit;
      }
    }
  }

  // Fallbacks
  const selectedKbs = settingsStore.getSelectedKnowledgeBases();
  if (selectedKbs && selectedKbs.length > 0) return selectedKbs[0];

  if (authStore.knowledgeBases && authStore.knowledgeBases.length > 0) {
    return authStore.knowledgeBases[0].id;
  }

  return '';
};

const onRootClick = (e: Event) => {
  const target = e.target as HTMLElement;
  if (!target) return;

  // Handle image clicks -> open preview (only for images inside markdown/answer content, not icons)
  if (target.tagName === 'IMG') {
    const imgEl = target as HTMLImageElement;
    if (imgEl.closest('.markdown-content') || imgEl.closest('.answer-content')) {
      const src = imgEl.getAttribute('src');
      if (src) {
        e.preventDefault();
        e.stopPropagation();
        openImagePreview(src);
        return;
      }
    }
  }

  // Handle web citation clicks
  const webEl = target.closest?.('.citation-web') as HTMLElement | null;
  if (webEl && webEl.getAttribute('data-url')) {
    e.preventDefault();
    handleCitationActivate(webEl);
    return;
  }

  // Handle KB citation clicks -> navigate to KB detail page
  const kbEl = target.closest?.('.citation-kb') as HTMLElement | null;
  if (kbEl && kbEl.getAttribute('data-kb-id')) {
    e.preventDefault();
    e.stopPropagation();
    const kbId = kbEl.getAttribute('data-kb-id');
    if (kbId) {
      try {
        // Navigate to knowledge base detail page
        router.push(`/platform/knowledge-bases/${kbId}`);
      } catch (error) {
        console.error('Failed to navigate to knowledge base:', error);
      }
    }
    return;
  }

  // Handle wiki link clicks -> navigate to KB wiki browser page
  const wikiEl = target.closest?.('.citation-wiki') as HTMLElement | null;
  if (wikiEl && wikiEl.getAttribute('data-slug')) {
    e.preventDefault();
    e.stopPropagation();
    const slug = wikiEl.getAttribute('data-slug');

    // Determine the relevant KB ID
    const kbId = getKbIdForWiki(slug);

    if (kbId && slug) {
      openWikiDrawer(kbId, slug);
    } else {
      MessagePlugin.warning(t('agentStream.citation.noKbForWiki'));
    }
    return;
  }

  // Handle generic a clicks (especially in Wails desktop)
  const aEl = target.closest?.('a') as HTMLAnchorElement | null;
  // @ts-ignore
  if (aEl && aEl.href && window.runtime && window.runtime.BrowserOpenURL) {
    if (aEl.href.startsWith('http://') || aEl.href.startsWith('https://')) {
      e.preventDefault();
      // @ts-ignore
      window.runtime.BrowserOpenURL(aEl.href);
      return;
    }
  }
};

const onRootKeydown = (e: KeyboardEvent) => {
  const target = e.target as HTMLElement;
  if (!target) return;

  // Handle web citation keyboard
  const webEl = target.closest?.('.citation-web') as HTMLElement | null;
  if (webEl) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      handleCitationActivate(webEl);
    }
    return;
  }

  // Handle KB citation keyboard -> navigate to KB detail
  const kbEl = target.closest?.('.citation-kb') as HTMLElement | null;
  if (kbEl) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      const kbId = kbEl.getAttribute('data-kb-id');
      if (kbId) {
        try {
          router.push(`/platform/knowledge-bases/${kbId}`);
        } catch (error) {
          console.error('Failed to navigate to knowledge base:', error);
        }
      }
    }
    return;
  }

  // Handle wiki citation keyboard -> navigate to KB wiki browser
  const wikiEl = target.closest?.('.citation-wiki') as HTMLElement | null;
  if (wikiEl) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      const slug = wikiEl.getAttribute('data-slug');

      const kbId = getKbIdForWiki(slug || '');

      if (kbId && slug) {
        openWikiDrawer(kbId, slug);
      } else {
        MessagePlugin.warning(t('agentStream.citation.noKbForWiki'));
      }
    }
    return;
  }
};

onMounted(() => {
  nextTick(async () => {
    const root = rootElement.value;
    if (!root) return;
    root.addEventListener('click', onRootClick, true);
    const keydownListener: EventListener = (evt: Event) => onRootKeydown(evt as KeyboardEvent);
    (root as any).__citationKeydown__ = keydownListener;
    root.addEventListener('keydown', keydownListener, true);
    rebindCitations();
    await hydrateProtectedFileImages(rootElement.value);
  });
});

onBeforeUnmount(() => {
  const root = rootElement.value;
  if (!root) return;
  root.removeEventListener('click', onRootClick, true);
  const keydownListener: EventListener | undefined = (root as any).__citationKeydown__;
  if (keydownListener) {
    root.removeEventListener('keydown', keydownListener, true);
    delete (root as any).__citationKeydown__;
  }
});

onUpdated(() => {
  nextTick(async () => {
    rebindCitations();
    // Hydrate protected-file images (e.g. local:// exports) as soon as the
    // typewriter reveals their <img> into the DOM, so they show in real time
    // mid-stream instead of waiting for the turn to finish. Hydration is cheap
    // and idempotent: blob results are cached per URL, in-flight fetches are
    // de-duped, and failures back off for a cooldown — so a not-yet-ready file
    // simply retries later (and the answerFullyRendered pass is the backstop).
    await hydrateProtectedFileImages(rootElement.value);
  });
});

// 自定义渲染器 - 支持 Mermaid
const agentRenderer = new marked.Renderer();
agentRenderer.code = createMermaidCodeRenderer('mermaid-agent');

const prepareAgentMarkdown = (markdown: string, cachedSvgHtml?: string | null): string => {
  const mermaidSafe = !isConversationDone.value
    ? prepareStreamingMermaidMarkdown(markdown, cachedSvgHtml ?? streamingMermaidSvgCache.value)
    : replaceIncompleteMermaidWithPlaceholder(markdown);
  return mermaidSafe.replace(/<(?:kb|web)\b[^>]*$/i, '');
};

const renderAgentMarkdown = (
  content: unknown,
  escapeMarkdown: (markdown: string) => string,
): string => {
  const contentStr = typeof content === 'string' ? content : String(content || '');
  if (!contentStr.trim()) return '';

  return renderChatMarkdown(contentStr, {
    renderer: agentRenderer,
    escapeMarkdown,
    sanitizeHtml: sanitizeMarkdownHTML,
    streaming: !isConversationDone.value,
    knowledgeReferences: props.session?.knowledge_references,
    cachedMermaidSvgHtml: streamingMermaidSvgCache.value,
    prepareMarkdown: prepareAgentMarkdown,
    injectCachedMermaidSvg,
  });
};

// 单次渲染 Markdown 内容（替代 token-by-token，修复 KaTeX 公式在 streaming 时闪烁消失的问题）
const renderMarkdownContent = (content: unknown): string => {
  return renderAgentMarkdown(content, sanitizeForDisplay);
};

// Renders an answer event's content. Strips final-answer wrappers
// (e.g. <answer>…</answer>, "Final Answer:") that some models wrap their
// plain-text answer in, then delegates to the standard markdown renderer.
const renderAnswerContent = (content: unknown): string => {
  const contentStr = typeof content === 'string' ? content : String(content || '');
  return renderMarkdownContent(unwrapFinalAnswerWrappers(contentStr));
};

// Legacy Markdown rendering function (kept for summaries)
const renderMarkdown = (content: unknown): string => {
  const contentStr = typeof content === 'string' ? content : String(content || '');
  if (!contentStr.trim()) return '';

  try {
    return renderAgentMarkdown(content, (text) => text);
  } catch (e) {
    console.error('Markdown rendering error:', e, 'Content:', contentStr.substring(0, 100));
    return contentStr.replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }
};

// 渲染 Mermaid 图表的函数
const renderMermaidDiagrams = async () => {
  await enhanceMarkdownContainer(rootElement.value);
};

// Tool summary - extract key info to display externally
const getToolSummary = (event: any): string => {
  if (!event || event.pending || !event.success) return '';

  const toolName = event.tool_name;
  const toolData = event.tool_data;

  // For search tools, don't return summary here - it will be displayed in SearchResults component
  if (toolName === 'search_knowledge' || toolName === 'knowledge_search') {
    return '';
  } else if (toolName === 'get_document_info') {
    if (toolData?.title) {
      return t('agentStream.toolSummary.getDocument', { title: toolData.title });
    }
  } else if (toolName === 'list_knowledge_chunks') {
    if (toolData?.faq_question) {
      return t('agentStream.toolSummary.listFaqEntry', { question: toolData.faq_question });
    }
    if (toolData?.fetched_chunks !== undefined) {
      const title = toolData?.knowledge_title || toolData?.knowledge_id || t('agentStream.toolSummary.document');
      return t('agentStream.toolSummary.listChunks', { title, fetched: toolData.fetched_chunks, total: toolData.total_chunks ?? '?' });
    }
  } else if (toolName === 'todo_write') {
    // Extract steps from tool data
    const steps = toolData?.steps;
    if (Array.isArray(steps)) {
      const inProgress = steps.filter((s: any) => s.status === 'in_progress').length;
      const pending = steps.filter((s: any) => s.status === 'pending').length;
      const completed = steps.filter((s: any) => s.status === 'completed').length;

      const parts = [];
      if (inProgress > 0) parts.push(`🚀 ${t('agentStream.plan.inProgress')} ${inProgress}`);
      if (pending > 0) parts.push(`📋 ${t('agentStream.plan.pending')} ${pending}`);
      if (completed > 0) parts.push(`✅ ${t('agentStream.plan.completed')} ${completed}`);

      return parts.join(' · ');
    }
  } else if (toolName === 'thinking') {
    // Return truthy value to trigger rendering, actual content rendered in template
    return toolData?.thought ? t('agentStream.toolSummary.deepThinking') : '';
  }

  return '';
};

// Get plan status parts for todo_write tool header
const getPlanStatusParts = (event: any) => {
  if (!event || !event.tool_data?.steps) {
    return { inProgress: 0, pending: 0, completed: 0 };
  }

  const steps = event.tool_data.steps;
  if (!Array.isArray(steps)) {
    return { inProgress: 0, pending: 0, completed: 0 };
  }

  return {
    inProgress: steps.filter((s: any) => s.status === 'in_progress').length,
    pending: steps.filter((s: any) => s.status === 'pending').length,
    completed: steps.filter((s: any) => s.status === 'completed').length
  };
};

// Get plan status items for display with icons
const getPlanStatusItems = (event: any) => {
  const parts = getPlanStatusParts(event);
  const items: Array<{ icon: string; class: string; label: string; count: number }> = [];

  if (parts.inProgress > 0) {
    items.push({
      icon: 'play-circle-filled',
      class: 'in-progress',
      label: t('agentStream.plan.inProgress'),
      count: parts.inProgress
    });
  }

  if (parts.pending > 0) {
    items.push({
      icon: 'time',
      class: 'pending',
      label: t('agentStream.plan.pending'),
      count: parts.pending
    });
  }

  if (parts.completed > 0) {
    items.push({
      icon: 'check-circle-filled',
      class: 'completed',
      label: t('agentStream.plan.completed'),
      count: parts.completed
    });
  }

  return items;
};

// Get plan status summary for todo_write tool header (deprecated, use getPlanStatusParts instead)
const getPlanStatusSummary = (event: any): string => {
  const parts = getPlanStatusParts(event);
  const textParts = [];
  if (parts.inProgress > 0) textParts.push(`🚀 ${t('agentStream.plan.inProgress')} ${parts.inProgress}`);
  if (parts.pending > 0) textParts.push(`📋 ${t('agentStream.plan.pending')} ${parts.pending}`);
  if (parts.completed > 0) textParts.push(`✅ ${t('agentStream.plan.completed')} ${parts.completed}`);
  return textParts.length > 0 ? textParts.join(' · ') : '';
};

/** Render SVG assets in the channel / brand color via CSS mask. */
function maskIconStyle(src: string, size = 18): Record<string, string> {
  if (!src) return {}
  const url = `url("${src}")`
  return {
    width: `${size}px`,
    height: `${size}px`,
    WebkitMaskImage: url,
    maskImage: url,
  }
}

// Get search results summary text (returns HTML with colored numbers)
const getSearchResultsSummary = (event: any): string => {
  if (!event || !event.tool_data) return '';

  const toolData = event.tool_data;
  const count = Number(toolData.results?.length ?? toolData.count ?? 0) || 0;
  if (count === 0) return t('agentStream.search.noResults');

  // Build summary text
  let summary = '';
  const kbCount = toolData.kb_counts ? Object.keys(toolData.kb_counts).length : 0;
  if (kbCount > 0) {
    summary = t('agentStream.search.foundResultsFromFiles', { count: `<strong>${count}</strong>`, files: `<strong>${kbCount}</strong>` });
  } else {
    summary = t('agentStream.search.foundResults', { count: `<strong>${count}</strong>` });
  }
  return summary;
};

// Get web search results summary text
const getWebSearchResultsSummary = (toolData: any): string => {
  if (!toolData) return '';

  const count = Number(toolData.results?.length ?? toolData.count ?? 0) || 0;
  if (count === 0) return '';

  return t('agentStream.search.webResults', { count });
};

// Get results count (number only) for web search summary
const getResultsCount = (toolData: any): number => {
  if (!toolData) return 0;
  return Number(toolData.results?.length ?? toolData.count ?? 0) || 0;
};

// Get grep results summary text (returns HTML with colored numbers)
const getGrepResultsSummary = (toolData: any): string => {
  if (!toolData) return '';

  const totalChunks = Number(toolData.total_matches ?? 0) || 0;
  const docCount = countGrepDocuments(toolData);

  if (totalChunks === 0) {
    return t('agentStream.search.noResults');
  }

  return t('agentStream.search.grepSummary', {
    chunks: `<strong>${totalChunks}</strong>`,
    docs: `<strong>${docCount}</strong>`,
  });
};

const getKnowledgeChunksSummary = (toolData: any): string => {
  return getKnowledgeChunksSummaryHtml(t, toolData);
};

// Get tool title - prefer summary over description, add query for search tools
const getToolTitle = (event: any): string => {
  if (event.pending) {
    if (event.tool_name === 'image_analysis') {
      return t('agentStream.toolStatus.imageAnalyzing');
    }
    if (event.tool_name === 'wiki_search' || event.tool_name === 'wiki_read_page') {
      return `${getLocalizedToolName(event.tool_name)}...`;
    }
    const localizedName = getLocalizedToolName(event.tool_name);
    return t('agentStream.toolStatus.calling', { name: localizedName });
  }

  const toolName = event.tool_name;
  const isSearchTool = toolName === 'search_knowledge' || toolName === 'knowledge_search' || toolName === 'wiki_search';
  const isWebSearchTool = toolName === 'web_search';
  const isGrepTool = toolName === 'grep_chunks';

  // For search tools, use description with query text
  if (isSearchTool) {
    const baseTitle = getToolDescription(event);
    const queryText =
      getQueryText(event.arguments) ||
      getQueryText(event.tool_data);
    if (queryText) {
      return `${baseTitle}：「${queryText}」`;
    }
    return baseTitle;
  }

  // For web search tools, use description with query text
  if (isWebSearchTool) {
    const baseTitle = getToolDescription(event);
    // Try to get query from arguments or tool_data
    let queryText = '';
    if (event.arguments && typeof event.arguments === 'object' && event.arguments.query) {
      const query = event.arguments.query;
      // Handle both string and array formats
      if (Array.isArray(query)) {
        queryText = query.filter((q: any) => q && typeof q === 'string').join('，');
      } else if (typeof query === 'string') {
        queryText = query;
      }
    } else if (event.tool_data && event.tool_data.query) {
      const query = event.tool_data.query;
      // Handle both string and array formats
      if (Array.isArray(query)) {
        queryText = query.filter((q: any) => q && typeof q === 'string').join('，');
      } else if (typeof query === 'string') {
        queryText = query;
      }
    }
    if (queryText) {
      return `${baseTitle}：「${queryText}」`;
    }
    return baseTitle;
  }

  // For grep tools, use description with patterns
  if (isGrepTool) {
    const baseTitle = getToolDescription(event);
    // Try to get patterns from arguments or tool_data
    let patterns: string[] = [];
    if (event.arguments && typeof event.arguments === 'object') {
      if (Array.isArray(event.arguments.queries)) {
        patterns = event.arguments.queries;
      } else if (Array.isArray(event.arguments.patterns)) {
        patterns = event.arguments.patterns;
      } else if (event.arguments.query) {
        patterns = [event.arguments.query];
      } else if (event.arguments.pattern) {
        patterns = [event.arguments.pattern];
      }
    } else if (event.tool_data) {
      if (Array.isArray(event.tool_data.queries)) {
        patterns = event.tool_data.queries;
      } else if (Array.isArray(event.tool_data.patterns)) {
        patterns = event.tool_data.patterns;
      } else if (event.tool_data.query) {
        patterns = [event.tool_data.query];
      } else if (event.tool_data.pattern) {
        patterns = [event.tool_data.pattern];
      }
    }
    if (patterns.length > 0) {
      // Show up to 2 patterns in title
      const displayPatterns = patterns.slice(0, 2);
      const patternText = displayPatterns.join('、');
      const moreText = patterns.length > 2 ? ` +${patterns.length - 2}` : '';
      return `${baseTitle}：「${patternText}${moreText}」`;
    }
    return baseTitle;
  }

  if (toolName === 'wiki_read_page') {
    const pageLabel = String(
      event.tool_data?.title ||
      getWikiPageText(event.arguments) ||
      getWikiPageText(event.tool_data)
    ).trim();
    const baseTitle = getToolDescription(event);
    return pageLabel ? `${baseTitle}：「${sanitizeForDisplay(pageLabel)}」` : baseTitle;
  }

  // Use tool summary if available
  const summary = getToolSummary(event);
  return summary || getToolDescription(event);
};

// Tool description
const getToolDescription = (event: any): string => {
  if (event.pending) {
    if (event.tool_name === 'image_analysis') {
      return t('agentStream.toolStatus.imageAnalyzing');
    }
    if (event.tool_name === 'query_understand') {
      return t('agentStream.toolStatus.queryUnderstanding');
    }
    const localizedName = getLocalizedToolName(event.tool_name);
    return t('agentStream.toolStatus.calling', { name: localizedName });
  }

  const success = event.success === true;
  const toolName = event.tool_name;

  if (toolName === 'search_knowledge' || toolName === 'knowledge_search') {
    return success ? t('agentStream.toolStatus.searchKb') : t('agentStream.toolStatus.searchKbFailed');
  } else if (toolName === 'wiki_search' || toolName === 'wiki_read_page') {
    const localizedName = getLocalizedToolName(toolName);
    return success ? localizedName : t('agentStream.toolStatus.calledFailed', { name: localizedName });
  } else if (toolName === 'web_search') {
    return success ? t('agentStream.toolStatus.webSearch') : t('agentStream.toolStatus.webSearchFailed');
  } else if (toolName === 'grep_chunks') {
    return success ? t('agentStream.toolStatus.grepSearch') : t('agentStream.toolStatus.grepSearchFailed');
  } else if (toolName === 'get_document_info') {
    return success ? t('agentStream.toolStatus.getDocInfo') : t('agentStream.toolStatus.getDocInfoFailed');
  } else if (toolName === 'get_document_content' || toolName === 'wiki_read_source_doc') {
    return success ? t('agentStream.toolStatus.viewDocument') : t('agentStream.toolStatus.calledFailed', { name: t('agentStream.toolStatus.viewDocument') });
  } else if (toolName === 'thinking') {
    return success ? t('agentStream.toolStatus.thinkingDone') : t('agentStream.toolStatus.thinkingFailed');
  } else if (toolName === 'todo_write') {
    return success ? t('agentStream.toolStatus.updateTodos') : t('agentStream.toolStatus.updateTodosFailed');
  } else if (toolName === 'image_analysis') {
    return success ? t('agentStream.toolStatus.imageAnalysisDone') : t('agentStream.toolStatus.imageAnalysisFailed');
  } else if (toolName === 'query_understand') {
    return success ? t('agentStream.toolStatus.queryUnderstandDone') : t('agentStream.toolStatus.calledFailed', { name: getLocalizedToolName(toolName) });
  } else {
    const localizedName = getLocalizedToolName(toolName);
    return success ? t('agentStream.toolStatus.called', { name: localizedName }) : t('agentStream.toolStatus.calledFailed', { name: localizedName });
  }
};

// Helper functions
const formatDuration = (ms?: number): string => {
  if (!ms) return '0s';
  if (ms < 1000) return `${ms}ms`;
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = seconds % 60;
  return `${minutes}m ${remainingSeconds}s`;
};

const formatJSON = (obj: any): string => {
  try {
    if (typeof obj === 'string') {
      // Try to parse if it's a JSON string
      try {
        const parsed = JSON.parse(obj);
        return JSON.stringify(parsed, null, 2);
      } catch {
        return obj;
      }
    }
    return JSON.stringify(obj, null, 2);
  } catch {
    return String(obj);
  }
};

// Helper function to get actual content (from answer or last thinking).
// Strips final-answer wrappers (e.g. <answer>…</answer>, "Final Answer:")
// so callers like copy and add-to-knowledge get clean text.
const getActualContent = (answerEvent: any): string => {
  // First try to get content from answer event
  const answerContent = (answerEvent?.content || '').trim();
  if (answerContent) {
    return unwrapFinalAnswerWrappers(answerContent).trim();
  }

  // If answer is empty, try to get from last thinking
  const stream = eventStream.value;
  if (stream && Array.isArray(stream)) {
    const thinkingEvents = stream.filter((e: any) => e.type === 'thinking' && e.content && e.content.trim());
    if (thinkingEvents.length > 0) {
      const lastThinking = thinkingEvents[thinkingEvents.length - 1];
      return unwrapFinalAnswerWrappers((lastThinking.content || '').trim()).trim();
    }
  }

  return '';
};

const handleCopyAnswer = async (answerEvent: any) => {
  const content = getActualContent(answerEvent);
  if (!content) {
    MessagePlugin.warning(t('agentStream.copy.emptyContent'));
    return;
  }

  try {
    await copyTextToClipboard(content);
    MessagePlugin.success(t('agentStream.copy.success'));
  } catch (err) {
    console.error('Copy failed:', err);
    MessagePlugin.error(t('agentStream.copy.failed'));
  }
};

const handleAddToKnowledge = (answerEvent: any) => {
  const content = getActualContent(answerEvent);
  if (!content) {
    MessagePlugin.warning(t('agentStream.saveToKb.emptyContent'));
    return;
  }

  const question = (props.userQuery || '').trim();
  const manualContent = buildManualMarkdown(question, content);
  const manualTitle = formatManualTitle(question);

  uiStore.openManualEditor({
    mode: 'create',
    title: manualTitle,
    content: manualContent,
    status: 'draft',
  });

  MessagePlugin.info(t('agentStream.saveToKb.editorOpened'));
};
</script>

<style lang="less" scoped>
@import '../../../components/css/chat-markdown.less';
@import '../../../components/css/chat-message-shared.less';
@import '../../../components/css/chat-citations.less';
@import '../../../components/css/chat-timeline-loading.less';

.agent-stream-display {
  display: flex;
  flex-direction: column;
  gap: 0;
  margin-bottom: 10px;
  position: relative;
  --agent-step-text-size: 14px;
  --agent-step-summary-size: 13px;
  --agent-step-line-color: color-mix(in srgb, var(--td-text-color-primary) 16%, transparent);
  --agent-step-icon-color: var(--td-text-color-placeholder);
  --stream-brand-2: color-mix(in srgb, var(--td-brand-color) 2%, transparent);
  --stream-brand-3: color-mix(in srgb, var(--td-brand-color) 3%, transparent);
  --stream-brand-4: color-mix(in srgb, var(--td-brand-color) 4%, transparent);
  --stream-brand-5: color-mix(in srgb, var(--td-brand-color) 5%, transparent);
  --stream-brand-6: color-mix(in srgb, var(--td-brand-color) 6%, transparent);
  --stream-brand-8: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
  --stream-brand-10: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
  --stream-brand-12: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
  --stream-brand-15: color-mix(in srgb, var(--td-brand-color) 15%, transparent);
  --stream-brand-20: color-mix(in srgb, var(--td-brand-color) 20%, transparent);

  &.is-rag-mode {
    margin-top: 0;
  }

  &.is-embedded {
    margin-bottom: 0;

    .loading-indicator {
      height: 41px;
      padding: 0 0 0 4px;
      margin-top: 0;
      animation: none;
    }
  }
}

// Streaming steps container
.streaming-steps-container {
  position: relative;

  &.is-streaming-timeline {
    margin-top: 8px;
  }

  &.streaming-steps-constrained {
    max-height: 400px;
    overflow-y: auto;

    &::-webkit-scrollbar {
      width: 4px;
    }

    &::-webkit-scrollbar-track {
      background: transparent;
    }

    &::-webkit-scrollbar-thumb {
      background: var(--td-bg-color-component-disabled);
      border-radius: 2px;

      &:hover {
        background: var(--td-text-color-placeholder);
      }
    }
  }
}

// Event items (flat, no timeline)
.event-item {
  position: relative;
  margin-bottom: 8px;

  &.event-answer {
    // answer 事件无特殊左侧装饰
  }
}

// ============ Tree View ============
.tree-container {
  margin: 0 0 16px;
  position: relative;
}

.tree-root {
  cursor: pointer;
  color: var(--td-text-color-secondary);
  margin-bottom: 0;
}

.tree-root-summary {
  :deep(strong) {
    font-weight: 600;
    color: var(--td-text-color-primary);
  }
}

.icon-mask {
  display: inline-block;
  flex-shrink: 0;
  background-color: var(--agent-step-icon-color);
  mask-size: contain;
  mask-repeat: no-repeat;
  mask-position: center;
  -webkit-mask-size: contain;
  -webkit-mask-repeat: no-repeat;
  -webkit-mask-position: center;
}

.tree-children {
  position: relative;
  padding-left: 0;
  margin-top: 14px;
  margin-left: 10px;
  max-height: none;
  overflow-y: visible;
  border-left: 0;
}

.tree-child {
  position: relative;
  padding-left: 42px;
  padding-bottom: 0;
  margin-bottom: 18px;

  // vertical trunk line (continues for non-last children)
  // bottom: -6px extends the line through the margin-bottom gap between siblings
  &::before {
    content: '';
    position: absolute;
    left: 9px;
    top: 22px;
    bottom: -18px;
    width: 0;
    border-left: 1px solid var(--agent-step-line-color);
  }

  // horizontal branch connector
  .tree-branch {
    display: none;
  }

  // last child: vertical line only goes to the branch, then stops
  &.tree-child-last {
    margin-bottom: 0;

    &::before {
      content: none;
    }
  }
}

.tree-child-content {
  // child content area
}

// Thinking detail content (inside action-details)
.thinking-detail-content {
  padding: 7px 0 0 30px;
  font-size: var(--agent-step-summary-size);
  color: var(--td-text-color-secondary);
  line-height: 1.6;
  max-height: none;
  overflow-y: visible;

  &.markdown-content {
    // Compact thinking-panel Markdown is intentionally not the chat answer body.
    // Answer Markdown typography belongs to chat-markdown.less.
    .chat-citation-pills();
  }
}

// Answer Event - 无边框，直接显示内容
.answer-event {
  animation: fadeInUp 0.25s ease-out;
  min-height: 20px;

  .fallback-icon-btn {
    color: var(--td-text-color-disabled) !important;
    border-color: var(--td-component-stroke) !important;

    &:hover {
      color: var(--td-text-color-placeholder) !important;
      border-color: var(--td-component-border) !important;
    }
  }

  .answer-content {
    &.markdown-content {
      // Chat Markdown visual styles are centralized in chat-markdown.less.
      // Do not add element-level Markdown rules here; update the shared mixin.
      .chat-markdown-typography();
      .chat-citation-pills();

      :deep(img) {
        background-color: var(--td-bg-color-secondarycontainer);
        /* 加载时的占位背景色 */
      }
    }
  }

  .answer-toolbar {
    margin-top: 10px;
  }
}

// Tool Event
.tool-event {
  animation: fadeInUp 0.25s ease-out;

  .action-card {
    background: transparent;
    border-radius: 0;
    border: 0;
    border-left: 0;
    overflow: visible;
    position: relative;
    transition: border-color 0.2s ease;
    box-shadow: none;

    >* {
      position: relative;
      z-index: 1;
    }

    &:hover {
      background: transparent;
    }

    &.action-error {
      color: var(--td-error-color);
    }

    &.action-pending {
      opacity: 1;
      box-shadow: none;
      background: transparent;
    }
  }

  .tool-summary {
    padding: 6px 12px;
    font-size: 12px;
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container);
    border-top: 1px solid var(--td-component-stroke);
    line-height: 1.6;
    font-weight: 500;
    animation: slideIn 0.2s ease-out;

    .tool-summary-markdown {
      // Compact tool summaries have local spacing by design; full chat answer
      // Markdown typography belongs to chat-markdown.less.
      font-weight: 400;
      line-height: 1.6;
      color: var(--td-text-color-primary);

      :deep(p) {
        margin: 3px 0;
        color: var(--td-text-color-primary);
      }

      :deep(ul),
      :deep(ol) {
        margin: 3px 0;
        padding-left: 18px;
      }

      :deep(code) {
        background: var(--td-bg-color-secondarycontainer);
        padding: 2px 5px;
        border-radius: 3px;
        font-size: 11px;
        color: var(--td-brand-color);
        font-weight: 500;
      }

      :deep(strong) {
        font-weight: 600;
        color: var(--td-text-color-primary);
      }
    }
  }
}

.action-header {
  display: flex;
  align-items: center;
  padding: 0;
  color: var(--td-text-color-primary);
  font-weight: 400;
  min-height: 24px;
  cursor: pointer;
  user-select: none;
  transition: background-color 0.15s ease;

  &:hover {
    background-color: transparent;
  }

  &.no-results {
    cursor: default;

    &:hover {
      background-color: transparent;
    }
  }
}

.action-title {
  display: flex;
  align-items: center;
  gap: 12px;
  position: relative;
  flex: 1;
  min-width: 0;

  .action-title-icon {
    flex-shrink: 0;

    &.t-icon {
      width: 18px;
      height: 18px;
      color: var(--agent-step-icon-color);
    }
  }

  :deep(.t-tooltip) {
    flex: 0 1 auto;
    min-width: 0;
  }

  .action-show-icon {
    flex-shrink: 0;
    margin-left: 2px;
  }

  .action-name {
    white-space: nowrap;
    font-size: var(--agent-step-text-size);
    line-height: 1.55;
    font-weight: 400;
    color: var(--td-text-color-secondary);
  }

  // Retracted preamble used as the card title: allow it to wrap to its full
  // text (it carries meaning) and use primary text color, while the reasoning
  // body stays in the collapsible details.
  .action-preamble-title {
    white-space: normal;
    word-break: break-word;
    font-size: var(--agent-step-text-size);
    line-height: 1.55;
    color: var(--td-text-color-secondary);
  }

  .action-badge {
    display: inline-flex;
    align-items: center;
    padding: 0 6px;
    height: 18px;
    border-radius: 9px;
    background: var(--stream-brand-10);
    color: color-mix(in srgb, var(--td-brand-color) 80%, var(--td-text-color-secondary));
    font-size: 11px;
    font-weight: 500;
    white-space: nowrap;
    flex-shrink: 0;
  }

  .action-summary {
    color: var(--td-text-color-secondary);
    font-size: var(--agent-step-summary-size);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex-shrink: 1;
  }
}


@keyframes fadeInUp {
  from {
    opacity: 0;
    transform: translateY(6px);
  }

  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@keyframes slideInDown {
  from {
    opacity: 0;
    transform: translateY(-8px);
  }

  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@keyframes slideIn {
  from {
    opacity: 0;
    transform: translateX(-6px);
  }

  to {
    opacity: 1;
    transform: translateX(0);
  }
}

// Loading 动画关键帧
@keyframes dotBounce {

  0%,
  80%,
  100% {
    transform: scale(1);
    opacity: 0.6;
  }

  40% {
    transform: scale(1.3);
    opacity: 1;
  }
}

@keyframes spin {
  0% {
    transform: rotate(0deg);
  }

  100% {
    transform: rotate(360deg);
  }
}

@keyframes pulse {

  0%,
  100% {
    transform: scale(1);
    opacity: 0.8;
  }

  50% {
    transform: scale(1.5);
    opacity: 0.3;
  }
}

@keyframes typingBounce {

  0%,
  60%,
  100% {
    transform: translate3d(0, 0, 0);
  }

  30% {
    transform: translate3d(0, -5px, 0);
  }
}

@keyframes wave {

  0%,
  40%,
  100% {
    transform: scaleY(0.4);
  }

  20% {
    transform: scaleY(1);
  }
}

@keyframes pulseBorder {

  0%,
  100% {
    border-left-color: var(--td-brand-color);
    box-shadow: 0 1px 3px var(--stream-brand-6);
  }

  50% {
    border-left-color: var(--td-brand-color);
    box-shadow: 0 1px 4px var(--stream-brand-12);
  }
}

@keyframes shakeError {

  0%,
  100% {
    transform: translateX(0);
  }

  10%,
  30%,
  50%,
  70%,
  90% {
    transform: translateX(-2px);
  }

  20%,
  40%,
  60%,
  80% {
    transform: translateX(2px);
  }
}

@keyframes actionPendingShimmer {
  0% {
    transform: translateX(-90%);
  }

  50% {
    transform: translateX(-5%);
  }

  100% {
    transform: translateX(90%);
  }
}

.action-name {
  font-size: var(--agent-step-text-size);
  font-weight: 400;
  color: var(--td-text-color-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  display: inline-block;
  max-width: 100%;
  vertical-align: middle;
}

.action-show-icon {
  font-size: 12px;
  padding: 0 2px;
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
}

.action-details {
  padding: 0;
  border-top: 0;
  background: transparent;
  display: flex;
  flex-direction: column;
}

.tool-result-wrapper {
  margin: 0;
}

.search-results-summary-fixed {
  padding: 2px 0 0 0;
  background: transparent;
  border-top: 0;

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

.plan-status-summary-fixed {
  padding: 2px 0 0 0;
  background: transparent;
  border-top: 0;

  .plan-status-text {
    font-size: var(--agent-step-summary-size);
    font-weight: 400;
    color: var(--td-text-color-secondary);
    line-height: 1.5;
    display: flex;
    align-items: center;
    gap: 4px;
    flex-wrap: wrap;

    .status-icon {
      font-size: 14px;
      flex-shrink: 0;

      &.in-progress {
        color: var(--td-brand-color);
      }

      &.pending {
        color: var(--td-warning-color);
      }

      &.completed {
        color: var(--td-brand-color);
      }
    }

    .separator {
      color: var(--td-text-color-placeholder);
      margin: 0 4px;
    }

    span:not(.separator) {
      display: inline-flex;
      align-items: center;
      gap: 4px;
    }
  }
}

@keyframes rotate {
  from {
    transform: rotate(0deg);
  }

  to {
    transform: rotate(360deg);
  }
}

.plan-task-change-event {
  min-height: 24px;

  .plan-task-change-card {
    padding: 0;
    background: transparent;
    border-radius: 0;
    border: 0;
    font-size: var(--agent-step-text-size);
    color: var(--td-text-color-secondary);
    line-height: 1.55;

    .plan-task-change-content {
      strong {
        color: var(--td-text-color-secondary);
        font-weight: 400;
        margin-right: 6px;
      }
    }
  }
}

.tool-output-wrapper {
  margin: 10px 0;
  padding: 0 8px;

  .fallback-header {
    display: flex;
    align-items: center;
    margin-bottom: 8px;
    padding: 0 4px;

    .fallback-label {
      font-size: 11px;
      color: var(--td-text-color-secondary);
      font-weight: 500;
      line-height: 1.5;
    }
  }

  .detail-output-wrapper {
    position: relative;
    background: var(--td-bg-color-secondarycontainer);
    border: 1px solid var(--td-component-stroke);
    border-radius: 6px;
    overflow: hidden;
    margin: 0;
    padding: 0;

    .detail-output {
      font-family: var(--app-font-family-mono);
      font-size: 11px;
      color: var(--td-text-color-primary);
      padding: 12px;
      margin: 0;
      white-space: pre-wrap;
      word-break: break-word;
      line-height: 1.6;
      max-height: 400px;
      overflow-y: auto;
      overflow-x: auto;
      background: var(--td-bg-color-container);
      display: block;

      &::-webkit-scrollbar {
        width: 6px;
        height: 6px;
      }

      &::-webkit-scrollbar-track {
        background: var(--td-bg-color-secondarycontainer);
        border-radius: 3px;
      }

      &::-webkit-scrollbar-thumb {
        background: var(--td-bg-color-component-disabled);
        border-radius: 3px;

        &:hover {
          background: var(--td-bg-color-component-disabled);
        }
      }
    }
  }
}

.tool-arguments-wrapper {
  margin-top: 8px;
  padding: 0 10px;
  margin-bottom: 8px;

  .arguments-header {
    margin-bottom: 6px;

    .arguments-label {
      font-size: 12px;
      font-weight: 600;
      color: var(--td-text-color-secondary);
      text-transform: uppercase;
      letter-spacing: 0.5px;
    }
  }

  .detail-code {
    font-size: 12px;
    background: var(--td-bg-color-container);
    padding: 10px;
    border-radius: 6px;
    font-family: var(--app-font-family-mono);
    color: var(--td-text-color-primary);
    margin: 0;
    overflow-x: auto;
    border: 1px solid var(--td-component-stroke);
    line-height: 1.5;
  }
}

.loading-indicator {
  display: flex;
  align-items: center;
  min-height: 24px;
  padding: 0;
  margin-top: 0;
  position: relative;
  animation: fadeInUp 0.3s ease-out;

  // 方案1: 三个跳动的圆点
  .loading-dots {
    display: flex;
    align-items: center;
    gap: 6px;

    span {
      width: 8px;
      height: 8px;
      border-radius: 50%;
      background: var(--td-brand-color);
      animation: dotBounce 1.4s ease-in-out infinite;

      &:nth-child(1) {
        animation-delay: -0.32s;
      }

      &:nth-child(2) {
        animation-delay: -0.16s;
      }

      &:nth-child(3) {
        animation-delay: 0s;
      }
    }
  }

  // 打字机效果
  .loading-typing {
    display: flex;
    align-items: center;
    gap: 4px;

    span {
      width: 4px;
      height: 4px;
      border-radius: 50%;
      background: var(--td-text-color-placeholder);
      animation: typingBounce 1.4s ease-in-out infinite;
      // Composite each dot so the bounce stays smooth and ghost-free while the
      // streaming answer relayouts every token.
      will-change: transform;
      backface-visibility: hidden;

      &:nth-child(1) {
        animation-delay: 0s;
      }

      &:nth-child(2) {
        animation-delay: 0.2s;
      }

      &:nth-child(3) {
        animation-delay: 0.4s;
      }
    }
  }

  // 方案5: 波浪线
  .loading-wave {
    display: flex;
    align-items: center;
    gap: 3px;

    span {
      width: 3px;
      height: 16px;
      background: var(--td-brand-color);
      border-radius: 2px;
      animation: wave 1.2s ease-in-out infinite;

      &:nth-child(1) {
        animation-delay: 0s;
      }

      &:nth-child(2) {
        animation-delay: 0.1s;
      }

      &:nth-child(3) {
        animation-delay: 0.2s;
      }

      &:nth-child(4) {
        animation-delay: 0.3s;
      }

      &:nth-child(5) {
        animation-delay: 0.4s;
      }
    }
  }

  .botanswer_loading_gif {
    width: 24px;
    height: 18px;
    margin-left: 0;
  }
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

// Final step layout override: keep agent reasoning/tool output visually close to
// Claude's compact timeline instead of boxed cards.
.agent-stream-display {
  .tool-event {
    .action-card {
      background: transparent;
      border: 0;
      border-left: 0;
      border-radius: 0;
      box-shadow: none;
      overflow: visible;

      &:hover {
        background: transparent;
      }

      &.action-error {
        color: var(--td-error-color);
      }

      &.action-pending {
        background: transparent;
      }
    }

    .action-header {
      padding: 0;

      &:hover {
        background: transparent;
      }
    }
  }

  .action-details {
    border-top: 0;
    background: transparent;
  }

  .thinking-detail-content {
    padding: 7px 0 0 0;
    font-size: var(--agent-step-summary-size);
    color: var(--td-text-color-secondary);
    max-height: none;
    overflow-y: visible;
  }

  .search-results-summary-fixed,
  .plan-status-summary-fixed {
    padding: 2px 0 0 0;
    background: transparent;
    border-top: 0;
  }

  .search-results-summary-fixed .results-summary-text,
  .plan-status-summary-fixed .plan-status-text {
    font-size: var(--agent-step-summary-size);
    font-weight: 400;
    color: var(--td-text-color-secondary);
  }

  .search-results-summary-fixed .results-summary-text :deep(strong) {
    color: var(--td-text-color-secondary);
    font-weight: 500;
  }

  .action-title {
    gap: 12px;
    position: relative;
  }

  .tree-root .action-title {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    flex: 0 1 auto;
    min-width: 0;
  }

  .tree-root .action-title-icon {
    display: none;
  }

  .icon-mask {
    background-color: var(--agent-step-icon-color);
  }

  .action-title .action-title-icon {
    color: var(--agent-step-icon-color);
    width: 18px;
    height: 18px;
  }

  .tree-child .action-title-icon {
    position: absolute;
    left: -42px;
    top: 3px;
  }

  .action-title .action-name,
  .action-name,
  .action-preamble-title {
    font-size: var(--agent-step-text-size);
    font-weight: 400;
    line-height: 1.55;
    color: var(--td-text-color-secondary);
  }

  .tree-root .action-name {
    font-size: 14px;
    color: var(--td-text-color-secondary);
  }

  .action-summary {
    font-size: var(--agent-step-summary-size);
    color: var(--td-text-color-placeholder);
  }

  .plan-task-change-card {
    padding: 0;
    background: transparent;
    border: 0;
    border-radius: 0;
    font-size: var(--agent-step-text-size);
    color: var(--td-text-color-secondary);
  }
}
</style>

<style lang="less">
/* Global styles for teleported components */

.wiki-graph-drawer {
  box-shadow: -4px 0 16px rgba(0, 0, 0, 0.08);

  .wiki-reader-meta {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .wiki-reader-meta-text {
    font-size: 13px;
    color: var(--td-text-color-placeholder);
  }

  // Wiki drawer is a non-chat reader surface. Chat answer Markdown styles are
  // centralized in chat-markdown.less; do not copy these rules into chat message components.
  .wiki-reader-body {
    line-height: 1.6;
    font-size: 14px;
    color: var(--td-text-color-primary);

    h1 {
      font-size: 24px;
      margin: 28px 0 16px;
      font-weight: 600;
      line-height: 1.4;
    }

    h2 {
      font-size: 18px;
      margin: 24px 0 12px;
      font-weight: 600;
      line-height: 1.4;
    }

    h3 {
      font-size: 16px;
      margin: 20px 0 10px;
      font-weight: 600;
      line-height: 1.5;
    }

    h4,
    h5,
    h6 {
      font-size: 14px;
      margin: 16px 0 8px;
      font-weight: 600;
      line-height: 1.5;
    }

    p {
      margin: 0 0 14px;
    }

    ul,
    ol {
      margin: 0 0 14px;
      padding-left: 24px;
    }

    li {
      margin-bottom: 6px;
      line-height: 1.6;
    }

    li>p {
      margin-bottom: 6px;
    }

    blockquote {
      margin: 0 0 14px;
      padding: 10px 16px;
      background: var(--td-bg-color-secondarycontainer);
      border-left: 4px solid var(--td-component-border);
      border-radius: 0 4px 4px 0;
      color: var(--td-text-color-secondary);
    }

    code {
      font-family: var(--app-font-family-mono);
      font-size: 13px;
      padding: 2px 4px;
      background: var(--td-bg-color-secondarycontainer);
      border-radius: 4px;
      color: var(--td-brand-color);
    }

    pre {
      margin: 0 0 14px;
      padding: 12px 16px;
      background: var(--td-bg-color-secondarycontainer);
      border-radius: 6px;
      overflow-x: auto;

      code {
        padding: 0;
        background: transparent;
        color: inherit;
      }
    }

    p:has(img) {
      text-align: center;
      color: var(--td-text-color-secondary);
      font-size: 13px;
      margin-top: 16px;
      margin-bottom: 24px;

      img {
        max-width: 100%;
        max-height: 400px;
        object-fit: contain;
        border-radius: 6px;
        display: block;
        margin: 0 auto 8px;
        cursor: zoom-in;
        transition: opacity 0.2s;

        &:hover {
          opacity: 0.9;
        }
      }
    }

    a.wiki-content-link {
      color: var(--td-brand-color);
      text-decoration: none;
      border-bottom: 1px dashed var(--td-brand-color);
      cursor: pointer;
      font-weight: 500;

      &:hover {
        border-bottom-style: solid;
        text-decoration: none !important;
      }
    }

    .chat-markdown-table {
      width: fit-content;
      max-width: 100%;
      overflow-x: auto;
      margin: 0 0 16px;
      background: var(--td-bg-color-container);
      border: 1px solid var(--td-component-stroke);
      border-radius: 6px;
      -webkit-overflow-scrolling: touch;
    }

    table {
      display: table;
      width: max-content;
      min-width: 0;
      border-collapse: separate;
      border-spacing: 0;
      font-size: 13px;
      line-height: 1.55;
    }

    table thead {
      background: var(--td-bg-color-secondarycontainer);
    }

    table th,
    table td {
      padding: 8px 12px;
      border-bottom: 1px solid var(--td-component-stroke);
      border-right: 1px solid var(--td-component-stroke);
      text-align: left;
      vertical-align: top;
      word-break: break-word;
    }

    table th {
      font-weight: 600;
      color: var(--td-text-color-primary);
      white-space: nowrap;
    }

    table th:last-child,
    table td:last-child {
      border-right: none;
    }

    table tbody tr:last-child td {
      border-bottom: none;
    }

    table tbody tr:hover {
      background: var(--td-bg-color-secondarycontainer);
    }

    table code {
      font-size: 12px;
    }
  }
}
</style>
