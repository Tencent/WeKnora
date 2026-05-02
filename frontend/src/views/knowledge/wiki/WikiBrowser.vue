<template>
  <div class="wiki-browser">
    <!-- Graph view (full screen) -->
    <template v-if="view === 'graph'">
      <div class="wiki-graph">
        <div ref="graphRef" class="wiki-graph-canvas"></div>

        <!-- Graph Search Overlay -->
        <div v-if="graphReady" class="wiki-graph-search-container">
          <div class="wiki-graph-search">
            <t-select
              v-model="graphSearchValue"
              filterable
              :options="graphSearchOptions"
              :placeholder="$t('knowledgeEditor.wikiBrowser.searchPlaceholder')"
              @change="handleGraphSearchSelect"
              @enter="handleGraphSearchEnter"
              :popup-props="{ zIndex: 100 }"
              class="graph-search-select"
            >
              <template #prefixIcon><t-icon name="search" /></template>
            </t-select>
          </div>
          <div v-if="stats && stats.pending_issues > 0" class="wiki-global-issues-status graph-issues-badge" @click="showGlobalIssuesDrawer = true">
            <t-icon name="error-circle" style="color: var(--td-warning-color);" />
            <span class="queue-text">{{ $t('knowledgeEditor.wikiBrowser.globalIssuesCount', { count: stats.pending_issues }) }}</span>
          </div>
        </div>

        <!-- Legend Overlay -->
        <div v-if="graphReady" class="wiki-graph-legend">
          <div class="legend-items">
            <div 
              class="legend-item clickable" 
              :class="{ disabled: !graphFilterTypes.has('summary') }"
              @click="toggleGraphFilterType('summary')"
            >
              <span class="legend-dot" style="background: #0052d9"></span>
              {{ $t('knowledgeEditor.wikiBrowser.filterSummary') }}
            </div>
            <div 
              class="legend-item clickable"
              :class="{ disabled: !graphFilterTypes.has('entity') }"
              @click="toggleGraphFilterType('entity')"
            >
              <span class="legend-dot" style="background: #2ba471"></span>
              {{ $t('knowledgeEditor.wikiBrowser.filterEntity') }}
            </div>
            <div 
              class="legend-item clickable"
              :class="{ disabled: !graphFilterTypes.has('concept') }"
              @click="toggleGraphFilterType('concept')"
            >
              <span class="legend-dot" style="background: #e37318"></span>
              {{ $t('knowledgeEditor.wikiBrowser.filterConcept') }}
            </div>
            <div 
              class="legend-item clickable"
              :class="{ disabled: !graphFilterTypes.has('synthesis') }"
              @click="toggleGraphFilterType('synthesis')"
            >
              <span class="legend-dot" style="background: #0594fa"></span>
              {{ $t('knowledgeEditor.wikiBrowser.filterSynthesis') }}
            </div>
            <div 
              class="legend-item clickable"
              :class="{ disabled: !graphFilterTypes.has('comparison') }"
              @click="toggleGraphFilterType('comparison')"
            >
              <span class="legend-dot" style="background: #d54941"></span>
              {{ $t('knowledgeEditor.wikiBrowser.filterComparison') }}
            </div>
          </div>
          <div class="legend-divider"></div>
          <div class="legend-actions">
            <div class="legend-action" @click="fitGraphToView" title="Fit to View">
              <span class="legend-action-icon"><t-icon name="focus" /></span>
              <span>{{ $t('knowledgeEditor.wikiBrowser.fitView') || '适应屏幕' }}</span>
            </div>
            <div class="legend-action" @click="toggleArrows">
              <span class="legend-action-icon"><t-icon :name="showArrows ? 'browse-off' : 'browse'" /></span>
              <span>{{ showArrows ? $t('knowledgeEditor.wikiBrowser.hideArrows') : $t('knowledgeEditor.wikiBrowser.showArrows') }}</span>
            </div>
          </div>
        </div>

        <div v-if="!graphReady" class="wiki-reader-empty wiki-graph-empty">
          <t-loading v-if="graphLoading" />
          <div v-else class="wiki-empty-icon">
            <t-icon name="chart-bubble" size="48px" />
          </div>
          <p class="wiki-empty-desc">{{ graphLoading ? $t('knowledgeEditor.wikiBrowser.graphEmpty') : $t('knowledgeEditor.wikiBrowser.graphNoData') }}</p>
        </div>

        <!-- Graph page detail drawer -->
        <t-drawer
          v-model:visible="graphDrawerVisible"
          :header="graphDrawerPage?.title || ''"
          size="480px"
          :footer="false"
          placement="right"
          :attach="false"
          :show-overlay="false"
          :close-btn="true"
          destroy-on-close
          class="wiki-graph-drawer"
        >
          <template v-if="graphDrawerPage">
            <div class="wiki-reader-meta" style="margin-bottom: 16px;">
              <t-tag size="small" :theme="getTypeTheme(graphDrawerPage.page_type)" variant="light-outline">
                {{ getTypeLabel(graphDrawerPage.page_type) }}
              </t-tag>
              <span class="wiki-reader-meta-text">{{ $t('knowledgeEditor.wikiBrowser.version', { ver: graphDrawerPage.version }) }}</span>
            </div>
            <div ref="drawerBodyRef" class="wiki-reader-body" v-html="graphDrawerContent" @click="handleGraphDrawerClick"></div>
          </template>
        </t-drawer>
      </div>
    </template>

    <!-- Browser view (left list + right reader) -->
    <template v-else>
      <!-- Left Panel: Page List -->
      <aside class="wiki-sidebar">
        <div class="wiki-sidebar-header">
          <div v-if="stats && (stats.pending_tasks > 0 || stats.is_active)" class="wiki-queue-status">
            <t-loading size="small" />
            <span class="queue-text">{{ $t('knowledgeEditor.wikiBrowser.queueStatus', { count: stats.pending_tasks || 0 }) }}</span>
          </div>
          <!-- Global Issues -->
          <div v-if="stats && stats.pending_issues > 0" class="wiki-global-issues-status" @click="showGlobalIssuesDrawer = true">
            <t-icon name="error-circle" style="color: var(--td-warning-color);" />
            <span class="queue-text">{{ $t('knowledgeEditor.wikiBrowser.globalIssuesCount', { count: stats.pending_issues }) }}</span>
          </div>
          <t-input
            v-model="searchQuery"
            :placeholder="$t('knowledgeEditor.wikiBrowser.searchPlaceholder')"
            clearable
            @enter="doSearch"
            @clear="loadPages"
          >
            <template #prefixIcon><t-icon name="search" /></template>
          </t-input>
        </div>

        <div class="wiki-page-list">
          <!-- Index page (pinned at top) -->
          <div
            v-if="indexPage"
            :class="['wiki-nav-item', { active: selectedPage?.id === indexPage.id }]"
            @click="selectPage(indexPage)"
          >
            <t-icon name="catalog" class="wiki-nav-icon" />
            <span class="wiki-nav-text">{{ $t('knowledgeEditor.wikiBrowser.indexTitle') }}</span>
          </div>

          <!-- Log page (pinned) -->
          <div
            v-if="logPage"
            :class="['wiki-nav-item', { active: selectedPage?.id === logPage.id }]"
            @click="selectPage(logPage)"
          >
            <t-icon name="history" class="wiki-nav-icon" />
            <span class="wiki-nav-text">{{ $t('knowledgeEditor.wikiBrowser.logTitle') }}</span>
          </div>

          <div class="wiki-sidebar-divider" v-if="indexPage || logPage"></div>

          <!-- Grouped by type (collapsible) -->
          <template v-for="group in groupedPages" :key="group.type">
            <div
              class="wiki-group-label"
              @click="toggleGroup(group.type)"
            >
              <t-icon
                :name="collapsedGroups[group.type] ? 'chevron-right' : 'chevron-down'"
                size="12px"
                class="wiki-group-chevron"
              />
              {{ group.label }}
              <span class="wiki-group-count">{{ group.pages.length }}</span>
            </div>
            <template v-if="!collapsedGroups[group.type]">
              <div
                v-for="page in group.pages"
                :key="page.id"
                :class="['wiki-page-item', { active: selectedPage?.id === page.id }]"
                @click="selectPage(page)"
              >
                <div class="wiki-page-item-title">{{ page.title }}</div>
                <div class="wiki-page-item-summary">{{ page.summary }}</div>
                <div class="wiki-page-item-meta">
                  <span>{{ formatDate(page.updated_at) }}</span>
                </div>
              </div>
            </template>
          </template>

          <!-- Empty state -->
          <div v-if="contentPages.length === 0 && !loading" class="wiki-empty-state">
            <div class="wiki-empty-icon">
              <t-icon name="file-unknown" size="36px" />
            </div>
            <p class="wiki-empty-title">{{ $t('knowledgeEditor.wikiBrowser.emptyTitle') }}</p>
            <p class="wiki-empty-desc">{{ $t('knowledgeEditor.wikiBrowser.emptyDesc') }}</p>
          </div>
        </div>
      </aside>

      <!-- Right Panel: Reader -->
      <div class="wiki-content">
        <div class="wiki-reader">
          <div class="wiki-reader-inner">
            <template v-if="selectedPage">
              <!-- Navigation -->
              <div v-if="navHistory.length" class="wiki-nav-bar">
                <a href="#" class="wiki-nav-back" @click.prevent="goBack">
                  <t-icon name="arrow-left" size="14px" />
                  <span>{{ navHistory[navHistory.length - 1].title }}</span>
                </a>
              </div>

              <!-- Page header -->
              <div class="wiki-reader-header">
                <h2 class="wiki-reader-title" style="display: flex; align-items: center;">
                  {{ selectedPage.title }}
                  
                  <t-popup
                    v-if="pageIssues.length > 0"
                    v-model="showIssuesBox"
                    placement="bottom-left"
                    trigger="click"
                    :overlayInnerStyle="{ padding: 0, boxShadow: 'var(--td-shadow-3)', borderRadius: '8px', width: '560px', maxWidth: '90vw' }"
                  >
                    <span 
                      class="wiki-issue-trigger"
                      :title="$t('knowledgeEditor.wikiBrowser.issueTitle', { count: pageIssues.length })"
                    >
                      <t-icon name="error-circle-filled" style="color: var(--td-warning-color);" />
                    </span>

                    <template #content>
                      <div class="wiki-issue-popup-content">
                        <div class="wiki-issue-popup-header">
                          <div class="wiki-issue-popup-title">
                            <span>{{ $t('knowledgeEditor.wikiBrowser.issueFixSuggestions', { count: pageIssues.length }) }}</span>
                          </div>
                          <t-button size="small" theme="primary" variant="base" @click="triggerAutoFix">
                            <template #icon><t-icon name="tools" /></template>
                            {{ $t('knowledgeEditor.wikiBrowser.issueFixBtn') }}
                          </t-button>
                        </div>
                      <div class="wiki-issue-popup-list">
                        <div v-for="issue in pageIssues" :key="issue.id" class="wiki-issue-popup-item">
                          <div class="wiki-issue-popup-main">
                            <div class="wiki-issue-popup-tags">
                              <t-tag v-if="issue.issue_type === 'mixed_entities'" theme="warning" variant="light" size="small">{{ $t('knowledgeEditor.wikiBrowser.issueMixed') }}</t-tag>
                              <t-tag v-else-if="issue.issue_type === 'contradictory_facts'" theme="danger" variant="light" size="small">{{ $t('knowledgeEditor.wikiBrowser.issueConflict') }}</t-tag>
                              <t-tag v-else-if="issue.issue_type === 'out_of_date'" theme="default" variant="light" size="small">{{ $t('knowledgeEditor.wikiBrowser.issueOutdated') }}</t-tag>
                              <t-tag v-else theme="primary" variant="light" size="small">{{ $t('knowledgeEditor.wikiBrowser.issueAttention') }}</t-tag>
                            </div>
                            <div class="wiki-issue-popup-desc">
                              {{ issue.description }}
                            </div>
                            <div class="wiki-issue-popup-meta">
                              <span class="wiki-issue-popup-reporter">
                                {{ issue.reported_by === 'wiki-researcher-agent' ? $t('knowledgeEditor.wikiBrowser.issueAiLinter') : $t('knowledgeEditor.wikiBrowser.issueReportedBy', { reporter: issue.reported_by }) }}
                              </span>
                              <div class="wiki-issue-popup-actions">
                                <span class="wiki-issue-popup-action" @click="triggerFixIssue(issue)" style="margin-right: 12px; font-weight: 500;">
                                  <t-icon name="tools" style="margin-right: 4px;" />{{ $t('knowledgeEditor.wikiBrowser.issueFixSingle') }}
                                </span>
                                <span class="wiki-issue-popup-action" style="color: var(--td-text-color-placeholder);" @click="handleIssueIgnore(issue.id)">{{ $t('knowledgeEditor.wikiBrowser.issueIgnore') }}</span>
                              </div>
                            </div>
                          </div>
                        </div>
                      </div>
                      </div>
                    </template>
                  </t-popup>
                </h2>
                <div v-if="selectedPage.aliases && selectedPage.aliases.length" class="wiki-reader-aliases">
                  <span class="wiki-alias-label">{{ $t('knowledgeEditor.wikiBrowser.aliases') }}:</span>
                  <t-tag v-for="alias in selectedPage.aliases" :key="alias" size="small" variant="light" class="wiki-alias-tag">
                    {{ alias }}
                  </t-tag>
                </div>
                <div class="wiki-reader-meta">
                  <t-tag size="small" :theme="getTypeTheme(selectedPage.page_type)" variant="light-outline">
                    {{ getTypeLabel(selectedPage.page_type) }}
                  </t-tag>
                  <span class="wiki-reader-meta-text">{{ $t('knowledgeEditor.wikiBrowser.version', { ver: selectedPage.version }) }}</span>
                  <span class="wiki-reader-meta-text">{{ formatDate(selectedPage.updated_at) }}</span>
                </div>
              </div>

              <!-- Backlinks (in_links) -->
              <div v-if="selectedPage.in_links?.length" class="wiki-reader-backlinks">
                <span class="wiki-backlink-label">
                  <t-icon name="link" size="14px" />
                  {{ $t('knowledgeEditor.wikiBrowser.linkedFrom') }}
                </span>
                <a
                  v-for="link in selectedPage.in_links"
                  :key="'in-' + link"
                  href="#"
                  class="wiki-backlink-tag"
                  @click.prevent="navigateToSlug(link)"
                >{{ slugDisplayName(link) }}</a>
              </div>

              <!-- Content -->
              <div ref="readerBodyRef" class="wiki-reader-body" v-html="renderedContent" @click="handleContentClick"></div>

              <!-- Source refs -->
              <div v-if="parsedSourceRefs.length" class="wiki-reader-sources">
                <span class="wiki-link-label">{{ $t('knowledgeEditor.wikiBrowser.sources') }}</span>
                <a
                  v-for="ref in parsedSourceRefs"
                  :key="ref.id"
                  href="#"
                  class="wiki-source-ref"
                  @click.prevent="emit('open-source-doc', ref.id)"
                >
                  <t-icon name="file" size="14px" />
                  {{ ref.title }}
                </a>
              </div>
            </template>

            <!-- No page selected -->
            <div v-else class="wiki-reader-empty">
              <div class="wiki-empty-icon">
                <t-icon name="browse" size="48px" />
              </div>
              <p class="wiki-empty-title" v-if="contentPages.length > 0">{{ $t('knowledgeEditor.wikiBrowser.selectPageHint') }}</p>
              <template v-else>
                <p class="wiki-empty-title">{{ $t('knowledgeEditor.wikiBrowser.emptyTitle') }}</p>
                <p class="wiki-empty-desc">{{ $t('knowledgeEditor.wikiBrowser.emptyDesc') }}</p>
              </template>
            </div>
          </div>
        </div>
      </div>
    </template>
    
    <!-- Image Preview -->
    <Teleport to="body">
      <picturePreview v-if="imagePreviewVisible" :reviewImg="imagePreviewVisible" :reviewUrl="imagePreviewUrl" @closePreImg="closeImagePreview" />
    </Teleport>
    
    <!-- Global Issues Drawer -->
    <t-drawer
      v-model:visible="showGlobalIssuesDrawer"
      :header="$t('knowledgeEditor.wikiBrowser.globalIssuesTitle')"
      size="480px"
      :footer="false"
      class="wiki-global-issues-drawer"
    >
      <div class="wiki-issue-popup-list">
        <div v-for="issue in globalIssues" :key="issue.id" class="wiki-issue-popup-item">
          <div class="wiki-issue-popup-main">
            <div class="wiki-issue-popup-tags">
              <t-tag v-if="issue.issue_type === 'mixed_entities'" theme="warning" variant="light" size="small">{{ $t('knowledgeEditor.wikiBrowser.issueMixed') }}</t-tag>
              <t-tag v-else-if="issue.issue_type === 'contradictory_facts'" theme="danger" variant="light" size="small">{{ $t('knowledgeEditor.wikiBrowser.issueConflict') }}</t-tag>
              <t-tag v-else-if="issue.issue_type === 'out_of_date'" theme="default" variant="light" size="small">{{ $t('knowledgeEditor.wikiBrowser.issueOutdated') }}</t-tag>
              <t-tag v-else theme="primary" variant="light" size="small">{{ $t('knowledgeEditor.wikiBrowser.issueAttention') }}</t-tag>
            </div>
            <div class="wiki-issue-popup-desc">
              <div style="font-weight: 500; margin-bottom: 4px; color: var(--td-brand-color); cursor: pointer;" @click="navigateToSlugAndFix(issue.slug)">
                <t-icon name="link" size="12px"/> {{ $t('knowledgeEditor.wikiBrowser.issuePagePrefix') }}{{ slugDisplayName(issue.slug) }}
              </div>
              {{ issue.description }}
            </div>
            <div class="wiki-issue-popup-meta">
              <span class="wiki-issue-popup-reporter">
                {{ issue.reported_by === 'wiki-researcher-agent' ? $t('knowledgeEditor.wikiBrowser.issueAiLinter') : $t('knowledgeEditor.wikiBrowser.issueReportedBy', { reporter: issue.reported_by }) }}
              </span>
              <div class="wiki-issue-popup-actions">
                <span class="wiki-issue-popup-action" @click="navigateToSlugAndFix(issue.slug)" style="margin-right: 12px; font-weight: 500;">
                  <t-icon name="arrow-right-circle" style="margin-right: 4px;" />{{ $t('knowledgeEditor.wikiBrowser.issueGoFix') }}
                </span>
                <span class="wiki-issue-popup-action" style="color: var(--td-text-color-placeholder);" @click="handleGlobalIssueIgnore(issue.id)">{{ $t('knowledgeEditor.wikiBrowser.issueIgnore') }}</span>
              </div>
            </div>
          </div>
        </div>
        <div v-if="globalIssues.length === 0" style="padding: 40px; text-align: center; color: var(--td-text-color-placeholder);">
          {{ $t('knowledgeEditor.wikiBrowser.globalIssuesEmpty') }}
        </div>
      </div>
    </t-drawer>

    <!-- Fix Chat Drawer -->
    <t-drawer
      v-model:visible="showFixDrawer"
      :header="$t('knowledgeEditor.wikiBrowser.fixAssistantTitle')"
      size="700px"
      :footer="false"
      class="wiki-fix-drawer"
    >
      <ChatView 
        v-if="showFixDrawer"
        :session_id="currentFixSessionId" 
        agentId="builtin-wiki-fixer" 
        :kbIds="[props.knowledgeBaseId]"
        :embeddedMode="true"
      />
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick, reactive } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useMenuStore } from '@/stores/menu'
import { useSettingsStore } from '@/stores/settings'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import { MessagePlugin } from 'tdesign-vue-next'
import { hydrateProtectedFileImages } from '@/utils/security'
import picturePreview from '@/components/picture-preview.vue'
import { createSessions } from '@/api/chat'
import ChatView from '@/views/chat/index.vue'
import {
  listWikiPages,
  getWikiPage,
  getWikiGraph,
  getWikiStats,
  searchWikiPages,
  listWikiIssues,
  updateWikiIssueStatus,
  type WikiPage,
  type WikiGraphData,
  type WikiStats,
  type WikiPageIssue,
} from '@/api/wiki'

