<template>
  <div ref="rootElement" class="agent-stream-display">
    
    <!-- Collapsed intermediate steps (tree root) -->
    <div v-if="shouldShowCollapsedSteps" class="tree-container">
      <div class="tree-root" @click="toggleIntermediateSteps">
        <div class="tree-root-title">
          <img :src="agentIcon" alt="" />
          <span v-html="intermediateStepsSummaryHtml"></span>
        </div>
        <div class="tree-root-toggle">
          <t-icon :name="showIntermediateSteps ? 'chevron-up' : 'chevron-down'" />
        </div>
      </div>
      <!-- Tree children (intermediate steps) -->
      <div v-if="showIntermediateSteps" class="tree-children">
        <template v-for="(event, index) in intermediateEvents" :key="getEventKey(event, index)">
          <div v-if="event && event.type" class="tree-child" :class="{ 'tree-child-last': index === intermediateEvents.length - 1 }">
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

              <!-- Thinking Event (streaming / merged) -->
              <div v-if="event.type === 'thinking'" class="tool-event">
                <div class="action-card" :class="{ 'action-pending': isThinkingActive(event.event_id) }">
                  <div class="action-header" @click="toggleEvent(event.event_id)">
                    <div class="action-title">
                      <img class="action-title-icon" :src="thinkingIcon" alt="" />
                      <span v-if="isEventExpanded(event.event_id)" class="action-name">{{ $t('agent.think') }}</span>
                      <span v-if="getThinkingSummary(event) && !isEventExpanded(event.event_id)" class="action-summary">{{ getThinkingSummary(event) }}</span>
                    </div>
                    <div v-if="event.content" class="action-show-icon">
                      <t-icon :name="isEventExpanded(event.event_id) ? 'chevron-up' : 'chevron-down'" />
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
                <div class="action-card" :class="{ 'action-pending': event.pending || isThinkingActive(event.tool_call_id) }">
                  <div class="action-header" @click="toggleEvent(event.tool_call_id)">
                    <div class="action-title">
                      <img class="action-title-icon" :src="thinkingIcon" alt="" />
                      <span class="action-name">{{ $t('agent.think') }}</span>
                      <span v-if="event.tool_data?.thought_number" class="action-badge">{{ event.tool_data.thought_number }}/{{ event.tool_data.total_thoughts }}</span>
                      <span v-if="getThinkingSummary(event) && !isEventExpanded(event.tool_call_id)" class="action-summary">{{ getThinkingSummary(event) }}</span>
                    </div>
                    <div v-if="event.tool_data?.thought" class="action-show-icon">
                      <t-icon :name="isEventExpanded(event.tool_call_id) ? 'chevron-up' : 'chevron-down'" />
                    </div>
                  </div>
                  <div v-if="event.tool_data?.thought && isEventExpanded(event.tool_call_id)" class="action-details">
                    <div class="thinking-detail-content markdown-content">
                      <div v-html="renderMarkdownContent(event.tool_data.thought)"></div>
                    </div>
                  </div>
                </div>
              </div>

              <!-- Tool Call Event (non-thinking) -->
              <div v-else-if="event.type === 'tool_call'" class="tool-event">
                <div
                  class="action-card"
                  :class="{
                    'action-pending': event.pending,
                    'action-error': event.success === false
                  }"
                >
                  <div class="action-header" @click="handleActionHeaderClick(event)" :class="{ 'no-results': !hasResults(event) }">
                    <div class="action-title">
                      <img v-if="event.tool_name && !isBookIcon(event.tool_name)" class="action-title-icon" :src="getToolIcon(event.tool_name)" alt="" />
                      <t-icon v-if="event.tool_name && isBookIcon(event.tool_name)" class="action-title-icon" name="book" />
                      <t-tooltip v-if="event.tool_name === 'todo_write' && event.tool_data?.steps" :content="t('agent.updatePlan')" placement="top">
                        <span class="action-name">{{ $t('agent.updatePlan') }}</span>
                      </t-tooltip>
                      <t-tooltip v-else :content="getToolTitle(event)" placement="top">
                        <span class="action-name">{{ getToolTitle(event) }}</span>
                      </t-tooltip>
                    </div>
                    <div v-if="!event.pending && hasResults(event)" class="action-show-icon">
                      <t-icon :name="isEventExpanded(event.tool_call_id) ? 'chevron-up' : 'chevron-down'" />
                    </div>
                  </div>

                  <div v-if="!event.pending && event.tool_name === 'todo_write' && event.tool_data?.steps" class="plan-status-summary-fixed">
                    <div class="plan-status-text">
                      <template v-for="(part, partIndex) in getPlanStatusItems(event)" :key="partIndex">
                        <t-icon :name="part.icon" :class="['status-icon', part.class]" />
                        <span>{{ part.label }} {{ part.count }}</span>
                        <span v-if="partIndex < getPlanStatusItems(event).length - 1" class="separator">·</span>
                      </template>
                    </div>
                  </div>

                  <div v-if="!event.pending && (event.tool_name === 'search_knowledge' || event.tool_name === 'knowledge_search') && event.tool_data" class="search-results-summary-fixed">
                    <div class="results-summary-text" v-html="getSearchResultsSummary(event)"></div>
                  </div>

                  <div v-if="!event.pending && event.tool_name === 'web_search' && event.tool_data" class="search-results-summary-fixed">
                    <div class="results-summary-text" v-html="t('agent.webSearchFound', { count: getResultsCount(event.tool_data) })"></div>
                  </div>

                  <div v-if="!event.pending && event.tool_name === 'grep_chunks' && event.tool_data" class="search-results-summary-fixed grep-summary">
                    <div class="results-summary-text" v-html="getGrepResultsSummary(event.tool_data)"></div>
                  </div>

                  <div v-if="isEventExpanded(event.tool_call_id) && !event.pending && hasResults(event)" class="action-details">
                      <div v-if="event.display_type && event.tool_data" class="tool-result-wrapper">
                        <ToolResultRenderer
                          :display-type="event.display_type"
                          :tool-data="event.tool_data"
                          :output="event.output"
                          :arguments="event.arguments"
                        />
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
      </div>
    </div>

    <!-- Event Stream (non-tree mode: before answer starts, or answer events) -->
    <div ref="streamingStepsContainer" class="streaming-steps-container" :class="{ 'streaming-steps-constrained': !hasAnswerStarted && !isConversationDone }">
    <template v-for="(event, index) in displayEvents" :key="getEventKey(event, index)">
      <div v-if="event && event.type" class="event-item" :class="{ 'event-answer': event.type === 'answer' }">

        <!-- Plan Task Change Event -->
        <div v-if="event.type === 'plan_task_change'" class="plan-task-change-event">
          <div class="plan-task-change-card">
            <div class="plan-task-change-content">
              <strong>{{ $t('agent.taskLabel') }}</strong> {{ event.task }}
            </div>
          </div>
        </div>

        <!-- Thinking Event (streaming / merged) -->
        <div v-if="event.type === 'thinking'" class="tool-event">
          <div class="action-card" :class="{ 'action-pending': isThinkingActive(event.event_id) }">
            <div class="action-header" @click="toggleEvent(event.event_id)">
              <div class="action-title">
                <img class="action-title-icon" :src="thinkingIcon" alt="" />
                <span class="action-name">{{ $t('agent.think') }}</span>
                <span v-if="getThinkingSummary(event) && !isEventExpanded(event.event_id)" class="action-summary">{{ getThinkingSummary(event) }}</span>
              </div>
              <div v-if="event.content" class="action-show-icon">
                <t-icon :name="isEventExpanded(event.event_id) ? 'chevron-up' : 'chevron-down'" />
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
          <div class="action-card" :class="{ 'action-pending': event.pending || isThinkingActive(event.tool_call_id) }">
            <div class="action-header" @click="toggleEvent(event.tool_call_id)">
              <div class="action-title">
                <img class="action-title-icon" :src="thinkingIcon" alt="" />
                <span class="action-name">{{ $t('agent.think') }}</span>
                <span v-if="event.tool_data?.thought_number" class="action-badge">{{ event.tool_data.thought_number }}/{{ event.tool_data.total_thoughts }}</span>
                <span v-if="getThinkingSummary(event) && !isEventExpanded(event.tool_call_id)" class="action-summary">{{ getThinkingSummary(event) }}</span>
              </div>
              <div v-if="event.tool_data?.thought" class="action-show-icon">
                <t-icon :name="isEventExpanded(event.tool_call_id) ? 'chevron-up' : 'chevron-down'" />
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
        <div v-else-if="event.type === 'answer' && (event.done || (event.content && event.content.trim()))" class="answer-event">
          <div
            v-if="event.content && event.content.trim()"
            class="answer-content markdown-content"
          >
               <div v-html="renderAnswerContent(event.content)"></div>
          </div>
          <div v-if="event.done && event.content && event.content.trim()" class="answer-toolbar">
            <t-button size="small" variant="outline" shape="round" @click.stop="handleCopyAnswer(event)" :title="$t('agent.copy')">
              <t-icon name="copy" />
            </t-button>
            <t-button size="small" variant="outline" shape="round" @click.stop="handleAddToKnowledge(event)" :title="$t('agent.addToKnowledgeBase')">
              <t-icon name="add" />
            </t-button>
            <t-tooltip v-if="event.is_fallback" :content="$t('chat.fallbackHint')" placement="top">
              <t-button size="small" variant="outline" shape="round" class="fallback-icon-btn">
                <t-icon name="info-circle" />
              </t-button>
            </t-tooltip>
          </div>
        </div>

        <!-- Tool Call Event (non-thinking) -->
        <div v-else-if="event.type === 'tool_call'" class="tool-event">
        <div
          class="action-card"
          :class="{
            'action-pending': event.pending,
            'action-error': event.success === false
          }"
        >
          <div class="action-header" @click="handleActionHeaderClick(event)" :class="{ 'no-results': !hasResults(event) }">
            <div class="action-title">
              <img v-if="event.tool_name && !isBookIcon(event.tool_name)" class="action-title-icon" :src="getToolIcon(event.tool_name)" alt="" />
              <t-icon v-if="event.tool_name && isBookIcon(event.tool_name)" class="action-title-icon" name="book" />
              <t-tooltip v-if="event.tool_name === 'todo_write' && event.tool_data?.steps" :content="t('agent.updatePlan')" placement="top">
                <span class="action-name">
                  {{ $t('agent.updatePlan') }}
                </span>
              </t-tooltip>
              <t-tooltip v-else :content="getToolTitle(event)" placement="top">
                <span class="action-name">{{ getToolTitle(event) }}</span>
              </t-tooltip>
            </div>
            <div v-if="!event.pending && hasResults(event)" class="action-show-icon">
              <t-icon :name="isEventExpanded(event.tool_call_id) ? 'chevron-up' : 'chevron-down'" />
            </div>
          </div>

          <div v-if="!event.pending && event.tool_name === 'todo_write' && event.tool_data?.steps" class="plan-status-summary-fixed">
            <div class="plan-status-text">
              <template v-for="(part, partIndex) in getPlanStatusItems(event)" :key="partIndex">
                <t-icon :name="part.icon" :class="['status-icon', part.class]" />
                <span>{{ part.label }} {{ part.count }}</span>
                <span v-if="partIndex < getPlanStatusItems(event).length - 1" class="separator">·</span>
              </template>
            </div>
          </div>

          <div v-if="!event.pending && (event.tool_name === 'search_knowledge' || event.tool_name === 'knowledge_search') && event.tool_data" class="search-results-summary-fixed">
            <div class="results-summary-text" v-html="getSearchResultsSummary(event)"></div>
          </div>

          <div v-if="!event.pending && event.tool_name === 'web_search' && event.tool_data" class="search-results-summary-fixed">
            <div class="results-summary-text" v-html="t('agent.webSearchFound', { count: getResultsCount(event.tool_data) })"></div>
          </div>

          <div v-if="!event.pending && event.tool_name === 'grep_chunks' && event.tool_data" class="search-results-summary-fixed grep-summary">
            <div class="results-summary-text" v-html="getGrepResultsSummary(event.tool_data)"></div>
          </div>

          <div v-if="isEventExpanded(event.tool_call_id) && !event.pending && hasResults(event)" class="action-details">
              <div v-if="event.display_type && event.tool_data" class="tool-result-wrapper">
                <ToolResultRenderer
                  :display-type="event.display_type"
                  :tool-data="event.tool_data"
                  :output="event.output"
                  :arguments="event.arguments"
                />
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
    </template>
    <!-- Loading Indicator (inside container so it scrolls into view) -->
    <div v-if="!isConversationDone && eventStream.length > 0" class="loading-indicator">
      <div class="loading-typing">
        <span></span>
        <span></span>
        <span></span>
      </div>
    </div>
    </div>
  </div>
  <!-- 全局浮层：统一承载 Web/KB 的 hover 内容 -->
  <Teleport to="body">
    <div
      v-if="floatPopup.visible"
      class="kb-float-popup"
      :style="{ top: floatPopup.top + 'px', left: floatPopup.left + 'px', width: floatPopup.width + 'px' }"
      @mouseenter="cancelFloatClose()"
      @mouseleave="scheduleFloatClose()"
    >
      <div class="t-popup__content">
        <template v-if="floatPopup.type === 'web'">
          <div class="tip-title">{{ floatPopup.title || '' }}</div>
          <div class="tip-url">{{ floatPopup.url || '' }}</div>
        </template>
        <template v-else>
          <div v-if="floatPopup.knowledgeTitle" class="tip-meta"><strong>{{ floatPopup.knowledgeTitle }}</strong></div>
          <div v-if="floatPopup.loading" class="tip-loading">{{ $t('common.loading') }}</div>
          <div v-else-if="floatPopup.error" class="tip-error">{{ floatPopup.error }}</div>
          <div v-else class="tip-content" v-html="floatPopup.content"></div>
          <div v-if="floatPopup.chunkId" class="tip-meta">{{ $t('chat.chunkIdLabel') }} {{ floatPopup.chunkId }}</div>
        </template>
      </div>
    </div>
  </Teleport>
  
  <!-- Image Preview -->
  <picturePreview :reviewImg="imagePreviewVisible" :reviewUrl="imagePreviewUrl" @closePreImg="closeImagePreview" />
  
  <!-- Wiki Page Detail Drawer -->
  <t-drawer
    v-model:visible="wikiDrawerVisible"
    :header="wikiDrawerPage?.title || ''"
    size="480px"
    :footer="false"
    placement="right"
    attach="body"
    :show-overlay="true"
    :close-btn="true"
    :close-on-overlay-click="true"
    class="wiki-graph-drawer"
  >
    <template v-if="wikiDrawerPage">
      <div class="wiki-reader-meta" style="margin-bottom: 16px; display: flex; justify-content: space-between; align-items: center;">
        <div style="display: flex; align-items: center; gap: 12px;">
          <t-tag size="small" :theme="getTypeTheme(wikiDrawerPage.page_type)" variant="light-outline">
            {{ getTypeLabel(wikiDrawerPage.page_type) }}
          </t-tag>
          <span class="wiki-reader-meta-text">{{ $t('knowledgeEditor.wikiBrowser.version', { ver: wikiDrawerPage.version || 1 }) }}</span>
        </div>
        <t-link theme="primary" hover="color" @click="navigateToWikiGraph">
          <template #prefixIcon><t-icon name="chart-bubble" /></template>
          {{ $t('knowledgeEditor.wikiBrowser.viewInGraph') }}
        </t-link>
      </div>
      <div ref="wikiDrawerBodyRef" class="wiki-reader-body" v-html="wikiDrawerContent" @click="handleWikiDrawerClick"></div>
    </template>
  </t-drawer>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { marked } from 'marked';
import markedKatex from 'marked-katex-extension';
import 'katex/dist/katex.min.css';
import DOMPurify from 'dompurify';
import ToolResultRenderer from './ToolResultRenderer.vue';
import picturePreview from '@/components/picture-preview.vue';
import { getChunkByIdOnly } from '@/api/knowledge-base';
import { getWikiPage, type WikiPage } from '@/api/wiki';
import { MessagePlugin } from 'tdesign-vue-next';
import { useUIStore } from '@/stores/ui';
import { useSettingsStore } from '@/stores/settings';
import { useAuthStore } from '@/stores/auth';
import { useI18n } from 'vue-i18n';
import i18n from '@/i18n';
import { hydrateProtectedFileImages } from '@/utils/security';
import { unwrapFinalAnswerWrappers } from '@/utils/finalAnswer';
import {
  buildManualMarkdown,
  copyTextToClipboard,
  formatManualTitle,
  replaceIncompleteImageWithPlaceholder,
} from '@/utils/chatMessageShared';
import {
  createMermaidCodeRenderer,
  ensureMermaidInitialized,
  renderMermaidInContainer,
} from '@/utils/mermaidShared';

const router = useRouter();
const route = useRoute();
const uiStore = useUIStore();
const settingsStore = useSettingsStore();
const authStore = useAuthStore();
const { t } = useI18n();