const router = useRouter()
const route = useRoute()
const menuStore = useMenuStore()
const settingsStore = useSettingsStore()

const { t } = useI18n()

const props = defineProps<{
  knowledgeBaseId: string
  view?: 'browser' | 'graph'
}>()

const emit = defineEmits<{
  (e: 'open-source-doc', knowledgeId: string): void
  (e: 'status-change', payload: { pendingTasks: number; isActive: boolean; pendingIssues: number }): void
}>()
const pages = ref<WikiPage[]>([])
const selectedPage = ref<WikiPage | null>(null)
const pageIssues = ref<WikiPageIssue[]>([])
const showIssuesBox = ref(false)
const showFixDrawer = ref(false)
const showGlobalIssuesDrawer = ref(false)
const globalIssues = ref<WikiPageIssue[]>([])
const currentFixSessionId = ref('')
const stats = ref<WikiStats | null>(null)
const graphData = ref<WikiGraphData | null>(null)
const searchQuery = ref('')
const graphSearchValue = ref('')
const graphRef = ref<HTMLElement | null>(null)
const readerBodyRef = ref<HTMLElement | null>(null)
const drawerBodyRef = ref<HTMLElement | null>(null)
const loading = ref(false)
const graphLoading = ref(false)
const graphReady = ref(false)
const showArrows = ref(true)

// Graph filtering
const graphFilterTypes = ref<Set<string>>(new Set(['summary', 'entity', 'concept', 'synthesis', 'comparison', 'index', 'log']))