ensureMermaidInitialized();

// DOMPurify 配置 - 支持 Mermaid SVG 标签
const DOMPurifyConfig = {
  ALLOWED_TAGS: [
    'p', 'br', 'strong', 'em', 'u', 'code', 'pre', 'ul', 'ol', 'li', 'blockquote',
    'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'a', 'span', 'table', 'thead', 'tbody',
    'tr', 'th', 'td', 'img', 'figure', 'figcaption', 'div',
    // Mermaid SVG 支持的标签
    'svg', 'g', 'path', 'rect', 'circle', 'ellipse', 'line', 'polygon',
    'polyline', 'text', 'tspan', 'defs', 'marker', 'filter', 'use',
    'clippath', 'lineargradient', 'radialgradient', 'stop', 'pattern',
    'image', 'foreignobject', 'desc', 'title', 'switch', 'symbol', 'mask',
    // KaTeX MathML 支持的标签
    'math', 'annotation', 'semantics', 'mo', 'mi', 'mn', 'msup', 'mrow', 'mfrac', 'msqrt', 'mroot', 'mstyle'
  ],
  ALLOWED_ATTR: [
    'href', 'title', 'target', 'rel', 'data-tooltip', 'data-url', 'data-kb-id',
    'data-chunk-id', 'data-doc', 'data-slug', 'class', 'role', 'tabindex', 'src', 'alt', 'data-protected-src',
    'width', 'height', 'style', 'id',
    // Mermaid SVG 支持的属性
    'd', 'fill', 'stroke', 'stroke-width', 'stroke-linecap', 'stroke-linejoin',
    'stroke-dasharray', 'stroke-dashoffset', 'stroke-miterlimit', 'stroke-opacity',
    'fill-opacity', 'opacity', 'transform', 'viewbox', 'preserveaspectratio',
    'x', 'y', 'x1', 'y1', 'x2', 'y2', 'cx', 'cy', 'rx', 'ry', 'r',
    'dx', 'dy', 'text-anchor', 'dominant-baseline', 'font-family', 'font-size',
    'font-weight', 'font-style', 'letter-spacing', 'word-spacing',
    'marker-start', 'marker-mid', 'marker-end', 'markerunits', 'markerwidth',
    'markerheight', 'refx', 'refy', 'orient', 'points', 'offset',
    'gradientunits', 'gradienttransform', 'spreadmethod', 'stop-color', 'stop-opacity',
    'patternunits', 'patterntransform', 'clippathunits', 'maskunits',
    'filterunits', 'primitiveunits', 'xmlns', 'xmlns:xlink', 'xlink:href',
    'version', 'baseprofile', 'enable-background', 'overflow', 'visibility',
    'display', 'pointer-events', 'cursor', 'data-emit', 'direction',
    // KaTeX MathML 支持的属性
    'mathvariant', 'encoding', 'aria-hidden'
  ],
  USE_PROFILES: { html: true, svg: true, mathMl: true },
  // Allow provider:// URLs so they can be hydrated later.
  ALLOWED_URI_REGEXP: /^(?:(?:(?:f|ht)tps?|mailto|tel|callto|cid|xmpp):|(?:local|minio|cos|tos):|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))/i
};

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
  todo_write: 'agentStream.tools.todoWrite',
  knowledge_graph_extract: 'agentStream.tools.knowledgeGraphExtract',
  thinking: 'agentStream.tools.thinking',
  image_analysis: 'agentStream.tools.imageAnalysis',
  query_knowledge_graph: 'agentStream.tools.queryKnowledgeGraph',
  final_answer: 'agentStream.tools.finalAnswer',
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

  return marked.parse(preprocessed, { breaks: true, async: false }) as string;
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

// 浮层状态（Web/KB 共用）
const KB_SNIPPET_LIMIT = 600;

const floatPopup = ref<{
  visible: boolean;
  top: number;
  left: number;
  width: number;
  type: 'kb' | 'web';
  // web
  url?: string;
  title?: string;
  // kb
  loading: boolean;
  error?: string;
  content?: string;
  chunkId?: string;
  knowledgeTitle?: string;
}>({
  visible: false,
  top: 0,
  left: 0,
  width: 420,
  type: 'kb',
  url: '',
  title: '',
  loading: false,
  error: undefined,
  content: '',
  chunkId: undefined,
});
let floatCloseTimer: number | null = null;

const scheduleFloatClose = () => {
  if (floatCloseTimer) window.clearTimeout(floatCloseTimer);
  floatCloseTimer = window.setTimeout(() => {
    // Double-check mouse is not over citation or popup before closing
    const hoveredCitation = document.querySelector('.citation-kb:hover, .citation-web:hover');
    const hoveredPopup = document.querySelector('.kb-float-popup:hover');
    if (!hoveredCitation && !hoveredPopup) {
      floatPopup.value.visible = false;
    }
  }, 300);
};

const cancelFloatClose = () => {
  if (floatCloseTimer) {
    window.clearTimeout(floatCloseTimer);
    floatCloseTimer = null;
  }
};

const openFloatForEl = (el: HTMLElement, widthAdjust = 120) => {
  const rect = el.getBoundingClientRect();
  const pageTop = window.scrollY || document.documentElement.scrollTop || 0;
  const pageLeft = window.scrollX || document.documentElement.scrollLeft || 0;
  // Reduce gap to minimize mouseout triggers when moving to popup
  floatPopup.value.top = rect.bottom + pageTop + 1;
  floatPopup.value.left = rect.left + pageLeft;
  floatPopup.value.width = Math.min(520, Math.max(380, rect.width + widthAdjust));
  floatPopup.value.visible = true;
  // Cancel any pending close when opening
  cancelFloatClose();
};

// Import icons
import agentIcon from '@/assets/img/agent.svg';
import thinkingIcon from '@/assets/img/Frame3718.svg';
import knowledgeIcon from '@/assets/img/zhishiku-thin.svg';
import documentIcon from '@/assets/img/ziliao.svg';
import fileAddIcon from '@/assets/img/file-add-green.svg';
import webSearchGlobeGreenIcon from '@/assets/img/websearch-globe-green.svg';

interface SessionData {
  isAgentMode?: boolean;
  agentEventStream?: any[];
  knowledge_references?: any[];
}

const props = defineProps<{
  session: SessionData;
  userQuery?: string;
}>();

// Configure marked for security
marked.use({});
marked.use(markedKatex({ throwOnError: false, nonStandard: true }));

const preprocessMathDelimiters = (rawText: string): string => {
  if (!rawText || typeof rawText !== 'string') {
    return '';
  }
  return rawText
    .replace(/\\\[([\s\S]*?)\\\]/g, '$$$$$1$$$$')
    .replace(/\\\(([\s\S]*?)\\\)/g, '$$$1$$');
};

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
    if (props.session?.is_completed) {
      renderMermaidDiagrams();
    }
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
    // Auto-scroll streaming steps container to bottom during streaming
    if (!hasAnswerStarted.value && streamingStepsContainer.value) {
      const el = streamingStepsContainer.value;
      if (el.scrollHeight > el.clientHeight) {
        el.scrollTop = el.scrollHeight;
      }
    }
  });
}, { immediate: true, deep: true });

// State for intermediate steps collapse
const showIntermediateSteps = ref(false);

// Track whether answer has started streaming (for early collapse)
const hasAnswerStarted = ref(false);
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

  if (hasAnswerStarted.value) return;

  const hasAnswer = stream.some((e: any) => e.type === 'answer' && e.content);
  if (hasAnswer) {
    hasAnswerStarted.value = true;
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
  
  // Check for answer event with done=true
  const answerEvents = stream.filter((e: any) => e.type === 'answer');
  const doneAnswer = answerEvents.find((e: any) => e.done === true);
  
  console.log('[Collapse] Answer events:', answerEvents.length, 'Done answer:', !!doneAnswer);
  
  return !!doneAnswer;
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

  // Check if there's an answer event with content (normal path via final_answer tool)
  const answerEvents = stream.filter((e: any) => e.type === 'answer');
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

  // Fallback: if no answer content (legacy path or LLM didn't call final_answer),
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
  return intermediateEvents.value.filter((e: any) => e.type === 'thinking' || e.type === 'tool_call').length;
});

const intermediateStepsSummary = computed(() => {
  if (!eventStream.value) {
    return '';
  }

  const steps = intermediateStepsCount.value;
  const elapsed = agentDurationMs.value;

  if (elapsed > 0) {
    return t('agent.stepsCompletedWithDuration', { steps, duration: formatDuration(elapsed) });
  }

  return t('agent.stepsCompleted', { steps });
});

// HTML version of intermediate steps summary with colored numbers
const intermediateStepsSummaryHtml = computed(() => {
  return intermediateStepsSummary.value;
});

// Should show the collapsed steps indicator (tree root)
// Triggers when answer starts streaming (early collapse) or when conversation is done
const shouldShowCollapsedSteps = computed(() => {
  const hasSteps = intermediateStepsCount.value > 0;
  return hasSteps && (hasAnswerStarted.value || isConversationDone.value);
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
  return result;
};

// When the agent loop ended via natural-stop (no final_answer tool call) and
// we promote the trailing thinking event into a virtual answer card below
// the tree, we must hide that same thinking event from the tree to avoid
// rendering its content twice.
const promotedThinkingEventId = computed<string | null>(() => {
  const final = finalContent.value;
  if (!final || final.type !== 'thinking') return null;
  // Only promote when there's no real answer event content already.
  const stream = eventStream.value;
  if (!stream || !Array.isArray(stream)) return null;
  const hasRealAnswer = stream.some(
    (e: any) => e.type === 'answer' && e.content && e.content.trim()
  );
  if (hasRealAnswer) return null;
  return final.event_id || null;
});

// Intermediate events (tree children: everything except answer)
const intermediateEvents = computed(() => {
  const stream = eventStream.value;
  if (!stream || !Array.isArray(stream)) return [];
  const result = buildFullEventList(stream);
  const promotedId = promotedThinkingEventId.value;
  return result.filter((e: any) => {
    if (e.type === 'answer' || e.type === 'agent_complete') return false;
    if (promotedId && e.type === 'thinking' && e.event_id === promotedId) return false;
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

  // If answer hasn't started and not done, show everything (no tree yet)
  if (!hasAnswerStarted.value && !isConversationDone.value) {
    return result;
  }

  // When tree is active (shouldShowCollapsedSteps), displayEvents only shows answer events
  // The intermediate steps are rendered inside the tree-children via intermediateEvents

  // When answer has started (streaming or done), show only answer events here
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
    // free text instead of calling final_answer). Synthesize a virtual
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

// KB citations: 悬停用浮层展示摘要；点击跳转 KB 详情
type KbTooltipState = {
  loading: boolean;
  error?: string;
  html?: string;
};

const kbChunkDetails = ref<Record<string, KbTooltipState>>({});

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

const buildKbTooltipContent = (content: string): string => {
  const escapedContent = escapeHtml(content).replace(/\n/g, '<br>');
  return `<span class="tip-content">${escapedContent}</span>`;
};

const getKbTooltipInnerHtml = (state: KbTooltipState): string => {
  if (state.error) {
    return `<span class="tip-error">${escapeHtml(state.error)}</span>`;
  }
  if (state.html) {
    return state.html;
  }
  return `<span class="tip-loading">${t('agentStream.citation.loading')}</span>`;
};

const syncFloatPopupFromCache = (chunkId: string, state: KbTooltipState) => {
  if (floatPopup.value.type !== 'kb' || floatPopup.value.chunkId !== chunkId) {
    return;
  }
  floatPopup.value.loading = state.loading;
  floatPopup.value.error = state.error;
  floatPopup.value.content = state.html || '';
};

const setKbCacheState = (chunkId: string, state: KbTooltipState) => {
  kbChunkDetails.value[chunkId] = state;
  updateKBCitationTooltip(chunkId, state);
  syncFloatPopupFromCache(chunkId, state);
};

const loadChunkDetails = async (chunkId: string) => {
  const cacheEntry = kbChunkDetails.value[chunkId];
  if (cacheEntry) {
    if (cacheEntry.loading) {
      updateKBCitationTooltip(chunkId, cacheEntry);
      syncFloatPopupFromCache(chunkId, cacheEntry);
      return;
    }
    if (cacheEntry.html || cacheEntry.error) {
      updateKBCitationTooltip(chunkId, cacheEntry);
      syncFloatPopupFromCache(chunkId, cacheEntry);
      return;
    }
  }

  setKbCacheState(chunkId, { loading: true });

  try {
    const response = await getChunkByIdOnly(chunkId);
    const content = response.data?.content;
    if (content) {
      const html = buildKbTooltipContent(content);
      setKbCacheState(chunkId, { loading: false, html });
      return;
    }

    setKbCacheState(chunkId, { loading: false, error: t('agentStream.citation.notFound') });
  } catch (error: any) {
    console.error('Failed to load chunk details:', error);
    const errorMsg = error?.message || t('agentStream.citation.loadFailed');
    setKbCacheState(chunkId, { loading: false, error: errorMsg });
  }
};

const updateKBCitationTooltip = (chunkId: string, state: KbTooltipState) => {
  // Find all KB citation elements with this chunk ID
  const citations = document.querySelectorAll(`.citation-kb[data-chunk-id="${chunkId}"]`);
  citations.forEach((citation) => {
    const tipElement = citation.querySelector('.citation-tip');
    if (tipElement) {
      const shortChunkId = `${chunkId.substring(0, 25)}...`;
      
      const renderContent = (inner: string) => {
        tipElement.innerHTML = `
          <span class="t-popup__content">
            ${inner}
            <span class="tip-meta">${t('agentStream.citation.chunkId')}: ${shortChunkId}</span>
          </span>
        `;
      };

      renderContent(getKbTooltipInnerHtml(state));
    }
  });
};

// 统一 hover 入口（Web/KB）
let kbHoverTimer: number | null = null;
const onHover = (e: Event) => {
  const target = e.target as HTMLElement;
  if (!target) return;
  const kbEl = target.closest?.('.citation-kb') as HTMLElement | null;
  const webEl = target.closest?.('.citation-web') as HTMLElement | null;
  // KB
  if (kbEl) {
    const chunkId = kbEl.getAttribute('data-chunk-id') || '';
    const knowledgeTitle = kbEl.getAttribute('data-doc') || '';
    if (!chunkId) return;
    if (kbHoverTimer) window.clearTimeout(kbHoverTimer);
    kbHoverTimer = window.setTimeout(() => {
      cancelFloatClose();
      floatPopup.value.type = 'kb';
      floatPopup.value.chunkId = chunkId;
      floatPopup.value.knowledgeTitle = knowledgeTitle;
      const cacheEntry = kbChunkDetails.value[chunkId];
      if (cacheEntry) {
        syncFloatPopupFromCache(chunkId, cacheEntry);
        updateKBCitationTooltip(chunkId, cacheEntry);
      } else {
        floatPopup.value.loading = true;
        floatPopup.value.error = undefined;
        floatPopup.value.content = '';
      }
      openFloatForEl(kbEl);

      if (!cacheEntry || (!cacheEntry.loading && !cacheEntry.html && !cacheEntry.error)) {
        loadChunkDetails(chunkId);
      }
    }, 80);
    return;
  }
  // Web
  if (webEl) {
    const url = webEl.getAttribute('data-url') || '';
    const title = webEl.querySelector('.tip-title')?.textContent || webEl.getAttribute('data-title') || '';
    if (kbHoverTimer) window.clearTimeout(kbHoverTimer);
    kbHoverTimer = window.setTimeout(() => {
      cancelFloatClose(); // Cancel any pending close
      floatPopup.value.type = 'web';
      floatPopup.value.url = url;
      floatPopup.value.title = title || '';
      openFloatForEl(webEl, 60);
    }, 40);
    return;
  }
};

const onHoverOut = (e: Event) => {
  const rt = (e as MouseEvent).relatedTarget as HTMLElement | null;
  // If mouse is moving to another citation or the popup, don't close
  if (rt && (rt.closest?.('.citation-kb') || rt.closest?.('.citation-web') || rt.closest?.('.kb-float-popup'))) {
    return;
  }
  // Cancel any pending hover timer
  if (kbHoverTimer) {
    window.clearTimeout(kbHoverTimer);
    kbHoverTimer = null;
  }
  // Use a small delay to allow mouse to move to popup
  // The scheduleFloatClose will double-check before actually closing
  scheduleFloatClose();
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
  // 使用 nextTick 确保 DOM 已渲染
  nextTick(async () => {
    const root = rootElement.value;
    if (!root) return;
    root.addEventListener('click', onRootClick, true);
    const keydownListener: EventListener = (evt: Event) => onRootKeydown(evt as KeyboardEvent);
    // Store on element for removal
    (root as any).__citationKeydown__ = keydownListener;
    root.addEventListener('keydown', keydownListener, true);
    // 统一 hover 监听
    root.addEventListener('mouseover', onHover, true);
    root.addEventListener('mouseout', onHoverOut, true);
    window.addEventListener('scroll', scheduleFloatClose, true);
    window.addEventListener('resize', scheduleFloatClose, true);
    await hydrateProtectedFileImages(rootElement.value);
  });
});

onBeforeUnmount(() => {
  const root = rootElement.value;
  if (!root) return;
  root.removeEventListener('click', onRootClick, true);
  root.removeEventListener('mouseover', onHover, true);
  root.removeEventListener('mouseout', onHoverOut, true);
  window.removeEventListener('scroll', scheduleFloatClose, true);
  window.removeEventListener('resize', scheduleFloatClose, true);
  const keydownListener: EventListener | undefined = (root as any).__citationKeydown__;
  if (keydownListener) {
    root.removeEventListener('keydown', keydownListener, true);
    delete (root as any).__citationKeydown__;
  }
});

const ATTRIBUTE_REGEX = /([\w-]+)\s*=\s*"([^"]*)"/g;

const parseTagAttributes = (attrString: string): Record<string, string> => {
  const attributes: Record<string, string> = {};
  if (!attrString) return attributes;

  ATTRIBUTE_REGEX.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = ATTRIBUTE_REGEX.exec(attrString)) !== null) {
    const key = match[1];
    const value = match[2];
    attributes[key] = value;
  }

  return attributes;
};