watch(showGlobalIssuesDrawer, async (val) => {
  if (val) {
    try {
      const res = await listWikiIssues(props.knowledgeBaseId, '', 'pending')
      globalIssues.value = (res as any).data || res as any || []
    } catch (e) {
      console.error('Failed to load global wiki issues:', e)
      globalIssues.value = []
    }
  }
})

async function navigateToSlugAndFix(slug: string) {
  showGlobalIssuesDrawer.value = false
  if (props.view === 'graph') {
    handleGraphSearchSelect(slug)
  } else {
    await navigateToSlug(slug)
    showIssuesBox.value = true
  }
}

async function handleGlobalIssueIgnore(issueId: string) {
  try {
    await updateWikiIssueStatus(props.knowledgeBaseId, issueId, 'ignored')
    // Refresh list
    const res = await listWikiIssues(props.knowledgeBaseId, '', 'pending')
    globalIssues.value = (res as any).data || res as any || []
    loadStats()
  } catch (e) {
    console.error('Failed to update issue status:', e)
  }
}

function toggleGraphFilterType(type: string) {
  const newSet = new Set(graphFilterTypes.value)
  if (newSet.has(type)) {
    newSet.delete(type)
  } else {
    newSet.add(type)
  }
  graphFilterTypes.value = newSet
  applyGraphFilters()
}

function applyGraphFilters() {
  if (!graphReady.value) return
  
  // Build a map for O(1) lookups
  const nodeMap = new Map()
  for (const n of graphNodes) {
    nodeMap.set(n.slug, n)
  }

  // Only show nodes whose type is in the active filter set
  for (const { g, node } of graphNodeElsRef) {
    if (graphFilterTypes.value.has(node.type)) {
      g.style.display = ''
    } else {
      g.style.display = 'none'
    }
  }
  
  // Only show edges where BOTH source and target are visible
  for (const { line, source, target } of graphEdgeElsRef) {
    const sNode = nodeMap.get(source)
    const tNode = nodeMap.get(target)
    
    if (sNode && tNode && graphFilterTypes.value.has(sNode.type) && graphFilterTypes.value.has(tNode.type)) {
      line.style.display = ''
    } else {
      line.style.display = 'none'
    }
  }
  
  // Clear any existing highlight when filtering changes
  if (graphHighlightSlug.value || graphSelectedSlug.value) {
    const selectedStillVisible = graphSelectedSlug.value && 
      graphFilterTypes.value.has(nodeMap.get(graphSelectedSlug.value)?.type || '')
      
    if (!selectedStillVisible) {
      graphSelectedSlug.value = null
      graphHighlightSlug.value = null
      graphDrawerVisible.value = false
    }
    clearHighlight(graphNodeElsRef, graphEdgeElsRef)
  }
}

// Fit graph to view
function fitGraphToView() {
  if (!graphReady.value || !graphPanZoomRef || !graphRef.value || graphNodes.length === 0) return
  
  const container = graphRef.value
  const width = container.clientWidth
  const height = container.clientHeight
  
  // Find bounding box of all VISIBLE nodes
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity
  let visibleCount = 0
  
  for (const node of graphNodes) {
    if (!graphFilterTypes.value.has(node.type)) continue
    
    minX = Math.min(minX, node.x)
    minY = Math.min(minY, node.y)
    maxX = Math.max(maxX, node.x)
    maxY = Math.max(maxY, node.y)
    visibleCount++
  }
  
  if (visibleCount === 0) return // No visible nodes
  
  // Calculate center of the bounding box
  const cx = (minX + maxX) / 2
  const cy = (minY + maxY) / 2
  
  // Calculate scale to fit the bounding box (with some padding)
  const padding = 60
  const boxWidth = Math.max(maxX - minX, 100) + padding * 2
  const boxHeight = Math.max(maxY - minY, 100) + padding * 2
  
  const scaleX = width / boxWidth
  const scaleY = height / boxHeight
  const targetScale = Math.max(0.2, Math.min(2, Math.min(scaleX, scaleY))) // Limit scale between 0.2 and 2
  
  // Offset center if drawer is open
  const targetCx = width / 2 - (graphDrawerVisible.value ? 240 : 0)
  const targetCy = height / 2
  
  // Target translation
  const targetTx = targetCx - cx * targetScale
  const targetTy = targetCy - cy * targetScale
  
  graphPanZoomRef.flyTo(targetTx, targetTy, targetScale, 600)
}

const collapsedGroups = reactive<Record<string, boolean>>({})
const graphDrawerVisible = ref(false)
const graphDrawerPage = ref<WikiPage | null>(null)
const navHistory = ref<WikiPage[]>([])
// Index and log pages (pinned at top)
const indexPage = computed(() => pages.value.find(p => p.page_type === 'index'))
const logPage = computed(() => pages.value.find(p => p.page_type === 'log'))

// Filter out system pages (index, log) for the grouped list
const contentPages = computed(() =>
  pages.value.filter(p => p.page_type !== 'index' && p.page_type !== 'log')
)

// Group pages by type for display
const typeOrder = ['summary', 'entity', 'concept', 'synthesis', 'comparison']

const groupedPages = computed(() => {
  const groups: { type: string; label: string; pages: WikiPage[] }[] = []
  const byType = new Map<string, WikiPage[]>()

  for (const page of contentPages.value) {
    const arr = byType.get(page.page_type) || []
    arr.push(page)
    byType.set(page.page_type, arr)
  }

  for (const type of typeOrder) {
    const pages = byType.get(type)
    if (pages && pages.length > 0) {
      groups.push({ type, label: getTypeLabel(type), pages })
    }
  }

  // Any remaining types not in typeOrder
  for (const [type, pages] of byType) {
    if (!typeOrder.includes(type) && pages.length > 0) {
      groups.push({ type, label: getTypeLabel(type), pages })
    }
  }

  return groups
})

// Parse source refs in "id|title" format
const parsedSourceRefs = computed(() => {
  if (!selectedPage.value?.source_refs?.length) return []
  return selectedPage.value.source_refs.map(ref => {
    const pipeIdx = ref.indexOf('|')
    if (pipeIdx > 0) {
      return { id: ref.substring(0, pipeIdx), title: ref.substring(pipeIdx + 1) }
    }
    // Fallback: show raw ref (backwards compat with old data)
    return { id: ref, title: ref.length > 20 ? ref.substring(0, 8) + '...' : ref }
  })
})

// Rendered content for graph drawer
const graphDrawerContent = computed(() => {
  if (!graphDrawerPage.value) return ''
  return renderMarkdown(graphDrawerPage.value.content)
})

const imagePreviewVisible = ref(false)
const imagePreviewUrl = ref('')

function closeImagePreview() {
  imagePreviewVisible.value = false
  imagePreviewUrl.value = ''
}

watch(graphDrawerContent, async () => {
  await nextTick()
  if (drawerBodyRef.value) {
    await hydrateProtectedFileImages(drawerBodyRef.value)
  }
})

function renderMarkdown(content: string): string {
  // Pre-process wiki links [[slug|name]] to custom HTML tags
  let preprocessed = content.replace(/\[\[([^\]]+)\]\]/g, (_, inner: string) => {
    const pipeIdx = inner.indexOf('|')
    const slug = pipeIdx > 0 ? inner.substring(0, pipeIdx).trim() : inner.trim()
    const display = pipeIdx > 0 ? inner.substring(pipeIdx + 1).trim() : slugDisplayName(slug)
    return `<a href="#" class="wiki-content-link" data-slug="${slug}">${display}</a>`
  })

  // Use marked to render the markdown to HTML
  return marked.parse(preprocessed, { breaks: true, async: false }) as string
}

async function openGraphDrawer(slug: string) {
  try {
    const res = await getWikiPage(props.knowledgeBaseId, slug)
    graphDrawerPage.value = (res as any).data || res as any
    graphDrawerVisible.value = true
  } catch (e) {
    console.error(`Failed to load page ${slug}:`, e)
  }
}

function handleGraphDrawerClick(e: MouseEvent) {
  const target = e.target as HTMLElement
  if (target.classList.contains('wiki-content-link')) {
    e.preventDefault()
    const slug = target.getAttribute('data-slug')
    if (slug) handleGraphSearchSelect(slug)
  } else if (target.tagName.toLowerCase() === 'img') {
    e.preventDefault()
    imagePreviewUrl.value = target.getAttribute('src') || ''
    if (imagePreviewUrl.value) {
      imagePreviewVisible.value = true
    }
  }
}

function toggleGroup(type: string) {
  collapsedGroups[type] = !collapsedGroups[type]
}

function getTypeTheme(type: string): string {
  const map: Record<string, string> = {
    summary: 'primary', entity: 'success', concept: 'warning',
    synthesis: 'primary', comparison: 'danger', index: 'default', log: 'default',
  }
  return map[type] || 'default'
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
  }
  return map[type] || type
}

const renderedContent = computed(() => {
  if (!selectedPage.value) return ''
  return renderMarkdown(selectedPage.value.content)
})

watch(renderedContent, async () => {
  await nextTick()
  if (readerBodyRef.value) {
    await hydrateProtectedFileImages(readerBodyRef.value)
  }
})

function handleContentClick(e: MouseEvent) {
  const target = e.target as HTMLElement
  if (target.classList.contains('wiki-content-link')) {
    e.preventDefault()
    const slug = target.getAttribute('data-slug')
    if (slug) navigateToSlug(slug)
  } else if (target.tagName.toLowerCase() === 'img') {
    e.preventDefault()
    imagePreviewUrl.value = target.getAttribute('src') || ''
    if (imagePreviewUrl.value) {
      imagePreviewVisible.value = true
    }
  }
}

async function loadPages() {
  loading.value = true
  try {
    const PAGE_SIZE = 500
    const MAX_PAGES = 50 // safety cap: up to 25k pages
    const collected: WikiPage[] = []
    let page = 1
    let totalPages = 1
    while (page <= totalPages && page <= MAX_PAGES) {
      const res = await listWikiPages(props.knowledgeBaseId, { page, page_size: PAGE_SIZE })
      const body = (res as any).data || res
      const batch: WikiPage[] = body?.pages || []
      collected.push(...batch)
      const reportedTotalPages = Number(body?.total_pages) || 0
      if (reportedTotalPages > 0) {
        totalPages = reportedTotalPages
      } else if (batch.length < PAGE_SIZE) {
        break
      } else {
        totalPages = page + 1
      }
      page++
    }
    pages.value = collected
    // Auto-select based on query or index page
    if (!selectedPage.value) {
      if (route.query.slug && typeof route.query.slug === 'string') {
        navigateToSlug(route.query.slug)
      } else if (indexPage.value) {
        selectPage(indexPage.value)
      }
    }
  } catch (e) {
    console.error('Failed to load wiki pages:', e)
  } finally {
    loading.value = false
  }
}

let statsTimer: ReturnType<typeof setInterval> | null = null

async function loadStats() {
  try {
    const res = await getWikiStats(props.knowledgeBaseId)
    stats.value = (res as any).data || res as any

    // Notify parent so it can reflect wiki status (e.g. indexing badge in the breadcrumb)
    if (stats.value) {
      emit('status-change', {
        pendingTasks: stats.value.pending_tasks || 0,
        isActive: !!stats.value.is_active,
        pendingIssues: stats.value.pending_issues || 0,
      })
    }

    // Poll if there are pending tasks or wiki ingest is active
    if (stats.value && (stats.value.pending_tasks > 0 || stats.value.is_active)) {
      if (!statsTimer) {
        statsTimer = setInterval(() => {
          loadStats()
        }, 5000)
      }
    } else if (statsTimer) {
      // If completed, clear timer and reload pages once to get new content
      clearInterval(statsTimer)
      statsTimer = null
      loadPages()
      // Also refresh the currently opened page (right panel) so users see updated content
      refreshSelectedPage()
      // If currently viewing the graph, reload it as well so new nodes/edges show up
      if (props.view === 'graph') {
        loadGraph()
      }
    }
  } catch (e) { /* ignore */ }
}

// Refresh the currently selected page's content without touching navigation history
async function refreshSelectedPage() {
  if (!selectedPage.value) return
  const slug = selectedPage.value.slug
  try {
    const res = await getWikiPage(props.knowledgeBaseId, slug)
    selectedPage.value = (res as any).data || res as any
    await loadPageIssues(slug)
  } catch (e) {
    console.error(`Failed to refresh wiki page ${slug}:`, e)
  }
}

async function loadGraph() {
  graphLoading.value = true
  graphReady.value = false
  try {
    const res = await getWikiGraph(props.knowledgeBaseId)
    graphData.value = (res as any).data || res as any
    await nextTick()
    renderGraph()
    if (route.query.slug && typeof route.query.slug === 'string') {
      graphSelectedSlug.value = null // reset first to ensure watch triggers
      setTimeout(() => {
        handleGraphSearchSelect(route.query.slug as string)
      }, 300)
    }
  } catch (e) {
    console.error('Failed to load graph:', e)
  } finally {
    graphLoading.value = false
  }
}

async function loadPageIssues(slug: string) {
  try {
    const res = await listWikiIssues(props.knowledgeBaseId, slug, 'pending')
    pageIssues.value = (res as any).data || res as any || []
    showIssuesBox.value = false
  } catch (e) {
    console.error('Failed to load wiki issues:', e)
    pageIssues.value = []
    showIssuesBox.value = false
  }
}

async function selectPage(page: WikiPage) {
  try {
    if (selectedPage.value && selectedPage.value.id !== page.id) {
      navHistory.value.push(selectedPage.value)
    }
    const res = await getWikiPage(props.knowledgeBaseId, page.slug)
    selectedPage.value = (res as any).data || res as any
    await loadPageIssues(page.slug)
  } catch (e) {
    console.error('Failed to load wiki page:', e)
  }
}

async function navigateToSlug(slug: string) {
  try {
    if (selectedPage.value && selectedPage.value.slug !== slug) {
      navHistory.value.push(selectedPage.value)
    }
    const res = await getWikiPage(props.knowledgeBaseId, slug)
    selectedPage.value = (res as any).data || res as any
    await loadPageIssues(slug)
  } catch (e) {
    console.error(`Failed to navigate to ${slug}:`, e)
  }
}

function goBack() {
  const prev = navHistory.value.pop()
  if (prev) {
    selectedPage.value = prev
    loadPageIssues(prev.slug)
  }
}

async function handleIssueIgnore(issueId: string) {
  try {
    await updateWikiIssueStatus(props.knowledgeBaseId, issueId, 'ignored')
    if (selectedPage.value) {
      await loadPageIssues(selectedPage.value.slug)
    }
  } catch (e) {
    console.error('Failed to update issue status:', e)
  }
}

async function startFixSession(prompt: string) {
  try {
    const res = await createSessions({})
    if (res && (res as any).data && (res as any).data.id) {
      const sessionId = (res as any).data.id
      const now = new Date().toISOString()
      
      menuStore.updataMenuChildren({
        title: t('knowledgeEditor.wikiBrowser.fixAssistantTitle'),
        path: `chat/${sessionId}`,
        id: sessionId,
        isMore: false,
        isNoTitle: true,
        created_at: now,
        updated_at: now
      })
      
      menuStore.changeIsFirstSession(true)
      menuStore.changeFirstQuery(prompt, [], '', [])
      
      currentFixSessionId.value = sessionId
      showFixDrawer.value = true
      showIssuesBox.value = false // Hide issues box
    } else {
      MessagePlugin.error(t('knowledgeEditor.wikiBrowser.fixStartError'))
    }
  } catch (e) {
    console.error('Failed to create fix session', e)
    MessagePlugin.error(t('knowledgeEditor.wikiBrowser.fixStartError'))
  }
}

function triggerFixIssue(issue: WikiPageIssue) {
  if (!selectedPage.value) return
  const prompt = t('knowledgeEditor.wikiBrowser.issueFixPromptSingle', {
    slug: selectedPage.value.slug,
    id: issue.id
  })
  startFixSession(prompt)
}

function triggerAutoFix() {
  if (!selectedPage.value || pageIssues.value.length === 0) return
  let prompt = t('knowledgeEditor.wikiBrowser.issueFixPromptAutoStart', { slug: selectedPage.value.slug }) + '\n\n'
  
  pageIssues.value.forEach((issue, idx) => {
    prompt += `${idx + 1}. Issue ID: ${issue.id}\n`
  })
  
  startFixSession(prompt)
}

async function doSearch() {
  if (!searchQuery.value.trim()) { loadPages(); return }
  loading.value = true
  try {
    const res = await searchWikiPages(props.knowledgeBaseId, searchQuery.value)
    pages.value = (res as any).data?.pages || (res as any).pages || []
  } catch (e) { console.error('Wiki search failed:', e) }
  finally { loading.value = false }
}

function toggleArrows() {
  showArrows.value = !showArrows.value
  for (const e of graphEdgeElsRef) {
    if (showArrows.value) {
      e.line.setAttribute('marker-end', 'url(#arrow-end)')
      if (e.bidir) e.line.setAttribute('marker-start', 'url(#arrow-start)')
    } else {
      e.line.removeAttribute('marker-end')
      e.line.removeAttribute('marker-start')
    }
  }
}