// Preprocess markdown to handle incomplete images and custom citations
const preprocessMarkdown = (contentStr: string): string => {
  if (!contentStr.trim()) return '';

  // Replace incomplete streaming image markdown with an in-place loading placeholder.
  // This avoids showing a half-baked provider:// URL while keeping layout stable.
  contentStr = replaceIncompleteImageWithPlaceholder(contentStr);

  // Preprocess custom citation tags
  return contentStr
    .replace(
      /<web\b([^>]*)\/>/g,
      (_m: string, attrString: string) => {
        const attrs = parseTagAttributes(attrString);
        const url = attrs.url || '';
        const title = attrs.title || '';

        if (!url) return '';

        let domain = url;
        try {
          const u = new URL(url);
          const host = u.hostname || '';
          const parts = host.split('.');
          if (parts.length >= 2) {
            domain = parts.slice(-2).join('.');
          } else {
            domain = host || url;
          }
        } catch {
          // keep original url text if parsing fails
        }
        const safeTitle = String(title || '').replace(/"/g, '&quot;');
        const safeUrl = String(url || '').replace(/"/g, '&quot;');
        const tipTitle = safeTitle || '';
        const tipUrl = safeUrl || '';
        return `<a class="citation citation-web" data-url="${safeUrl}" href="${safeUrl}" target="_blank" rel="noopener noreferrer"><span class="citation-icon web"></span><span class="citation-domain">${domain}</span><span class="citation-tip"><span class="tip-title">${tipTitle}</span><span class="tip-url">${tipUrl}</span></span></a>`;
      }
    )
    .replace(
      /<kb\b([^>]*)\/>/g,
      (_m, attrString: string) => {
        const attrs = parseTagAttributes(attrString);
        const doc = attrs.doc || '';
        const chunkId = attrs.chunk_id || attrs.chunkId || '';
        const kbId = attrs.kb_id || attrs.kbId || '';

        if (!doc || !chunkId) return '';

        const safeDoc = escapeHtml(doc);
        const safeKbId = escapeHtml(kbId);
        const safeChunkId = escapeHtml(chunkId);

        const truncateMiddle = (text: string, maxLength = 13): string => {
          if (!text) return '';
          if (text.length <= maxLength) return text;
          const half = Math.floor((maxLength - 3) / 2);
          const start = text.slice(0, half + ((maxLength - 3) % 2));
          const end = text.slice(-half);
          return `${start}...${end}`;
        };

        const displayDoc = escapeHtml(truncateMiddle(doc));
        return `<span class="citation citation-kb" data-kb-id="${safeKbId}" data-chunk-id="${safeChunkId}" data-doc="${safeDoc}" role="button" tabindex="0"><span class="citation-icon kb"></span><span class="citation-text">${displayDoc}</span><span class="citation-tip"><span class="t-popup__content"><span class="tip-loading">${t('agentStream.citation.loading')}</span></span></span></span>`;
      }
    )
    .replace(
      /\[\[([^\]]+)\]\]/g,
      (match, inner: string) => {
        const pipeIdx = inner.indexOf('|');
        const slug = pipeIdx > 0 ? inner.substring(0, pipeIdx).trim() : inner.trim();
        let display = slug;
        if (pipeIdx > 0) {
          display = inner.substring(pipeIdx + 1).trim();
        } else {
          // Fallback: strip type prefix like "summary/" or "concept/"
          const parts = slug.split('/');
          display = parts.length > 1 ? parts.slice(1).join('/') : slug;
        }

        // Bail out on empty slug; otherwise accept any non-empty slug.
        // Structural pages like "index" and "log" have no slash but are
        // still valid targets — the drawer renderer already treats them
        // as such, so the chat bubble must match.
        if (!slug) return match;

        const safeSlug = escapeHtml(slug);
        const safeDisplay = escapeHtml(display);
        return `<a href="#" class="wiki-content-link citation-wiki" data-slug="${safeSlug}">${safeDisplay}</a>`;
      }
    );
};

const HTML_PLACEHOLDER_RE = /@@WEKNORA_HTML_PLACEHOLDER_(\d+)@@/g;

const extractRenderableHtmlPlaceholders = (contentStr: string): { content: string; htmlSnippets: string[] } => {
  const htmlSnippets: string[] = [];
  const storeHtml = (html: string): string => {
    const idx = htmlSnippets.length;
    htmlSnippets.push(html);
    return `@@WEKNORA_HTML_PLACEHOLDER_${idx}@@`;
  };

  const content = contentStr
    .replace(/<(?:kb|web)\b[^>]*\/>/g, (match) => storeHtml(preprocessMarkdown(match)))
    .replace(/\[\[([^\]]+)\]\]/g, (match) => storeHtml(preprocessMarkdown(match)));

  return { content, htmlSnippets };
};

const restoreRenderableHtmlPlaceholders = (html: string, htmlSnippets: string[]): string => {
  if (!htmlSnippets.length) return html;
  return html.replace(HTML_PLACEHOLDER_RE, (_match, idx) => htmlSnippets[Number(idx)] || '');
};

// 自定义渲染器 - 支持 Mermaid
const agentRenderer = new marked.Renderer();
agentRenderer.code = createMermaidCodeRenderer('mermaid-agent');

// 单次渲染 Markdown 内容（替代 token-by-token，修复 KaTeX 公式在 streaming 时闪烁消失的问题）
const renderMarkdownContent = (content: any): string => {
  const contentStr = typeof content === 'string' ? content : String(content || '');
  if (!contentStr.trim()) return '';

  // Extract <kb.../> and <web.../> tags before sanitization to prevent
  // sanitizeForDisplay from stripping chunk_id labels and UUIDs inside them.
  const tagPlaceholders: string[] = [];
  const preserved = contentStr.replace(/<(?:kb|web)\b[^>]*\/>/g, (match) => {
    const idx = tagPlaceholders.length;
    tagPlaceholders.push(match);
    return `\x00TAG${idx}\x00`;
  });

  // CRITICAL FIX: Also protect image URLs from sanitizeForDisplay
  // Extract image markdown ![alt](url) before sanitization
  const imagePlaceholders: string[] = [];
  const preservedWithImages = preserved.replace(/!\[([^\]]*)\]\(([^)]+)\)/g, (match) => {
    const idx = imagePlaceholders.length;
    imagePlaceholders.push(match);
    return `\x00IMG${idx}\x00`;
  });

  // Preserve wiki links [[slug|name]]
  const wikiPlaceholders: string[] = [];
  const preservedWithWiki = preservedWithImages.replace(/\[\[([^\]]+)\]\]/g, (match) => {
    const idx = wikiPlaceholders.length;
    wikiPlaceholders.push(match);
    return `\x00WIKI${idx}\x00`;
  });

  let sanitized = sanitizeForDisplay(preservedWithWiki);

  // Restore preserved wiki links
  sanitized = sanitized.replace(/\x00WIKI(\d+)\x00/g, (_, idx) => wikiPlaceholders[Number(idx)]);

  // Restore preserved images
  sanitized = sanitized.replace(/\x00IMG(\d+)\x00/g, (_, idx) => imagePlaceholders[Number(idx)]);

  // Restore preserved tags
  sanitized = sanitized.replace(/\x00TAG(\d+)\x00/g, (_, idx) => tagPlaceholders[Number(idx)]);

  const mathSafe = preprocessMathDelimiters(sanitized);
  const imageSafe = replaceIncompleteImageWithPlaceholder(mathSafe);
  const { content: markdownWithPlaceholders, htmlSnippets } = extractRenderableHtmlPlaceholders(imageSafe);
  const html = marked.parse(markdownWithPlaceholders, { renderer: agentRenderer }) as string;
  const htmlWithCitations = restoreRenderableHtmlPlaceholders(html, htmlSnippets);
  const protectedHTML = protectProviderImageSrcInHTML(htmlWithCitations);
  return DOMPurify.sanitize(protectedHTML, DOMPurifyConfig);
};

// Renders an answer event's content. Strips final-answer wrappers
// (e.g. <answer>…</answer>, "Final Answer:") that some models emit instead
// of calling the structured final_answer tool, then delegates to the
// standard markdown renderer.
const renderAnswerContent = (content: any): string => {
  const contentStr = typeof content === 'string' ? content : String(content || '');
  return renderMarkdownContent(unwrapFinalAnswerWrappers(contentStr));
};

// Legacy Markdown rendering function (kept for summaries)
const renderMarkdown = (content: any): string => {
  const contentStr = typeof content === 'string' ? content : String(content || '');
  if (!contentStr.trim()) return '';

  try {
    const mathSafe = preprocessMathDelimiters(contentStr);
    const imageSafe = replaceIncompleteImageWithPlaceholder(mathSafe);
    const { content: markdownWithPlaceholders, htmlSnippets } = extractRenderableHtmlPlaceholders(imageSafe);
    const html = marked.parse(markdownWithPlaceholders, { renderer: agentRenderer }) as string;
    if (!html) return '';

    const htmlWithCitations = restoreRenderableHtmlPlaceholders(html, htmlSnippets);
    const protectedHTML = protectProviderImageSrcInHTML(htmlWithCitations);
    return DOMPurify.sanitize(protectedHTML, DOMPurifyConfig);
  } catch (e) {
    console.error('Markdown rendering error:', e, 'Content:', contentStr.substring(0, 100));
    return contentStr.replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }
};