function formatDate(dateStr: string) {
  if (!dateStr) return ''
  const d = new Date(dateStr)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${pad(d.getMonth() + 1)}/${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

// Convert slug like "entity/acme-corp" to a readable label "acme-corp"
function slugDisplayName(slug: string): string {
  // Find the page title if loaded
  const page = pages.value.find(p => p.slug === slug)
  if (page) return page.title
  // Fallback: strip type prefix, replace hyphens
  const parts = slug.split('/')
  return parts.length > 1 ? parts.slice(1).join('/') : slug
}

// ─── Graph Rendering (interactive SVG force-directed graph) ───
// Features: drag nodes, pan canvas, zoom, hover highlight, click to open drawer, legend

interface GNode {
  x: number; y: number; vx: number; vy: number
  slug: string; title: string; type: string
  linkCount: number; pinned: boolean
}

// Persistent graph state so it survives re-renders
let graphNodes: GNode[] = []
let graphSvg: SVGSVGElement | null = null
let graphAnimFrame = 0
// Shared timer for debouncing node mouseleave -> mouseenter transitions.
// Prevents flickering when the pointer quickly moves between adjacent nodes.
let graphHoverLeaveTimer: ReturnType<typeof setTimeout> | null = null

// Used for graph search centering interaction
let graphPanZoomRef: {
  setScale: (s: number) => void,
  setTranslate: (x: number, y: number) => void,
  apply: () => void,
  flyTo: (x: number, y: number, s?: number, duration?: number) => void,
  getScale: () => number
} | null = null

const graphHighlightSlug = ref<string | null>(null)
const graphSelectedSlug = ref<string | null>(null)

// Color map for node types
const nodeColorMap: Record<string, string> = {
  summary: '#0052d9', entity: '#2ba471', concept: '#e37318',
  synthesis: '#0594fa', comparison: '#d54941', index: '#8c8c8c', log: '#8c8c8c',
}

function renderGraph() {
  const container = graphRef.value
  const data = graphData.value
  if (!container) return
  if (!data || !data.nodes?.length) {
    container.innerHTML = ''
    return
  }

  // Stop any previous animation
  if (graphAnimFrame) { cancelAnimationFrame(graphAnimFrame); graphAnimFrame = 0 }
  if (graphHoverLeaveTimer) { clearTimeout(graphHoverLeaveTimer); graphHoverLeaveTimer = null }

  const width = container.clientWidth || 800
  const height = container.clientHeight || 600

  // Create SVG
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
  svg.setAttribute('viewBox', `0 0 ${width} ${height}`)
  svg.style.width = '100%'
  svg.style.height = '100%'
  container.innerHTML = ''
  container.appendChild(svg)
  graphSvg = svg

  // Root group for pan/zoom transform
  const rootG = document.createElementNS('http://www.w3.org/2000/svg', 'g')
  rootG.setAttribute('class', 'graph-root')
  svg.appendChild(rootG)

  // Edge group (below nodes)
  const edgeG = document.createElementNS('http://www.w3.org/2000/svg', 'g')
  rootG.appendChild(edgeG)

  // Node group (above edges)
  const nodeG = document.createElementNS('http://www.w3.org/2000/svg', 'g')
  rootG.appendChild(nodeG)

  // Build adjacency for highlight
  const adjacency = new Map<string, Set<string>>()
  for (const edge of data.edges) {
    if (!adjacency.has(edge.source)) adjacency.set(edge.source, new Set())
    if (!adjacency.has(edge.target)) adjacency.set(edge.target, new Set())
    adjacency.get(edge.source)!.add(edge.target)
    adjacency.get(edge.target)!.add(edge.source)
  }

  // Build nodes
  const nodeMap = new Map<string, GNode>()
  graphNodes = data.nodes.map((n, i) => {
    const angle = (2 * Math.PI * i) / data.nodes.length
    const r = Math.min(width, height) * 0.35
    const node: GNode = {
      x: width / 2 + r * Math.cos(angle) + (Math.random() - 0.5) * 50,
      y: height / 2 + r * Math.sin(angle) + (Math.random() - 0.5) * 50,
      vx: 0, vy: 0,
      slug: n.slug, title: n.title, type: n.page_type,
      linkCount: n.link_count || 0, pinned: false,
    }
    nodeMap.set(n.slug, node)
    return node
  })

  // Node radius based on link count (logarithmic scale to prevent overly large nodes)
  function nodeRadius(n: GNode) { 
    return Math.max(8, Math.min(24, 8 + Math.log(n.linkCount + 1) * 4)) 
  }

  // Define arrow markers in SVG <defs>
  const defs = document.createElementNS('http://www.w3.org/2000/svg', 'defs')

  // Single-direction arrow (at end)
  const markerEnd = document.createElementNS('http://www.w3.org/2000/svg', 'marker')
  markerEnd.setAttribute('id', 'arrow-end')
  markerEnd.setAttribute('viewBox', '0 0 10 6')
  markerEnd.setAttribute('refX', '10')
  markerEnd.setAttribute('refY', '3')
  markerEnd.setAttribute('markerWidth', '8')
  markerEnd.setAttribute('markerHeight', '6')
  markerEnd.setAttribute('orient', 'auto')
  const arrowPath = document.createElementNS('http://www.w3.org/2000/svg', 'path')
  arrowPath.setAttribute('d', 'M0,0 L10,3 L0,6 L2,3 Z')
  arrowPath.setAttribute('fill', '#c0c4cc')
  markerEnd.appendChild(arrowPath)
  defs.appendChild(markerEnd)

  // Bidirectional: arrow at start (reverse)
  const markerStart = document.createElementNS('http://www.w3.org/2000/svg', 'marker')
  markerStart.setAttribute('id', 'arrow-start')
  markerStart.setAttribute('viewBox', '0 0 10 6')
  markerStart.setAttribute('refX', '0')
  markerStart.setAttribute('refY', '3')
  markerStart.setAttribute('markerWidth', '8')
  markerStart.setAttribute('markerHeight', '6')
  markerStart.setAttribute('orient', 'auto')
  const arrowPathStart = document.createElementNS('http://www.w3.org/2000/svg', 'path')
  arrowPathStart.setAttribute('d', 'M10,0 L0,3 L10,6 L8,3 Z')
  arrowPathStart.setAttribute('fill', '#c0c4cc')
  markerStart.appendChild(arrowPathStart)
  defs.appendChild(markerStart)

  // Highlighted arrows
  for (const id of ['arrow-end-hl', 'arrow-start-hl']) {
    const m = document.createElementNS('http://www.w3.org/2000/svg', 'marker')
    m.setAttribute('id', id)
    m.setAttribute('viewBox', '0 0 10 6')
    m.setAttribute('refX', id.includes('end') ? '10' : '0')
    m.setAttribute('refY', '3')
    m.setAttribute('markerWidth', '8')
    m.setAttribute('markerHeight', '6')
    m.setAttribute('orient', 'auto')
    const p = document.createElementNS('http://www.w3.org/2000/svg', 'path')
    p.setAttribute('d', id.includes('end') ? 'M0,0 L10,3 L0,6 L2,3 Z' : 'M10,0 L0,3 L10,6 L8,3 Z')
    p.setAttribute('fill', '#0052d9')
    m.appendChild(p)
    defs.appendChild(m)
  }

  // Drop shadow filter for nodes
  const filter = document.createElementNS('http://www.w3.org/2000/svg', 'filter')
  filter.setAttribute('id', 'node-shadow')
  filter.setAttribute('x', '-20%')
  filter.setAttribute('y', '-20%')
  filter.setAttribute('width', '140%')
  filter.setAttribute('height', '140%')
  filter.innerHTML = `<feDropShadow dx="0" dy="2" stdDeviation="3" flood-color="#000" flood-opacity="0.15"/>`
  defs.appendChild(filter)

  svg.appendChild(defs)

  // Detect bidirectional edges (A→B and B→A both exist)
  const edgePairSet = new Set<string>()
  for (const edge of data.edges) {
    edgePairSet.add(`${edge.source}→${edge.target}`)
  }

  // Create SVG elements for edges (deduplicate bidirectional into single line with double arrows)
  type EdgeEl = { line: SVGLineElement; source: string; target: string; bidir: boolean }
  const edgeEls: EdgeEl[] = []
  const processedPairs = new Set<string>()

  for (const edge of data.edges) {
    const pairKey = [edge.source, edge.target].sort().join('↔')
    if (processedPairs.has(pairKey)) continue
    processedPairs.add(pairKey)

    const bidir = edgePairSet.has(`${edge.target}→${edge.source}`)

    const line = document.createElementNS('http://www.w3.org/2000/svg', 'line')
    line.setAttribute('stroke', '#c0c4cc')
    line.setAttribute('stroke-width', '1.2')
    line.setAttribute('stroke-opacity', '0.4')
    line.setAttribute('marker-end', 'url(#arrow-end)')
    line.style.transition = 'stroke 0.2s, stroke-width 0.2s, stroke-opacity 0.2s'
    if (bidir) {
      line.setAttribute('marker-start', 'url(#arrow-start)')
    }
    edgeG.appendChild(line)
    edgeEls.push({ line, source: edge.source, target: edge.target, bidir })
  }

  // Create SVG elements for nodes
  const nodeEls: { g: SVGGElement; circle: SVGCircleElement; text: SVGTextElement; activeRing: SVGCircleElement; node: GNode }[] = []
  for (const n of graphNodes) {
    const g = document.createElementNS('http://www.w3.org/2000/svg', 'g')
    g.style.cursor = 'pointer'

    const r = nodeRadius(n)

    // Pulse ring for selected state
    const activeRing = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
    activeRing.setAttribute('r', String(r + 5))
    activeRing.setAttribute('fill', 'none')
    activeRing.setAttribute('stroke', nodeColorMap[n.type] || '#8c8c8c')
    activeRing.setAttribute('stroke-width', '2')
    activeRing.style.opacity = '0'
    activeRing.style.transition = 'opacity 0.2s'
    activeRing.classList.add('node-active-ring')
    g.appendChild(activeRing)

    const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
    circle.setAttribute('r', String(r))
    circle.setAttribute('fill', nodeColorMap[n.type] || '#8c8c8c')
    circle.setAttribute('stroke', '#fff')
    circle.setAttribute('stroke-width', '2')
    // circle.setAttribute('filter', 'url(#node-shadow)')
    circle.style.transition = 'r 0.2s, stroke-width 0.2s, opacity 0.2s'
    g.appendChild(circle)

    // Text label wrapper for better readability
    const textBg = document.createElementNS('http://www.w3.org/2000/svg', 'rect')
    g.appendChild(textBg) // we'll size this after we know text size

    const text = document.createElementNS('http://www.w3.org/2000/svg', 'text')
    text.setAttribute('text-anchor', 'middle')
    text.setAttribute('dy', String(r + 14))
    text.setAttribute('font-size', '11')
    text.setAttribute('fill', 'var(--td-text-color-secondary)')
    text.setAttribute('pointer-events', 'none')
    text.style.transition = 'opacity 0.2s' // Smooth fade in/out
    text.style.textShadow = '0 1px 3px var(--td-bg-color-container), 0 -1px 3px var(--td-bg-color-container), 1px 0 3px var(--td-bg-color-container), -1px 0 3px var(--td-bg-color-container)'
    text.textContent = n.title.length > 14 ? n.title.substring(0, 14) + '…' : n.title
    g.appendChild(text)

    // Hover highlight
    // We debounce the "leave" side so that quickly sliding the pointer from
    // one node to the next doesn't flash through the fully-unhighlighted state
    // (which is what caused the whole-graph flickering).
    g.addEventListener('mouseenter', () => {
      if (graphHoverLeaveTimer) {
        clearTimeout(graphHoverLeaveTimer)
        graphHoverLeaveTimer = null
      }
      if (!graphSelectedSlug.value) {
        if (graphHighlightSlug.value === n.slug) return
        graphHighlightSlug.value = n.slug
        applyHighlight(n.slug, adjacency, nodeEls, edgeEls)
      } else if (graphSelectedSlug.value !== n.slug) {
        if (graphHighlightSlug.value === n.slug) return
        graphHighlightSlug.value = n.slug
        applyHighlight(graphSelectedSlug.value, adjacency, nodeEls, edgeEls, n.slug)
      }
    })
    g.addEventListener('mouseleave', () => {
      if (graphHoverLeaveTimer) clearTimeout(graphHoverLeaveTimer)
      graphHoverLeaveTimer = setTimeout(() => {
        graphHoverLeaveTimer = null
        if (!graphSelectedSlug.value) {
          graphHighlightSlug.value = null
          clearHighlight(nodeEls, edgeEls)
        } else {
          graphHighlightSlug.value = null
          applyHighlight(graphSelectedSlug.value, adjacency, nodeEls, edgeEls)
        }
      }, 60)
    })

    // Click to select & open drawer directly
    g.addEventListener('click', (e) => {
      e.stopPropagation()
      
      // Select and highlight
      graphSelectedSlug.value = n.slug
      applyHighlight(n.slug, adjacency, nodeEls, edgeEls)
      
      // Auto pan to center the node, shifted left for drawer
      if (graphPanZoomRef) {
        const container = graphRef.value
        if (container) {
          const width = container.clientWidth
          const height = container.clientHeight
          graphPanZoomRef.flyTo(
            width / 2 - n.x * graphPanZoomRef.getScale() - 240,
            height / 2 - n.y * graphPanZoomRef.getScale()
          )
        }
      }
      
      // Open drawer (it will handle drawer visibility and fetching content)
      openGraphDrawer(n.slug)
    })

    // Drag support
    setupDrag(g, n, nodeMap, edgeEls, nodeEls, nodeRadius)

    nodeG.appendChild(g)
    nodeEls.push({ g, circle, text, activeRing, node: n })
  }

  // Pan & zoom on SVG background
  setupPanZoom(svg, rootG)

  // Animated force simulation
  let alpha = 1.0
  function tick() {
    alpha *= 0.985
    if (alpha < 0.02) { graphAnimFrame = 0; return }

    // Repulsion: Optimized using 1D spatial sorting (X-axis) to reduce O(n²) to O(n log n)
    // This allows smooth rendering even for > 1000 nodes
    const sortedNodes = [...graphNodes].sort((a, b) => a.x - b.x)
    const MAX_REPULSION_DIST = 300 // Only calculate repulsion for nodes within 300px
    const MAX_REPULSION_DIST_SQ = MAX_REPULSION_DIST * MAX_REPULSION_DIST

    for (let i = 0; i < sortedNodes.length; i++) {
      const n1 = sortedNodes[i]
      for (let j = i + 1; j < sortedNodes.length; j++) {
        const n2 = sortedNodes[j]
        const dx = n2.x - n1.x
        
        // Because nodes are sorted by X, if dx > MAX_REPULSION_DIST, 
        // all subsequent n2 nodes will also be too far on the X axis, so we can break early
        if (dx > MAX_REPULSION_DIST) break
        
        const dy = n2.y - n1.y
        if (Math.abs(dy) > MAX_REPULSION_DIST) continue // Too far on Y axis
        
        const distSq = dx * dx + dy * dy
        if (distSq > MAX_REPULSION_DIST_SQ) continue

        const dist = Math.sqrt(distSq) || 1
        // Prevent extremely high repulsion when nodes are very close
        const force = (200 * alpha) / Math.max(distSq, 100) * 60
        const fx = (dx / dist) * force
        const fy = (dy / dist) * force
        
        if (!n1.pinned) { n1.vx -= fx; n1.vy -= fy }
        if (!n2.pinned) { n2.vx += fx; n2.vy += fy }
      }
    }

    // Attraction along edges
    for (const edge of data.edges) {
      const s = nodeMap.get(edge.source)
      const t = nodeMap.get(edge.target)
      if (!s || !t) continue
      const dx = t.x - s.x
      const dy = t.y - s.y
      const dist = Math.sqrt(dx * dx + dy * dy) || 1
      const force = (dist - 120) * 0.005 * alpha
      const fx = (dx / dist) * force
      const fy = (dy / dist) * force
      if (!s.pinned) { s.vx += fx; s.vy += fy }
      if (!t.pinned) { t.vx -= fx; t.vy -= fy }
    }

    // Center gravity
    // Increase gravity slightly when there are more nodes to prevent the graph from expanding too much
    const gravityStrength = Math.min(0.01, 0.001 + graphNodes.length * 0.00002)
    for (const n of graphNodes) {
      if (n.pinned) continue
      n.vx += (width / 2 - n.x) * gravityStrength * alpha
      n.vy += (height / 2 - n.y) * gravityStrength * alpha
    }

    // Apply velocity
    for (const n of graphNodes) {
      if (n.pinned) continue
      n.vx *= 0.6
      n.vy *= 0.6
      // Cap velocity to prevent nodes from flying off screen during initial explosive layout
      const v = Math.sqrt(n.vx * n.vx + n.vy * n.vy)
      if (v > 20) {
        n.vx = (n.vx / v) * 20
        n.vy = (n.vy / v) * 20
      }
      n.x += n.vx
      n.y += n.vy
    }

    // Update SVG positions
    for (const { g, node } of nodeEls) {
      g.setAttribute('transform', `translate(${node.x},${node.y})`)
    }
    for (const e of edgeEls) {
      const s = nodeMap.get(e.source)
      const t = nodeMap.get(e.target)
      if (s && t) {
        setEdgePositions(e.line, s, t, nodeRadius)
      }
    }

    graphAnimFrame = requestAnimationFrame(tick)
  }

  // Initial positions before first paint
  for (const { g, node } of nodeEls) {
    g.setAttribute('transform', `translate(${node.x},${node.y})`)
  }
  for (const e of edgeEls) {
    const s = nodeMap.get(e.source)
    const t = nodeMap.get(e.target)
    if (s && t) {
      setEdgePositions(e.line, s, t, nodeRadius)
    }
  }

  // Store node and edge refs for search and arrow toggle
  graphNodeElsRef = nodeEls
  graphEdgeElsRef = edgeEls.map(e => ({ line: e.line, source: e.source, target: e.target, bidir: e.bidir }))
  graphAdjacencyRef = adjacency
  
  applyGraphFilters()
  
  graphAnimFrame = requestAnimationFrame(tick)
  graphReady.value = true
}

// Set edge line positions, shortened to stop at node circle boundary so arrows are visible
function setEdgePositions(line: SVGLineElement, s: GNode, t: GNode, nodeRadius: (n: GNode) => number) {
  const dx = t.x - s.x
  const dy = t.y - s.y
  const dist = Math.sqrt(dx * dx + dy * dy) || 1
  const ux = dx / dist
  const uy = dy / dist

  // Shorten each end by the node radius + arrow margin
  const rS = nodeRadius(s) + 4
  const rT = nodeRadius(t) + 4

  line.setAttribute('x1', String(s.x + ux * rS))
  line.setAttribute('y1', String(s.y + uy * rS))
  line.setAttribute('x2', String(t.x - ux * rT))
  line.setAttribute('y2', String(t.y - uy * rT))
}

// ─── Drag ───
function setupDrag(
  g: SVGGElement, node: GNode,
  nodeMap: Map<string, GNode>,
  edgeEls: { line: SVGLineElement; source: string; target: string; bidir: boolean }[],
  nodeEls: { g: SVGGElement; circle: SVGCircleElement; text: SVGTextElement; activeRing: SVGCircleElement; node: GNode }[],
  nodeRadius: (n: GNode) => number,
) {
  let dragging = false
  let startX = 0, startY = 0

  function getPoint(e: MouseEvent | Touch) {
    const svg = graphSvg
    if (!svg) return { x: e.clientX, y: e.clientY }
    const pt = svg.createSVGPoint()
    pt.x = e.clientX; pt.y = e.clientY
    const rootG = svg.querySelector('.graph-root') as SVGGElement
    const ctm = rootG?.getCTM()?.inverse()
    if (ctm) {
      const svgP = pt.matrixTransform(ctm)
      return { x: svgP.x, y: svgP.y }
    }
    return { x: e.clientX, y: e.clientY }
  }

  function onStart(e: MouseEvent) {
    if (e.button !== 0) return
    e.stopPropagation()
    dragging = true
    node.pinned = true
    const p = getPoint(e)
    startX = p.x - node.x
    startY = p.y - node.y
    g.querySelector('circle')?.setAttribute('stroke', nodeColorMap[node.type] || '#8c8c8c')
    g.querySelector('circle')?.setAttribute('stroke-width', '3')
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onEnd)
  }

  function onMove(e: MouseEvent) {
    if (!dragging) return
    const p = getPoint(e)
    node.x = p.x - startX
    node.y = p.y - startY
    node.vx = 0; node.vy = 0
    g.setAttribute('transform', `translate(${node.x},${node.y})`)
    // Update connected edges immediately
    for (const edge of edgeEls) {
      if (edge.source === node.slug || edge.target === node.slug) {
        const sn = nodeMap.get(edge.source)
        const tn = nodeMap.get(edge.target)
        if (sn && tn) setEdgePositions(edge.line, sn, tn, nodeRadius)
      }
    }
  }

  function onEnd() {
    dragging = false
    // Keep pinned after drag so the node stays where user placed it
    g.querySelector('circle')?.setAttribute('stroke', '#fff')
    g.querySelector('circle')?.setAttribute('stroke-width', '2')
    window.removeEventListener('mousemove', onMove)
    window.removeEventListener('mouseup', onEnd)
  }

  g.addEventListener('mousedown', onStart)
}

// ─── Pan & Zoom ───
function setupPanZoom(svg: SVGSVGElement, rootG: SVGGElement) {
  let scale = 1
  let translateX = 0, translateY = 0
  let panning = false
  let panStartX = 0, panStartY = 0
  let dragStartX = 0, dragStartY = 0

  function applyTransform() {
    rootG.setAttribute('transform', `translate(${translateX},${translateY}) scale(${scale})`)
    updateLabelsVisibility()
  }

  function updateLabelsVisibility() {
    // Hide labels when zoomed out too much or hide less important labels
    // We only want to show labels for important nodes (high link count) when zoomed out
    for (const { text, node } of graphNodeElsRef) {
      if (node.slug === graphSelectedSlug.value || node.slug === graphHighlightSlug.value) {
        text.style.opacity = '1' // Always show selected/highlighted
        continue
      }
      
      let visibilityThreshold = 0.5 // Default: need to zoom in to at least 0.5 to see all labels
      
      // Highly connected nodes get their labels shown earlier
      if (node.linkCount > 10) visibilityThreshold = 0.2
      else if (node.linkCount > 5) visibilityThreshold = 0.35
      else if (node.linkCount > 2) visibilityThreshold = 0.45
      
      if (scale < visibilityThreshold) {
        text.style.opacity = '0'
      } else {
        text.style.opacity = '1'
      }
    }
  }

  // Export methods for programmatic pan/zoom
  let animId = 0
  graphPanZoomRef = {
    setScale: (s: number) => { scale = s },
    setTranslate: (x: number, y: number) => { translateX = x; translateY = y },
    apply: applyTransform,
    getScale: () => scale,
    flyTo: (tx: number, ty: number, s?: number, duration = 400) => {
      cancelAnimationFrame(animId)
      const startX = translateX, startY = translateY, startScale = scale
      const targetScale = s || scale
      const startTime = performance.now()
      const animate = (time: number) => {
        let t = (time - startTime) / duration
        if (t > 1) t = 1
        const ease = 1 - Math.pow(1 - t, 3) // cubic ease out
        translateX = startX + (tx - startX) * ease
        translateY = startY + (ty - startY) * ease
        scale = startScale + (targetScale - startScale) * ease
        applyTransform()
        if (t < 1) animId = requestAnimationFrame(animate)
      }
      animId = requestAnimationFrame(animate)
    }
  }

  // Zoom with mouse wheel
  svg.addEventListener('wheel', (e) => {
    e.preventDefault()
    const zoomFactor = e.deltaY > 0 ? 0.92 : 1.08
    const newScale = Math.max(0.2, Math.min(5, scale * zoomFactor))

    // Zoom towards cursor
    const rect = svg.getBoundingClientRect()
    const cx = e.clientX - rect.left
    const cy = e.clientY - rect.top
    translateX = cx - (cx - translateX) * (newScale / scale)
    translateY = cy - (cy - translateY) * (newScale / scale)
    scale = newScale
    applyTransform()
  }, { passive: false })

  // Pan with mouse drag on background
  svg.addEventListener('mousedown', (e) => {
    if (e.button !== 0) return
    // Only pan if clicking the SVG background, not a node
    if ((e.target as Element).tagName === 'svg' || (e.target as Element).tagName === 'SVG') {
      panning = true
      panStartX = e.clientX - translateX
      panStartY = e.clientY - translateY
      dragStartX = e.clientX
      dragStartY = e.clientY
      svg.style.cursor = 'grabbing'
    }
  })

  window.addEventListener('mousemove', (e) => {
    if (!panning) return
    translateX = e.clientX - panStartX
    translateY = e.clientY - panStartY
    applyTransform()
  })

  window.addEventListener('mouseup', (e) => {
    if (panning) {
      panning = false
      svg.style.cursor = 'default'
      
      // If we barely moved, consider it a click to clear selection
      const dx = e.clientX - dragStartX
      const dy = e.clientY - dragStartY
      if (Math.abs(dx) < 5 && Math.abs(dy) < 5) {
        if ((e.target as Element).tagName === 'svg' || (e.target as Element).tagName === 'SVG') {
          graphSelectedSlug.value = null
          graphDrawerVisible.value = false
          clearHighlight(graphNodeElsRef, graphEdgeElsRef)
        }
      }
    }
  })
}

// ─── Hover Highlight ───
function applyHighlight(
  slug: string,
  adjacency: Map<string, Set<string>>,
  nodeEls: { g: SVGGElement; circle: SVGCircleElement; text: SVGTextElement; activeRing: SVGCircleElement; node: GNode }[],
  edgeEls: { line: SVGLineElement; source: string; target: string; bidir: boolean }[],
  hoverSlug?: string
) {
  const neighbors = adjacency.get(slug) || new Set()
  const hoverNeighbors = hoverSlug ? (adjacency.get(hoverSlug) || new Set()) : new Set()
  
  // Helper to get consistent radius
  const getRadius = (n: GNode) => Math.max(8, Math.min(24, 8 + Math.log(n.linkCount + 1) * 4))
  
  for (const { g, circle, activeRing, node } of nodeEls) {
    const r = getRadius(node)
    if (node.slug === slug) {
      circle.setAttribute('r', String(r + 3))
      circle.setAttribute('stroke-width', '3')
      g.style.opacity = '1'
    } else if (hoverSlug && node.slug === hoverSlug) {
      circle.setAttribute('r', String(r + 3))
      circle.setAttribute('stroke-width', '3')
      g.style.opacity = '1'
    } else if (neighbors.has(node.slug) || (hoverSlug && hoverNeighbors.has(node.slug))) {
      circle.setAttribute('r', String(r))
      circle.setAttribute('stroke-width', '2')
      g.style.opacity = '1'
    } else {
      circle.setAttribute('r', String(r))
      circle.setAttribute('stroke-width', '2')
      g.style.opacity = '0.2'
    }
    
    if (node.slug === graphSelectedSlug.value) {
      activeRing.style.opacity = '1'
    } else {
      activeRing.style.opacity = '0'
    }
  }
  for (const e of edgeEls) {
    if (e.source === slug || e.target === slug || (hoverSlug && (e.source === hoverSlug || e.target === hoverSlug))) {
      e.line.setAttribute('stroke-opacity', '0.9')
      e.line.setAttribute('stroke-width', '2')
      
      // Determine which node is driving the highlight color
      const focusSlug = (hoverSlug && (e.source === hoverSlug || e.target === hoverSlug)) ? hoverSlug : slug
      const hlColor = nodeColorMap[
        nodeEls.find(n => n.node.slug === focusSlug)?.node.type || ''
      ] || '#0052d9'
      
      e.line.setAttribute('stroke', hlColor)
      e.line.setAttribute('marker-end', 'url(#arrow-end-hl)')
      if (e.bidir) e.line.setAttribute('marker-start', 'url(#arrow-start-hl)')
    } else {
      e.line.setAttribute('stroke-opacity', '0.08')
      e.line.setAttribute('stroke-width', '1')
      e.line.setAttribute('marker-end', 'url(#arrow-end)')
      if (e.bidir) e.line.setAttribute('marker-start', 'url(#arrow-start)')
      else e.line.removeAttribute('marker-start')
    }
  }
}

function clearHighlight(
  nodeEls: { g: SVGGElement; circle: SVGCircleElement; text: SVGTextElement; activeRing: SVGCircleElement; node: GNode }[],
  edgeEls: { line: SVGLineElement; source: string; target: string; bidir: boolean }[],
) {
  if (graphSelectedSlug.value) {
    applyHighlight(graphSelectedSlug.value, graphAdjacencyRef, nodeEls, edgeEls)
    return
  }

  const getRadius = (n: GNode) => Math.max(8, Math.min(24, 8 + Math.log(n.linkCount + 1) * 4))

  for (const { g, circle, activeRing, node } of nodeEls) {
    circle.setAttribute('r', String(getRadius(node)))
    circle.setAttribute('stroke-width', '2')
    g.style.opacity = '1'
    activeRing.style.opacity = '0'
  }
  for (const e of edgeEls) {
    e.line.setAttribute('stroke', '#c0c4cc')
    e.line.setAttribute('stroke-width', '1.2')
    e.line.setAttribute('stroke-opacity', '0.4')
    e.line.setAttribute('marker-end', 'url(#arrow-end)')
    if (e.bidir) e.line.setAttribute('marker-start', 'url(#arrow-start)')
    else e.line.removeAttribute('marker-start')
  }
}

const graphSearchOptions = computed(() => {
  if (!graphData.value?.nodes) return []
  return graphData.value.nodes.map(n => ({
    label: n.title,
    value: n.slug
  }))
})

let graphNodeElsRef: { g: SVGGElement; circle: SVGCircleElement; text: SVGTextElement; activeRing: SVGCircleElement; node: GNode }[] = []
let graphEdgeElsRef: { line: SVGLineElement; source: string; target: string; bidir: boolean }[] = []
let graphAdjacencyRef = new Map<string, Set<string>>()

function handleGraphSearchSelect(value: string) {
  if (!value) return
  
  // Find node coordinates
  const node = graphNodes.find(n => n.slug === value)
  
  // If the node's type is currently filtered out, re-enable it so it becomes visible
  if (node && !graphFilterTypes.value.has(node.type)) {
    const newSet = new Set(graphFilterTypes.value)
    newSet.add(node.type)
    graphFilterTypes.value = newSet
    applyGraphFilters()
  }

  if (node && graphPanZoomRef) {
    const container = graphRef.value
    if (container) {
      const width = container.clientWidth
      const height = container.clientHeight
      // Center node while maintaining current scale, shifted left by 240px to account for the 480px drawer
      const currentScale = graphPanZoomRef.getScale()
      graphPanZoomRef.flyTo(
        width / 2 - node.x * currentScale - 240,
        height / 2 - node.y * currentScale
      )
    }
  }

  // Trigger highlight
  graphSelectedSlug.value = value
  graphHighlightSlug.value = value
  if (graphNodeElsRef.length > 0) {
    applyHighlight(value, graphAdjacencyRef, graphNodeElsRef, graphEdgeElsRef)
  }

  // Open drawer automatically when searching
  openGraphDrawer(value)

  // Clear search input after selection to be ready for next search
  setTimeout(() => { graphSearchValue.value = '' }, 300)
}

function handleGraphSearchEnter(context: { inputValue: string }) {
  const value = context.inputValue?.trim()
  if (!value) return
  
  // Try to find exact or partial match
  const match = graphSearchOptions.value.find(opt => 
    opt.label.toLowerCase().includes(value.toLowerCase()) || 
    opt.value.toLowerCase().includes(value.toLowerCase())
  )
  
  if (match) {
    handleGraphSearchSelect(match.value)
  }
}

// Load graph when switching to graph view
// Reload all pages when search query is cleared (backspace or clear button)
let searchTimer: ReturnType<typeof setTimeout> | null = null
watch(searchQuery, (val) => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    if (!val || !val.trim()) {
      loadPages()
    } else {
      doSearch()
    }
  }, 300)
})

watch(() => props.view, (v) => {
  if (v === 'graph') {
    loadGraph()
  } else if (v === 'browser') {
    nextTick(async () => {
      if (readerBodyRef.value && renderedContent.value) {
        await hydrateProtectedFileImages(readerBodyRef.value)
      }
    })
  }
})

watch(() => route.query.slug, (newSlug) => {
  if (newSlug && typeof newSlug === 'string') {
    if (!selectedPage.value || selectedPage.value.slug !== newSlug) {
      if (props.view === 'graph') {
        handleGraphSearchSelect(newSlug)
      } else {
        navigateToSlug(newSlug)
      }
    }
  }
})

onMounted(() => {
  loadPages()
  loadStats()
  if (props.view === 'graph') loadGraph()
})

onUnmounted(() => {
  if (statsTimer) {
    clearInterval(statsTimer)
  }
  if (graphHoverLeaveTimer) {
    clearTimeout(graphHoverLeaveTimer)
    graphHoverLeaveTimer = null
  }
  if (graphAnimFrame) {
    cancelAnimationFrame(graphAnimFrame)
    graphAnimFrame = 0
  }
})
</script>

<style scoped lang="less">
.wiki-browser {
  display: flex;
  height: 100%;
  min-height: 0;
  background: var(--td-bg-color-container);
}