const protectProviderImageSrcInHTML = (html: string): string => {
  if (!html) return html;
  const placeholder = 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==';
  return html.replace(
    /<img\b([^>]*?)\ssrc=(["'])(local|minio|cos|tos):\/\/([^"']+)\2([^>]*)>/gi,
    (_m, before, quote, provider, restPath, after) => {
      const src = `${provider}://${restPath}`;
      return `<img${before} src=${quote}${placeholder}${quote} data-protected-src=${quote}${src}${quote}${after}>`;
    },
  );
};

// 渲染 Mermaid 图表的函数
const renderMermaidDiagrams = async () => {
  await renderMermaidInContainer(rootElement.value);
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

// Check if tool should use book icon
const isBookIcon = (toolName: string): boolean => {
  return false; // 不再使用 t-icon 的 book，改用 SVG 图标
};

// Get icon for tool type
const getToolIcon = (toolName: string): string => {
  if (toolName === 'thinking') {
    return thinkingIcon;
  } else if (toolName === 'search_knowledge' || toolName === 'knowledge_search') {
    return knowledgeIcon;
  } else if (toolName === 'grep_chunks') {
    return knowledgeIcon; // Use same icon as knowledge_search for consistency
  } else if (toolName === 'web_search') {
    return webSearchGlobeGreenIcon;
  } else if (toolName === 'get_document_info' || toolName === 'list_knowledge_chunks') {
    return documentIcon;
  } else if (toolName === 'todo_write') {
    return fileAddIcon;
  } else if (toolName === 'image_analysis') {
    return thinkingIcon;
  } else if (toolName.startsWith('mcp_')) {
    return documentIcon; // MCP external tool icon
  } else {
    return documentIcon; // default icon
  }
};

// Get search results summary text (returns HTML with colored numbers)
const getSearchResultsSummary = (event: any): string => {
  if (!event || !event.tool_data) return '';
  
  const toolData = event.tool_data;
  const count = toolData.results?.length || toolData.count || 0;
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
  
  const count = toolData.results?.length || toolData.count || 0;
  if (count === 0) return '';
  
  return t('agentStream.search.webResults', { count });
};

// Get results count (number only) for web search summary
const getResultsCount = (toolData: any): number => {
  if (!toolData) return 0;
  return toolData.results?.length || toolData.count || 0;
};

// Get grep results summary text (returns HTML with colored numbers)
const getGrepResultsSummary = (toolData: any): string => {
  if (!toolData) return '';
  
  const totalMatches = toolData.total_matches || 0;
  const resultCount = toolData.result_count || 0;
  
  if (totalMatches === 0) {
    return t('agentStream.search.noResults');
  }

  let summary = t('agentStream.search.foundMatches', { count: `<strong>${totalMatches}</strong>` });
  if (totalMatches > resultCount) {
    summary += t('agentStream.search.showingCount', { count: `<strong>${resultCount}</strong>` });
  }
  
  return summary;
};

// Extract and format query parameters from args
const getQueryText = (args: any): string => {
  if (!args) return '';
  
  // Parse if it's a string
  let parsedArgs = args;
  if (typeof parsedArgs === 'string') {
    try {
      parsedArgs = JSON.parse(parsedArgs);
    } catch (e) {
      return '';
    }
  }
  
  if (!parsedArgs || typeof parsedArgs !== 'object') return '';
  
  const queries: string[] = [];
  
  // Add query if exists
  if (parsedArgs.query && typeof parsedArgs.query === 'string') {
    queries.push(parsedArgs.query);
  }
  
  // Add vector_queries if exists
  if (Array.isArray(parsedArgs.queries) && parsedArgs.queries.length > 0) {
    queries.push(...parsedArgs.queries
      .filter((q: any) => q && typeof q === 'string')
      );
  }
  
  // Join all queries with comma and remove duplicates
  const uniqueQueries = Array.from(new Set(queries));
  return uniqueQueries.join('，');
};

// Get tool title - prefer summary over description, add query for search tools
const getToolTitle = (event: any): string => {
  if (event.pending) {
    if (event.tool_name === 'image_analysis') {
      return t('agentStream.toolStatus.imageAnalyzing');
    }
    const localizedName = getLocalizedToolName(event.tool_name);
    return t('agentStream.toolStatus.calling', { name: localizedName });
  }

  const toolName = event.tool_name;
  const isSearchTool = toolName === 'search_knowledge' || toolName === 'knowledge_search';
  const isWebSearchTool = toolName === 'web_search';
  const isGrepTool = toolName === 'grep_chunks';
  
  // For search tools, use description with query text
  if (isSearchTool) {
    const baseTitle = getToolDescription(event);
    if (event.arguments) {
      const queryText = getQueryText(event.arguments);
      if (queryText) {
        return `${baseTitle}：「${queryText}」`;
      }
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
      if (Array.isArray(event.arguments.patterns)) {
        patterns = event.arguments.patterns;
      } else if (event.arguments.pattern) {
        patterns = [event.arguments.pattern];
      }
    } else if (event.tool_data) {
      if (Array.isArray(event.tool_data.patterns)) {
        patterns = event.tool_data.patterns;
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
    const localizedName = getLocalizedToolName(event.tool_name);
    return t('agentStream.toolStatus.calling', { name: localizedName });
  }

  const success = event.success === true;
  const toolName = event.tool_name;

  if (toolName === 'search_knowledge' || toolName === 'knowledge_search') {
    return success ? t('agentStream.toolStatus.searchKb') : t('agentStream.toolStatus.searchKbFailed');
  } else if (toolName === 'web_search') {
    return success ? t('agentStream.toolStatus.webSearch') : t('agentStream.toolStatus.webSearchFailed');
  } else if (toolName === 'get_document_info') {
    return success ? t('agentStream.toolStatus.getDocInfo') : t('agentStream.toolStatus.getDocInfoFailed');
  } else if (toolName === 'thinking') {
    return success ? t('agentStream.toolStatus.thinkingDone') : t('agentStream.toolStatus.thinkingFailed');
  } else if (toolName === 'todo_write') {
    return success ? t('agentStream.toolStatus.updateTodos') : t('agentStream.toolStatus.updateTodosFailed');
  } else if (toolName === 'image_analysis') {
    return success ? t('agentStream.toolStatus.imageAnalysisDone') : t('agentStream.toolStatus.imageAnalysisFailed');
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
@import '../../../components/css/markdown.less';
@import '../../../components/css/chat-message-shared.less';

.agent-stream-display {
  display: flex;
  flex-direction: column;
  gap: 0;
  margin-bottom: 10px;
  position: relative;
}

// Streaming steps container
.streaming-steps-container {
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
  margin-bottom: 12px;

  &.event-answer {
    // answer 事件无特殊左侧装饰
  }
}

// ============ Tree View ============
.tree-container {
  margin-bottom: 10px;
  position: relative;
}

.tree-root {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 6px 14px;
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  border-radius: 8px;
  background-color: var(--td-bg-color-container);
  border: .5px solid var(--td-component-stroke);
  box-shadow: 0 2px 4px rgba(7, 192, 95, 0.08);
  color: var(--td-text-color-primary);
  transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);

  &:hover {
    background-color: rgba(7, 192, 95, 0.04);
  }
}

.tree-root-title {
  display: flex;
  align-items: center;

  img {
    width: 16px;
    height: 16px;
    color: var(--td-brand-color);
    fill: currentColor;
    margin-right: 8px;
  }

  span {
    white-space: nowrap;
    font-size: 12px;

    :deep(strong) {
      color: var(--td-brand-color);
      font-weight: 600;
    }
  }
}

.tree-root-toggle {
  font-size: 13px;
  padding: 0 2px 1px 2px;
  color: var(--td-brand-color);
}

.tree-children {
  position: relative;
  padding-left: 12px; // indent for branch lines
  margin-top: 6px; // gap from root
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

.tree-child {
  position: relative;
  padding-left: 20px; // space for the horizontal branch
  padding-bottom: 0;
  margin-bottom: 6px; // gap between children

  // vertical trunk line (continues for non-last children)
  // bottom: -6px extends the line through the margin-bottom gap between siblings
  &::before {
    content: '';
    position: absolute;
    left: 0;
    top: 0;
    bottom: -6px;
    width: 0;
    border-left: 1px dashed var(--td-component-stroke);
  }

  // horizontal branch connector
  .tree-branch {
    position: absolute;
    left: 0;
    top: 15px; // align with the middle of the child card header
    width: 16px;
    height: 0;
    border-top: 1px dashed var(--td-component-stroke);
  }

  // last child: vertical line only goes to the branch, then stops
  &.tree-child-last {
    margin-bottom: 0;

    &::before {
      bottom: auto;
      height: 16px; // stops at the branch level
    }
  }
}

.tree-child-content {
  // child content area
}

// Thinking detail content (inside action-details)
.thinking-detail-content {
  padding: 2px 12px;
  font-size: 13px;
  color: var(--td-text-color-primary);
  line-height: 1.6;
  max-height: 200px;
  overflow-y: auto;
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
    font-size: 15px;
    color: var(--td-text-color-primary);
    line-height: 1.6;
    
    &.markdown-content {
      /* citation-web styles moved to global fallback below to avoid duplication */
      
      /* keyboard focus */
      :deep(.citation-web:focus-visible) {
        outline: 2px solid var(--td-success-color); /* green-400 */
        outline-offset: 2px;
      }
      
      /* KB citation styles are defined globally, no need to override here */
      
      :deep(p) {
        margin: 6px 0;
        line-height: 1.6;
      }
      
      :deep(code) {
        background: var(--td-bg-color-secondarycontainer);
        padding: 2px 5px;
        border-radius: 3px;
        font-family: var(--app-font-family-mono);
        font-size: 11px;
      }
      
      :deep(pre) {
        background: var(--td-bg-color-secondarycontainer);
        padding: 10px;
        border-radius: 4px;
        overflow-x: auto;
        margin: 6px 0;
        
        code {
          background: none;
          padding: 0;
        }
      }
      
      :deep(ul), :deep(ol) {
        margin: 6px 0;
        padding-left: 20px;
      }
      
      :deep(li) {
        margin: 3px 0;
      }
      
      :deep(blockquote) {
        border-left: 2px solid var(--td-brand-color);
        padding-left: 10px;
        margin: 6px 0;
        color: var(--td-text-color-secondary);
      }
      
      :deep(h1), :deep(h2), :deep(h3), :deep(h4), :deep(h5), :deep(h6) {
        margin: 10px 0 6px 0;
        font-weight: 600;
        color: var(--td-text-color-primary);
      }
      
      :deep(a) {
        color: var(--td-brand-color);
        text-decoration: none;
        
        &:hover {
          text-decoration: underline;
        }
      }
      
      :deep(table) {
        border-collapse: collapse;
        margin: 6px 0;
        font-size: 11px;

        th, td {
          border: 1px solid var(--td-component-stroke);
          padding: 5px 8px;
        }

        th {
          background: var(--td-bg-color-secondarycontainer);
          font-weight: 600;
        }
      }

      :deep(img) {
        max-width: 80%;
        max-height: 300px;
        width: auto;
        height: auto;
        min-height: 100px; /* 防止流式输出时图片高度塌陷导致抖动 */
        border-radius: 8px;
        display: block;
        margin: 8px 0;
        border: 0.5px solid var(--td-component-stroke);
        object-fit: contain;
        cursor: pointer;
        transition: transform 0.2s ease;
        background-color: var(--td-bg-color-secondarycontainer); /* 加载时的占位背景色 */

        &:hover {
        }
      }

      // Mermaid 图表样式
      :deep(.mermaid) {
        margin: 16px 0;
        padding: 16px;
        background: var(--td-bg-color-secondarycontainer);
        border-radius: 8px;
        overflow-x: auto;
        text-align: center;

        svg {
          max-width: 100%;
          height: auto;
        }
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
    background: var(--td-bg-color-container);
    border-radius: 5px;
    border: 1px solid var(--td-component-stroke);
    overflow: hidden;
    position: relative;
    transition: all 0.2s ease;
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.02);

    > * {
      position: relative;
      z-index: 1;
    }

    &:hover {
      border-color: var(--td-brand-color);
      box-shadow: 0 1px 4px rgba(7, 192, 95, 0.08);
    }

    &.action-error {
      border-left: 2px solid var(--td-error-color);
    }
    
    &.action-pending {
      opacity: 1;
      box-shadow: none;
      border-color: rgba(7, 192, 95, 0.15);
      background: linear-gradient(120deg, rgba(7, 192, 95, 0.01), var(--td-bg-color-container));

      &::after {
        content: '';
        position: absolute;
        inset: 0;
        background: linear-gradient(
          120deg,
          transparent 0%,
          rgba(7, 192, 95, 0.06) 40%,
          rgba(7, 192, 95, 0.08) 55%,
          transparent 85%
        );
        transform: translateX(-100%);
        animation: actionPendingShimmer 2.8s ease-in-out infinite;
        pointer-events: none;
        z-index: 0;
      }
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
      font-weight: 400;
      line-height: 1.6;
      color: var(--td-text-color-primary);
      
      :deep(p) {
        margin: 3px 0;
        color: var(--td-text-color-primary);
      }
      
      :deep(ul), :deep(ol) {
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
  justify-content: space-between;
  align-items: center;
  padding: 5px 10px;
  color: var(--td-text-color-primary);
  font-weight: 500;
  cursor: pointer;
  user-select: none;
  transition: background-color 0.15s ease;

  &:hover {
    background-color: rgba(7, 192, 95, 0.03);
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
  gap: 7px;
  flex: 1;
  min-width: 0;
  
  .action-title-icon {
    width: 14px;
    height: 14px;
    color: var(--td-brand-color);
    fill: currentColor;
    flex-shrink: 0;
    
    :deep(svg) {
      width: 14px;
      height: 14px;
      color: var(--td-brand-color);
      fill: currentColor;
    }
  }
  
  :deep(.t-tooltip) {
    flex: 1;
    min-width: 0;
  }
  
  .action-name {
    white-space: nowrap;
    font-size: 12px;
  }

  .action-badge {
    display: inline-flex;
    align-items: center;
    padding: 0 6px;
    height: 18px;
    border-radius: 9px;
    background: rgba(7, 192, 95, 0.10);
    color: var(--td-brand-color);
    font-size: 11px;
    font-weight: 500;
    white-space: nowrap;
    flex-shrink: 0;
  }

  .action-summary {
    color: var(--td-text-color-placeholder);
    font-size: 12px;
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
  0%, 80%, 100% {
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
  0%, 100% {
    transform: scale(1);
    opacity: 0.8;
  }
  50% {
    transform: scale(1.5);
    opacity: 0.3;
  }
}

@keyframes typingBounce {
  0%, 60%, 100% {
    transform: translateY(0);
  }
  30% {
    transform: translateY(-8px);
  }
}

@keyframes wave {
  0%, 40%, 100% {
    transform: scaleY(0.4);
  }
  20% {
    transform: scaleY(1);
  }
}

@keyframes pulseBorder {
  0%, 100% {
    border-left-color: var(--td-brand-color);
    box-shadow: 0 1px 3px rgba(7, 192, 95, 0.06);
  }
  50% {
    border-left-color: var(--td-brand-color);
    box-shadow: 0 1px 4px rgba(7, 192, 95, 0.12);
  }
}

@keyframes shakeError {
  0%, 100% {
    transform: translateX(0);
  }
  10%, 30%, 50%, 70%, 90% {
    transform: translateX(-2px);
  }
  20%, 40%, 60%, 80% {
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
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
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
}

.action-details {
  padding: 0;
  border-top: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
  display: flex;
  flex-direction: column;
}

.tool-result-wrapper {
  margin: 0;
}

.search-results-summary-fixed {
  padding: 6px 10px;
  background: var(--td-bg-color-container);
  border-top: 1px solid var(--td-component-stroke);
  
  .results-summary-text {
    font-size: 12px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    line-height: 1.5;
    
    :deep(strong) {
      color: var(--td-brand-color);
      font-weight: 600;
    }
  }
}

.plan-status-summary-fixed {
  padding: 6px 10px;
  background: var(--td-bg-color-container);
  border-top: 1px solid var(--td-component-stroke);
  
  .plan-status-text {
    font-size: 12px;
    font-weight: 500;
    color: var(--td-text-color-primary);
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
  min-height: 20px;
  
  .plan-task-change-card {
    padding: 8px 12px;
    background: linear-gradient(135deg, rgba(7, 192, 95, 0.05), rgba(7, 192, 95, 0.02));
    border-radius: 6px;
    border: 1px solid rgba(7, 192, 95, 0.2);
    font-size: 12px;
    color: var(--td-text-color-primary);
    
    .plan-task-change-content {
      strong {
        color: var(--td-brand-color);
        font-weight: 600;
        margin-right: 3px;
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

/* Global citation styles fallback to ensure rendering in any container */
:deep(.citation) {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border-radius: 10px;
  padding: 2px 4px;
  font-size: 11px;
  line-height: 1.4;
  background-clip: padding-box;
  margin: 0 4px;
}

:deep(.citation .citation-tip) {
  display: none;
}

:deep(.citation-web) {
  /* Align with app primary green scheme */
  background: var(--td-success-color-light);           /* green-50 */
  color: var(--td-success-color);                /* green-800 */
  border: 1px solid var(--td-success-color-focus);     /* green-200 */
  cursor: pointer;
  white-space: nowrap;
  position: relative;
}

:deep(.citation-web:hover) {
  /* Subtle hover in green tone */
  background: var(--td-success-color-light);           /* green-100 */
  border-color: var(--td-success-color);         /* green-300 */
  color: var(--td-success-color);                /* keep readable on light bg */
}

/* Embedded tooltip bubble - hidden, use global floatPopup instead */
:deep(.citation-web .citation-tip) {
  display: none !important;
  pointer-events: none;
}


/* Citation icons */
:deep(.citation .citation-icon) {
  display: inline-block;
  width: 14px;
  height: 14px;
  margin-right: 0px;
  background-repeat: no-repeat;
  background-size: contain;
  background-position: center;
  flex-shrink: 0;
}

/* Web icon (globe) */
:deep(.citation .citation-icon.web) {
  background-image: url("../../../assets/img/websearch-globe-green.svg");
}

/* Knowledge base icon */
:deep(.citation .citation-icon.kb) {
  background-image: url("../../../assets/img/zhishiku-thin.svg");
}

.kb-float-popup {
  position: absolute;
  z-index: 10000;
  pointer-events: auto;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  border: none !important;
  box-shadow: 0 6px 18px rgba(0,0,0,0.2);
  padding: 12px 14px;
  color: var(--td-text-color-primary);
  line-height: 1.5;
  font-size: 12px;
  box-sizing: border-box;
  max-width: 520px;
}

.kb-float-popup .t-popup__content {
  display: flex;
  flex-direction: column;
  gap: 4px;
  border: none !important;
  padding: 0 !important;
  margin: 0 !important;
  background: transparent !important;
  box-shadow: none !important;
}

.kb-float-popup .tip-title {
  font-weight: 600;
  color: var(--td-brand-color);
}

.kb-float-popup .tip-url {
  word-break: break-word;
}

.kb-float-popup .tip-meta {
  margin-top: 1px;
  font-size: 11px;
  color: var(--td-text-color-secondary);
}

.kb-float-popup .tip-loading {
  color: var(--td-text-color-secondary);
  font-style: italic;
}

.kb-float-popup .tip-error {
  color: var(--td-error-color);
  font-weight: 500;
}

.kb-float-popup .tip-content {
  border: none !important;
  padding: 0 !important;
  margin: 0 !important;
  background: transparent !important;
  box-shadow: none !important;
  max-height: 250px;
  overflow-y: auto;
  overflow-x: hidden;
}

/* KB citation styles - same green theme as web citations */
:deep(.citation.citation-kb) {
  /* Green theme - same as web citations */
  background: var(--td-success-color-light);           /* green-50 */
  color: var(--td-success-color);                /* green-800 */
  border: 1px solid var(--td-success-color-focus);     /* green-200 */
  cursor: pointer;
  white-space: nowrap;
  position: relative;
  transition: all 0.2s ease;
}

:deep(.citation.citation-kb:hover) {
  /* Subtle hover in green tone */
  background: var(--td-success-color-light);           /* green-100 */
  border-color: var(--td-success-color);         /* green-300 */
  color: var(--td-success-color);                /* keep readable on light bg */
}

:deep(.citation.citation-kb:focus-visible) {
  outline: 2px solid var(--td-success-color);    /* green-400 */
  outline-offset: 2px;
}

/* KB citation tooltip styles (same as web citation) */
:deep(.citation.citation-kb .citation-tip) {
  display: none !important;
  pointer-events: none;
}

/* Inline wiki link style for chat bubbles — brand-blue dashed underline,
   matching the style used inside the wiki drawer. Rendered inline with
   the surrounding text, NOT as a pill, so it reads like a regular link. */
:deep(a.wiki-content-link) {
  color: var(--td-brand-color);
  text-decoration: none;
  border-bottom: 1px dashed var(--td-brand-color);
  cursor: pointer;
  font-weight: 500;
  transition: border-bottom-style 0.15s ease;
}

:deep(a.wiki-content-link:hover) {
  border-bottom-style: solid;
  text-decoration: none !important;
}

:deep(a.wiki-content-link:focus-visible) {
  outline: 2px solid var(--td-brand-color);
  outline-offset: 2px;
  border-radius: 2px;
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
  padding: 12px 0;
  margin-top: 0;
  padding-left: 0;
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
      width: 6px;
      height: 6px;
      border-radius: 50%;
      background: var(--td-brand-color);
      animation: typingBounce 1.4s ease-in-out infinite;
      
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

  .wiki-reader-body {
    line-height: 1.6;
    font-size: 14px;
    color: var(--td-text-color-primary);

    h1 { font-size: 24px; margin: 28px 0 16px; font-weight: 600; line-height: 1.4; }
    h2 { font-size: 18px; margin: 24px 0 12px; font-weight: 600; line-height: 1.4; }
    h3 { font-size: 16px; margin: 20px 0 10px; font-weight: 600; line-height: 1.5; }
    h4, h5, h6 { font-size: 14px; margin: 16px 0 8px; font-weight: 600; line-height: 1.5; }
    
    p { margin: 0 0 14px; }
    
    ul, ol { 
      margin: 0 0 14px; 
      padding-left: 24px; 
    }
    li { 
      margin-bottom: 6px; 
      line-height: 1.6;
    }
    li > p {
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
  }
}
// Dark mode: invert agent icon (uses currentColor which doesn't work in <img>)
html[theme-mode="dark"] .tree-root-title img {
  filter: invert(1);
  opacity: 0.55;
}
</style>