// ── Left Sidebar ──
.wiki-sidebar {
  width: 280px;
  min-width: 240px;
  border-right: 1px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  background: var(--td-bg-color-container);
}

.wiki-sidebar-header {
  padding: 16px 16px 12px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.wiki-queue-status {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  
  .queue-text {
    line-height: 1.2;
  }
}

.wiki-global-issues-status {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  background: var(--td-warning-color-light);
  border-radius: 6px;
  color: var(--td-warning-color-8);
  font-size: 13px;
  cursor: pointer;
  transition: filter 0.2s;

  &:hover {
    filter: brightness(0.95);
  }

  .queue-text {
    line-height: 1.2;
    font-weight: 500;
  }
}

.wiki-page-list {
  flex: 1;
  overflow-y: auto;
  padding: 0 12px 12px;
}

.wiki-nav-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-radius: 6px;
  cursor: pointer;
  margin-bottom: 4px;
  transition: all 0.15s;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.active {
    background: var(--td-brand-color-light);
    .wiki-nav-text {
      color: var(--td-brand-color);
      font-weight: 600;
    }
    .wiki-nav-icon {
      color: var(--td-brand-color);
    }
  }

  .wiki-nav-icon {
    font-size: 16px;
    color: var(--td-text-color-secondary);
  }

  .wiki-nav-text {
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }
}

.wiki-sidebar-divider {
  height: 1px;
  background: var(--td-component-stroke);
  margin: 8px 12px;
}

.wiki-group-label {
  position: sticky;
  top: 0;
  z-index: 10;
  background: var(--td-bg-color-container);
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  padding: 12px 8px 8px;
  cursor: pointer;
  display: flex;
  align-items: center;
  gap: 6px;
  user-select: none;
  transition: color 0.15s;

  &:hover {
    color: var(--td-text-color-primary);
  }

  &:first-child {
    margin-top: 0;
  }
}

.wiki-group-chevron {
  font-size: 14px;
  color: var(--td-text-color-placeholder);
  transition: transform 0.2s;
  flex-shrink: 0;
}

.wiki-group-count {
  margin-left: auto;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 10px;
  padding: 0 8px;
  line-height: 18px;
  text-align: center;
}

.wiki-page-item {
  padding: 10px 12px;
  border-radius: 6px;
  cursor: pointer;
  margin-bottom: 2px;
  transition: all 0.15s;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.active {
    background: var(--td-brand-color-light);
  }
}

.wiki-page-item-title {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  margin-bottom: 4px;
}

.wiki-page-item-summary {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  margin-bottom: 6px;
}

.wiki-page-item-meta {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}

// ── Right Content ──
.wiki-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  overflow: hidden;
}

.wiki-reader {
  flex: 1;
  overflow-y: auto;
  padding: 16px 24px;
}

.wiki-reader-inner {
  width: 100%;
}

.wiki-reader-header {
  margin-bottom: 16px;
}

.wiki-nav-bar {
  margin-bottom: 16px;
}

.wiki-nav-back {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  text-decoration: none;
  padding: 4px 8px;
  margin-left: -8px;
  border-radius: 4px;
  transition: all 0.15s;

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-bg-color-container-hover);
  }
}

.wiki-reader-title {
  margin: 0 0 12px;
  font-size: 26px;
  font-weight: 600;
  line-height: 1.3;
  color: var(--td-text-color-primary);
}

.wiki-reader-aliases {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 8px;
  margin: 0 0 10px;
  font-size: 13px;
  line-height: 1.4;
}

.wiki-alias-label {
  color: var(--td-text-color-placeholder);
  font-size: 13px;
  line-height: 1.4;
}

.wiki-alias-tag {
  // Slight vertical nudge so the tag baseline lines up with the label.
  vertical-align: middle;
}

.wiki-reader-meta {
  display: flex;
  align-items: center;
  gap: 12px;
}

.wiki-reader-meta-text {
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.wiki-reader-links {
  padding: 12px 16px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 8px;
  margin-bottom: 20px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.wiki-link-group {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  font-size: 13px;
}

.wiki-link-label {
  color: var(--td-text-color-secondary);
  font-weight: 500;
  flex-shrink: 0;
}

.wiki-link-tag {
  color: var(--td-brand-color);
  text-decoration: none;
  font-family: var(--app-font-family-mono);
  font-size: 12px;
  padding: 2px 8px;
  background: rgba(7, 192, 95, 0.06);
  border-radius: 4px;
  transition: background 0.15s;

  &:hover {
    background: rgba(7, 192, 95, 0.12);
  }
}

.wiki-reader-body {
  line-height: 1.6;
  font-size: 14px;
  color: var(--td-text-color-primary);

  :deep(h1) { font-size: 24px; margin: 28px 0 16px; font-weight: 600; line-height: 1.4; }
  :deep(h2) { font-size: 18px; margin: 24px 0 12px; font-weight: 600; line-height: 1.4; }
  :deep(h3) { font-size: 16px; margin: 20px 0 10px; font-weight: 600; line-height: 1.5; }
  :deep(h4), :deep(h5), :deep(h6) { font-size: 14px; margin: 16px 0 8px; font-weight: 600; line-height: 1.5; }
  
  :deep(p) { margin: 0 0 14px; }
  
  :deep(ul), :deep(ol) { 
    margin: 0 0 14px; 
    padding-left: 24px; 
  }
  :deep(li) { 
    margin-bottom: 6px; 
    line-height: 1.6;
  }
  :deep(li > p) {
    margin-bottom: 6px;
  }

  :deep(blockquote) {
    margin: 0 0 14px;
    padding: 10px 16px;
    background: var(--td-bg-color-secondarycontainer);
    border-left: 4px solid var(--td-component-border);
    border-radius: 0 4px 4px 0;
    color: var(--td-text-color-secondary);
  }
  
  :deep(code) {
    font-family: var(--app-font-family-mono);
    font-size: 13px;
    padding: 2px 4px;
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 4px;
    color: var(--td-brand-color);
  }
  
  :deep(pre) {
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

  :deep(p:has(img)) {
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

  :deep(a.wiki-content-link) {
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

  // ── Markdown tables (GFM) ──
  // Use `width: fit-content` so tables shrink to their content instead of
  // always stretching to fill the reader column, while still respecting
  // `max-width: 100%` and allowing horizontal scrolling for wide tables.
  :deep(table) {
    display: block;
    width: fit-content;
    max-width: 100%;
    overflow-x: auto;
    margin: 0 0 16px;
    border-collapse: collapse;
    font-size: 13px;
    line-height: 1.55;
    background: var(--td-bg-color-container);
    border: 1px solid var(--td-component-stroke);
    border-radius: 6px;
    -webkit-overflow-scrolling: touch;
  }

  :deep(table thead) {
    background: var(--td-bg-color-secondarycontainer);
  }

  :deep(table th),
  :deep(table td) {
    padding: 8px 12px;
    border-bottom: 1px solid var(--td-component-stroke);
    border-right: 1px solid var(--td-component-stroke);
    text-align: left;
    vertical-align: top;
    word-break: break-word;
  }

  :deep(table th) {
    font-weight: 600;
    color: var(--td-text-color-primary);
    white-space: nowrap;
  }

  :deep(table th:last-child),
  :deep(table td:last-child) {
    border-right: none;
  }

  :deep(table tbody tr:last-child td) {
    border-bottom: none;
  }

  :deep(table tbody tr:hover) {
    background: var(--td-bg-color-secondarycontainer);
  }

  :deep(table code) {
    font-size: 12px;
  }
}

.wiki-reader-backlinks {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  margin-bottom: 24px;
}

.wiki-backlink-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  font-weight: 500;
  flex-shrink: 0;
  margin-right: 4px;
}

.wiki-backlink-tag {
  color: var(--td-text-color-secondary);
  text-decoration: none;
  font-size: 13px;
  padding: 2px 8px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 4px;
  transition: all 0.15s;

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }
}

.wiki-reader-sources {
  margin-top: 24px;
  padding-top: 16px;
  border-top: 1px solid var(--td-component-stroke);
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  font-size: 13px;
}

.wiki-source-ref {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 10px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 4px;
  color: var(--td-brand-color);
  font-size: 12px;
  text-decoration: none;
  cursor: pointer;
  transition: background 0.15s;

  &:hover {
    background: var(--td-brand-color-light);
  }
}

// ── Empty states ──
.wiki-empty-state,
.wiki-reader-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 60px 20px;
  text-align: center;
}

.wiki-empty-icon {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  background: var(--td-bg-color-secondarycontainer);
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 16px;
  color: var(--td-text-color-placeholder);
}

.wiki-empty-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  margin: 0 0 4px;
}

.wiki-empty-desc {
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  margin: 0;
}

// ── Graph ──
.wiki-graph {
  flex: 1;
  position: relative;
  overflow: hidden;
  width: 100%;
  height: 100%;
}

.wiki-graph-empty {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  z-index: 20;
  background: var(--td-bg-color-container);
}

.wiki-graph-search-container {
  position: absolute;
  top: 16px;
  left: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  z-index: 10;
  width: 280px;
}

.wiki-graph-search {
  width: 100%;
  box-shadow: var(--td-shadow-1);
  border-radius: 4px;
}

.graph-issues-badge {
  box-shadow: var(--td-shadow-1);
  opacity: 0.95;
}

:deep(.wiki-graph-drawer) {
  box-shadow: -4px 0 16px rgba(0, 0, 0, 0.08);
}

.graph-search-select {
  background: var(--td-bg-color-container) !important;
  opacity: 0.95;
}

.wiki-graph-canvas {
  width: 100%;
  height: 100%;
  min-height: 500px;
}

.wiki-graph-legend {
  position: absolute;
  top: 16px;
  right: 16px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  padding: 10px 12px;
  box-shadow: var(--td-shadow-1);
  display: flex;
  flex-direction: column;
  gap: 12px;
  z-index: 10;
  opacity: 0.95;
}

.legend-items {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.legend-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 11px;
  color: var(--td-text-color-secondary);
  
  &.clickable {
    cursor: pointer;
    transition: all 0.15s;
    
    &:hover {
      color: var(--td-text-color-primary);
    }
  }
  
  &.disabled {
    color: var(--td-text-color-placeholder);
    text-decoration: line-through;
    opacity: 0.5;
  }
}

.legend-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  display: inline-block;
  flex-shrink: 0;
}

.legend-divider {
  height: 1px;
  background: var(--td-component-stroke);
  margin: 0 -12px;
}

.legend-actions {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.legend-action {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  line-height: 14px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  user-select: none;
  transition: all 0.15s;

  &:hover {
    color: var(--td-brand-color);
    .legend-action-icon {
      color: var(--td-brand-color);
    }
  }
  
  &.active {
    color: var(--td-brand-color);
    .legend-action-icon {
      color: var(--td-brand-color);
    }
  }
}

.legend-action-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  font-size: 13px;
  line-height: 1;
  color: var(--td-text-color-placeholder);
  transition: color 0.15s;

  .t-icon {
    font-size: 13px;
    line-height: 1;
  }
}

@keyframes node-active-pulse {
  0% { transform: scale(1); opacity: 0.8; }
  100% { transform: scale(1.6); opacity: 0; }
}

.node-active-ring {
  transform-origin: 0 0;
  animation: node-active-pulse 1.5s cubic-bezier(0.25, 0.46, 0.45, 0.94) infinite;
}

// ── Issues Popup ──
.wiki-issue-trigger {
  margin-left: 8px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 20px;
  transition: opacity 0.2s ease;
  
  &:hover {
    opacity: 0.8;
  }
}

.wiki-issue-popup-content {
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
  border-radius: 8px;
  overflow: hidden;
}

.wiki-issue-popup-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  background: var(--td-bg-color-secondarycontainer);
  border-bottom: 1px solid var(--td-component-stroke);
}

.wiki-issue-popup-title {
  display: flex;
  align-items: center;
  font-weight: 500;
  font-size: 14px;
  color: var(--td-text-color-primary);
  
  .wiki-issue-popup-icon {
    color: var(--td-brand-color);
    margin-right: 8px;
    font-size: 16px;
  }
}

.wiki-issue-popup-list {
  display: flex;
  flex-direction: column;
  max-height: 400px;
  overflow-y: auto;
  gap: 12px;
  padding: 8px 12px;
}

.wiki-issue-popup-item {
  display: flex;
  padding: 16px;
  gap: 12px;
  border: 1px solid var(--td-component-border);
  border-radius: 6px;
  transition: box-shadow 0.2s ease, border-color 0.2s ease;
  background: var(--td-bg-color-container);
  
  &:hover {
    border-color: var(--td-brand-color-light);
  }
}

.wiki-issue-popup-main {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.wiki-issue-popup-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.wiki-issue-popup-desc {
  font-size: 13px;
  color: var(--td-text-color-primary);
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 150px;
  overflow-y: auto;
  padding-right: 4px;
}

/* 优化描述区域的滚动条样式 */
.wiki-issue-popup-desc::-webkit-scrollbar {
  width: 4px;
}
.wiki-issue-popup-desc::-webkit-scrollbar-thumb {
  background: var(--td-scrollbar-color);
  border-radius: 4px;
}
.wiki-issue-popup-desc::-webkit-scrollbar-track {
  background: transparent;
}

.wiki-issue-popup-meta {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-top: 8px;
  padding-top: 12px;
  border-top: 1px dashed var(--td-component-stroke);
}

.wiki-issue-popup-reporter {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  flex: 1;
}

.wiki-issue-popup-actions {
  display: flex;
  align-items: center;
}

.wiki-issue-popup-action {
  font-size: 12px;
  color: var(--td-brand-color);
  cursor: pointer;
  transition: opacity 0.2s ease;
  
  &:hover {
    opacity: 0.8;
  }
}
</style>

<style lang="less">
/* Fix Embedded Chat UI (unscoped because drawer attaches to body) */
.wiki-fix-drawer {
  .t-drawer__body {
    padding: 20px !important;
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
  }
  
  .chat {
    max-width: 100% !important;
    min-width: 100% !important;
    padding: 0 !important;
    height: 100% !important;
    flex: 1 !important;
    border-radius: 0 !important;
  }
  
  .chat_scroll_box {
    padding: 0 !important;
  }

  .chat > .input-container {
    padding: 16px 0 0 0 !important;
    box-sizing: border-box;
    width: 100% !important;
    max-width: 100% !important;
    margin: 0 !important;
    overflow-x: hidden;
  }

  .msg_list {
    max-width: 100% !important;
    padding-bottom: 0 !important;
    margin: 0 !important;
  }
}
</style>
