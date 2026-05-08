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
              :options="graphSearchEffectiveOptions"
              :loading="graphSearchLoading"
              :on-search="handleGraphRemoteSearch"
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
            <div
              v-if="graphMode === 'ego' && graphFrontierCount > 0"
              class="legend-action"
              @click="growFrontier"
              :title="$t('knowledgeEditor.wikiBrowser.growFrontierTitle', { count: graphFrontierCount })"
            >
              <span class="legend-action-icon"><t-icon name="chart-bubble" /></span>
              <span>{{ $t('knowledgeEditor.wikiBrowser.growFrontier', { count: graphFrontierCount }) }}</span>
            </div>
            <div v-if="graphMode === 'ego'" class="legend-action" @click="loadGraph">
              <span class="legend-action-icon"><t-icon name="rollback" /></span>
              <span>{{ $t('knowledgeEditor.wikiBrowser.backToOverview') }}</span>
            </div>
            <t-popup
              trigger="click"
              placement="top-right"
              :show-arrow="true"
              overlay-class-name="wiki-graph-help-popup"
            >
              <div class="legend-action" :title="$t('knowledgeEditor.wikiBrowser.helpButtonTitle')">
                <span class="legend-action-icon help-glyph-icon">?</span>
                <span>{{ $t('knowledgeEditor.wikiBrowser.helpButtonTitle') }}</span>
              </div>
              <template #content>
                <div class="wiki-graph-help">
                  <div class="help-section-title">{{ $t('knowledgeEditor.wikiBrowser.helpTitle') }}</div>
                  <div class="help-rows">
                    <div class="help-row" v-for="row in graphHelpRows" :key="row.action">
                      <span class="help-key">{{ row.action }}</span>
                      <span class="help-desc">{{ row.desc }}</span>
                    </div>
                  </div>
                </div>
              </template>
            </t-popup>
          </div>
          <template v-if="graphStatusCard">
            <div class="legend-divider"></div>
            <div class="wiki-graph-status-card">
              <div class="status-card-header">
                <t-icon :name="graphStatusCard.icon" />
                <span class="status-card-title">{{ graphStatusCard.title }}</span>
              </div>
              <div class="status-card-primary" :title="graphStatusCard.primary">
                {{ graphStatusCard.primary }}
              </div>
              <div v-if="graphStatusCard.secondary" class="status-card-secondary">
                {{ graphStatusCard.secondary }}
              </div>
            </div>
          </template>
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
            <div class="wiki-reader-meta" style="margin-bottom: 8px;">
              <t-tag size="small" :theme="getTypeTheme(graphDrawerPage.page_type)" variant="light-outline">
                {{ getTypeLabel(graphDrawerPage.page_type) }}
              </t-tag>
              <span class="wiki-reader-meta-text">{{ $t('knowledgeEditor.wikiBrowser.version', { ver: graphDrawerPage.version }) }}</span>
              <t-button
                v-if="graphMode === 'ego' && graphCenter !== graphDrawerPage.slug"
                size="small"
                variant="outline"
                theme="default"
                style="margin-left: auto;"
                :disabled="!graphDrawerCanBloom"
                @click="loadBloomNeighbors(graphDrawerPage.slug)"
              >
                {{ $t('knowledgeEditor.wikiBrowser.bloomNeighbors') }}
              </t-button>
              <t-button
                v-if="graphMode !== 'ego' || graphCenter !== graphDrawerPage.slug"
                size="small"
                variant="outline"
                theme="primary"
                :style="graphMode === 'ego' && graphCenter !== graphDrawerPage.slug ? '' : 'margin-left: auto;'"
                @click="loadEgoGraph(graphDrawerPage.slug)"
              >
                {{ $t('knowledgeEditor.wikiBrowser.expandNeighbors') }}
              </t-button>
            </div>
            <div v-if="graphDrawerNeighborHint" class="wiki-drawer-neighbor-hint" style="margin-bottom: 16px;">
              {{ graphDrawerNeighborHint }}
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
            @clear="searchResults = null"
          >
            <template #prefixIcon><t-icon name="search" /></template>
          </t-input>
        </div>

        <div class="wiki-page-list" ref="pageListRef">
          <!-- Search mode: flat list of hits, no group chrome. Clearing
               the search snaps back to the bucketed view below. -->
          <template v-if="searchResults !== null">
            <div
              v-for="page in searchResults"
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
            <div v-if="searchResults.length === 0 && !loading" class="wiki-empty-state">
              <p class="wiki-empty-desc">{{ $t('knowledgeEditor.wikiBrowser.searchNoResults') || '没有找到匹配的页面' }}</p>
            </div>
          </template>

          <template v-else>
            <!-- Index overview (pinned at top). Rendered lazily from a
                 structured API response — never loads the full directory
                 as markdown. -->
            <div
              v-if="indexAvailable"
              :class="['wiki-nav-item', { active: activeSystemView === 'index' }]"
              @click="openIndexView"
            >
              <t-icon name="catalog" class="wiki-nav-icon" />
              <span class="wiki-nav-text">{{ $t('knowledgeEditor.wikiBrowser.indexTitle') }}</span>
            </div>

            <!-- Log feed (pinned). Events live in wiki_log_entries and
                 are loaded lazily when the user clicks this entry. -->
            <div
              v-if="logAvailable"
              :class="['wiki-nav-item', { active: activeSystemView === 'log' }]"
              @click="openLogView"
            >
              <t-icon name="history" class="wiki-nav-icon" />
              <span class="wiki-nav-text">{{ $t('knowledgeEditor.wikiBrowser.logTitle') }}</span>
            </div>

            <div class="wiki-sidebar-divider" v-if="indexAvailable || logAvailable"></div>

            <!-- Horizontal tab bar: one per non-empty page_type. Clicking a
                 tab swaps the visible list to that bucket. Parallel tabs are
                 easier to scan than a vertical stack of collapsibles and
                 sidestep nested-scroller UX entirely — only one virtualized
                 list is mounted at a time. -->
            <div v-if="visibleTabs.length > 0" class="wiki-tab-bar">
              <div
                v-for="tab in visibleTabs"
                :key="tab.type"
                :class="['wiki-tab', { active: activeTab === tab.type }]"
                @click="setActiveTab(tab.type)"
              >
                <span class="wiki-tab-label">{{ tab.label }}</span>
                <span class="wiki-tab-count">{{ tab.total }}</span>
              </div>
            </div>

            <!-- Active-tab list -->
            <template v-if="activeGroup">
              <RecycleScroller
                ref="groupScrollerRef"
                class="wiki-group-scroller"
                :items="activeGroup.pages"
                :item-size="WIKI_PAGE_ITEM_HEIGHT"
                key-field="id"
                :buffer="400"
                page-mode
                v-slot="{ item }"
              >
                <div
                  :class="['wiki-page-item', { active: selectedPage?.id === item.id }]"
                  @click="selectPage(item)"
                >
                  <div class="wiki-page-item-title">{{ item.title }}</div>
                  <div class="wiki-page-item-summary">{{ item.summary }}</div>
                  <div class="wiki-page-item-meta">
                    <span>{{ formatDate(item.updated_at) }}</span>
                  </div>
                </div>
              </RecycleScroller>
              <!-- Sentinel: when this hits the viewport, fetch the next
                   page. Page-mode RecycleScroller delegates scrolling to
                   `.wiki-page-list`, so scroll-end events don't fire on
                   the scroller itself; IntersectionObserver is the right
                   primitive here and degrades gracefully while loading. -->
              <div
                v-if="activeGroup.hasMore"
                ref="groupSentinelRef"
                class="wiki-group-sentinel"
                :data-type="activeGroup.type"
              ></div>
              <div v-if="activeGroup.loading" class="wiki-group-loading">
                <t-loading size="small" />
              </div>
            </template>

            <!-- Empty state -->
            <div v-if="!hasContentPages && !loading" class="wiki-empty-state">
              <div class="wiki-empty-icon">
                <t-icon name="file-unknown" size="36px" />
              </div>
              <p class="wiki-empty-title">{{ $t('knowledgeEditor.wikiBrowser.emptyTitle') }}</p>
              <p class="wiki-empty-desc">{{ $t('knowledgeEditor.wikiBrowser.emptyDesc') }}</p>
            </div>
          </template>
        </div>
      </aside>

      <!-- Right Panel: Reader -->
      <div class="wiki-content">
        <div class="wiki-reader">
          <div class="wiki-reader-inner">
            <template v-if="selectedPage">
              <!-- Navigation -->
              <div v-if="navHistory.length || navFromSystemView" class="wiki-nav-bar">
                <a href="#" class="wiki-nav-back" @click.prevent="goBack">
                  <t-icon name="arrow-left" size="14px" />
                  <span>{{ backLabel }}</span>
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

            <!-- System view: index overview rendered as markdown. Starts
                 with intro only; an IntersectionObserver-driven sentinel
                 at the bottom auto-appends the next directory section
                 (Summary → Entity → Concept → …) as the user scrolls
                 near the end. [[wiki-link]] clicks inside the rendered
                 body are handled by handleContentClick just like a
                 regular wiki page. -->
            <template v-else-if="activeSystemView === 'index'">
              <div class="wiki-reader-header">
                <h2 class="wiki-reader-title">{{ $t('knowledgeEditor.wikiBrowser.indexTitle') }}</h2>
                <div class="wiki-reader-meta">
                  <t-tag size="small" theme="default" variant="light-outline">
                    {{ $t('knowledgeEditor.wikiBrowser.indexOverviewTag') }}
                  </t-tag>
                </div>
              </div>
              <div v-if="indexLoading && !indexMarkdown" class="wiki-reader-empty">
                <p class="wiki-empty-title">{{ $t('knowledgeEditor.wikiBrowser.logLoading') }}</p>
              </div>
              <template v-else-if="indexMarkdown">
                <div
                  ref="indexBodyRef"
                  class="wiki-reader-body wiki-index-body"
                  v-html="renderedIndexMarkdown"
                  @click="handleContentClick"
                ></div>
                <div v-if="indexHasMore" ref="indexSentinelRef" class="wiki-index-sentinel">
                  <span v-if="indexLoading" class="wiki-index-loading">
                    {{ $t('knowledgeEditor.wikiBrowser.logLoading') }}
                  </span>
                </div>
              </template>
              <div v-else-if="!indexLoading" class="wiki-reader-empty">
                <p class="wiki-empty-title">{{ $t('knowledgeEditor.wikiBrowser.indexEmpty') }}</p>
              </div>
            </template>

            <!-- System view: log feed. Mutually exclusive with selectedPage. -->
            <template v-else-if="activeSystemView === 'log'">
              <div class="wiki-reader-header">
                <h2 class="wiki-reader-title">{{ $t('knowledgeEditor.wikiBrowser.logTitle') }}</h2>
                <div class="wiki-reader-meta">
                  <t-tag size="small" theme="default" variant="light-outline">
                    {{ $t('knowledgeEditor.wikiBrowser.logFeedTag') }}
                  </t-tag>
                </div>
              </div>
              <div class="wiki-log-feed">
                <div v-if="logEntries.length === 0 && logInitialized" class="wiki-log-empty">
                  {{ $t('knowledgeEditor.wikiBrowser.logEmpty') }}
                </div>
                <div v-for="entry in logEntries" :key="entry.id" class="wiki-log-entry">
                  <div class="wiki-log-entry-header">
                    <t-tag size="small" :theme="entry.action === 'retract' ? 'danger' : 'primary'" variant="light">
                      {{ entry.action }}
                    </t-tag>
                    <span class="wiki-log-entry-title">{{ entry.doc_title || entry.knowledge_id || '—' }}</span>
                    <span class="wiki-log-entry-time">{{ formatDate(entry.created_at) }}</span>
                  </div>
                  <div v-if="entry.summary" class="wiki-log-entry-summary">{{ entry.summary }}</div>
                  <div v-if="entry.pages_affected && entry.pages_affected.length" class="wiki-log-entry-pages">
                    <a
                      v-for="ref in entry.pages_affected"
                      :key="entry.id + ':' + ref.slug"
                      href="#"
                      class="wiki-log-entry-page"
                      :title="ref.slug"
                      @click.prevent="navigateToSlug(ref.slug)"
                    >{{ ref.title || ref.slug }}</a>
                  </div>
                </div>
                <div v-if="logNextCursor || !logInitialized" class="wiki-log-load-more">
                  <t-button
                    size="small"
                    variant="outline"
                    theme="default"
                    :loading="logLoading"
                    @click="loadMoreLog"
                  >
                    {{ logInitialized ? $t('knowledgeEditor.wikiBrowser.logLoadMore') : $t('knowledgeEditor.wikiBrowser.logLoading') }}
                  </t-button>
                </div>
              </div>
            </template>

            <!-- No page selected -->
            <div v-else class="wiki-reader-empty">
              <div class="wiki-empty-icon">
                <t-icon name="browse" size="48px" />
              </div>
              <p class="wiki-empty-title" v-if="hasContentPages">{{ $t('knowledgeEditor.wikiBrowser.selectPageHint') }}</p>
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
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useMenuStore } from '@/stores/menu'
import { useSettingsStore } from '@/stores/settings'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import { MessagePlugin } from 'tdesign-vue-next'
// RecycleScroller virtualizes the sidebar page lists so expanding a
// 40k-item group no longer commits 40k DOM nodes. Each item has a fixed
// height (title + 2-line summary + meta + padding) which keeps recycle
// mode cheap — no measurement overhead per item.
import { RecycleScroller } from 'vue-virtual-scroller'
import { hydrateProtectedFileImages } from '@/utils/security'
import picturePreview from '@/components/picture-preview.vue'
import { createSessions } from '@/api/chat'
import ChatView from '@/views/chat/index.vue'
import {
  listWikiPages,
  getWikiPage,
  getWikiIndex,
  getWikiLog,
  getWikiGraph,
  getWikiStats,
  searchWikiPages,
  listWikiIssues,
  updateWikiIssueStatus,
  type WikiPage,
  type WikiGraphData,
  type WikiStats,
  type WikiPageIssue,
  type WikiLogEntry,
  type WikiIndexGroup,
  type WikiIndexEntryDTO,
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

// Per-type pagination state for the sidebar. 4万-page wikis used to load
// the entire page list into `pages.value` at startup (50 pages of 500 =
// 25k rows of JSON fetched even when the user only wants to glance at
// one type). Instead we now keep one bucket per page_type and lazy-load
// them on demand:
//
//   * Each bucket tracks loaded items, next page cursor, total count
//     (from the backend), and whether a fetch is currently in flight.
//   * Tabs (summary/entity/concept/…) only request their bucket when
//     the user expands that group, and more items are pulled when the
//     virtualized scroller nears the bottom.
//
// `pages.value` is still kept and contains the union of all loaded
// items, purely as a fallback lookup table for `slugDisplayName()` and
// similar "I saw this title somewhere" paths.
interface PageTypeBucket {
  items: WikiPage[]
  nextPage: number   // page cursor for the next fetch, 1-based
  total: number      // KB-wide count reported by the backend for this type
  loading: boolean
  initialized: boolean // true once the first page has been fetched
}
const pagesByType = ref<Record<string, PageTypeBucket>>({})
// Index view state. The reader renders an incrementally-built markdown
// string rather than a structured list — opening the view loads intro
// only, and "Load more" appends one directory section at a time in a
// fixed order (Summary → Entity → Concept → Synthesis → Comparison).
// Once the last section exhausts its pages, indexHasMore flips off.
//
// We deliberately avoid keeping a parallel structured list + a parallel
// markdown buffer; the markdown is the single source of truth the reader
// renders, and [[wiki-link]] clicks flow through the same
// handleContentClick as regular page bodies.
const indexMarkdown = ref('')
const indexLoading = ref(false)
const indexAvailable = ref(false)
// Per-section pagination cursor. Empty string = not yet loaded; empty
// cursor AFTER a load = that section is exhausted. `indexSectionIdx`
// tracks which section in INDEX_SECTION_ORDER is "next to load" — we
// advance to the following section only when the current one runs out.
const indexSections = ref<Record<string, { loaded: boolean; cursor: string; total: number }>>({})
const indexSectionIdx = ref(0)
const indexBodyRef = ref<HTMLElement | null>(null)
const indexSentinelRef = ref<HTMLElement | null>(null)
let indexObserver: IntersectionObserver | null = null

// Order matters: Summary first (these are the document-level pages the
// user most often wants to see), then the LLM-derived ones. Matches the
// plan's "intro then Summary → Entity → Concept → …" progression.
const INDEX_SECTION_ORDER = [
  'summary',
  'entity',
  'concept',
  'synthesis',
  'comparison',
] as const
// logAvailable is a flag: the sidebar "Log" entry is always shown once a
// KB exists, because the backing wiki_log_entries table is KB-independent
// and `GET /wiki/log` returns an empty entries list when nothing has been
// logged yet. We don't need a full WikiPage object anymore — selecting
// Log swaps the reader into a dedicated feed view below.
const logAvailable = ref(true)

// activeSystemView lets the reader toggle between a regular wiki page
// (selectedPage) and a "virtual" system view — index overview and log
// feed. These modes are mutually exclusive: entering a system view
// clears selectedPage, and picking a page clears the system view flag.
const activeSystemView = ref<'' | 'index' | 'log'>('')

// Paginated state for the log view. `entries` grows as the user scrolls;
// `nextCursor` is the opaque cursor returned by the backend and empty
// signals end-of-feed. `loading` is the guard that prevents overlapping
// loadMore calls while a request is in flight.
const logEntries = ref<WikiLogEntry[]>([])
const logNextCursor = ref('')
const logLoading = ref(false)
const logInitialized = ref(false)

// When the user types into the search box we leave pagination mode and
// show a flat result list instead. Bucketed state is preserved behind
// the scenes so clearing the query can snap back without re-fetching.
const searchResults = ref<WikiPage[] | null>(null)
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

// Graph slicing state. The backend caps an overview fetch at 500 nodes —
// tens-of-thousands-page wikis would otherwise crash the browser trying to
// render 100k SVG elements. `graphMode` tracks whether we're on the
// overview landing or drilled into an ego neighborhood so the UI can offer
// "back to overview" and show the truncation hint.
const graphMode = ref<'overview' | 'ego'>('overview')
const graphCenter = ref<string>('')
const GRAPH_OVERVIEW_LIMIT = 500
const GRAPH_EGO_LIMIT = 500
const GRAPH_EGO_DEFAULT_DEPTH = 1

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

// toggleGraphFilterType flips a page_type in the active allow-list and
// refetches the graph from the server. Client-side DOM hiding used to
// suffice when the canvas contained every page, but once we cap the
// overview at top-500 by link_count, hiding the "summary" type just
// blanks out most of the canvas without surfacing the next 500 nodes
// that would qualify under the narrowed filter. Re-asking the server
// keeps the top-N always relevant to what the user said they wanted to
// see, at the cost of one network round-trip per toggle.
async function toggleGraphFilterType(type: string) {
  const newSet = new Set(graphFilterTypes.value)
  if (newSet.has(type)) {
    newSet.delete(type)
  } else {
    newSet.add(type)
  }
  graphFilterTypes.value = newSet

  // Dismiss any highlight/drawer that no longer matches the new filter
  // before we repaint, otherwise the old selection can linger against
  // freshly-rendered elements that were never built for it.
  graphHighlightSlug.value = null
  if (graphSelectedSlug.value && !newSet.has(
    graphData.value?.nodes.find(n => n.slug === graphSelectedSlug.value)?.page_type || ''
  )) {
    graphSelectedSlug.value = null
    graphDrawerVisible.value = false
  }

  if (graphMode.value === 'ego' && graphCenter.value) {
    await loadEgoGraph(graphCenter.value)
  } else {
    await loadGraph()
  }
}

// applyGraphFilters is retained as a no-op for compatibility with a
// handful of callers that used to nudge the client-side hide/show state
// (e.g. handleGraphSearchSelect re-enabling a filtered-out type before
// centering on it). With server-side filtering the allow-list change
// itself triggers a refetch via the watcher in toggleGraphFilterType,
// so this function no longer has to do anything.
function applyGraphFilters() {
  // Intentionally empty: server-side filtering handles the actual
  // node/edge membership when the allow-list changes.
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
    // Every node in graphNodes is a visible candidate now that filtering
    // is server-side — no need to recheck the client-side allow-list.
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

const graphDrawerVisible = ref(false)
const graphDrawerPage = ref<WikiPage | null>(null)
const navHistory = ref<WikiPage[]>([])
// navFromSystemView remembers which system view (Index / Log) the user
// was viewing when they clicked into a slug, so goBack can restore it
// once the page-level history stack is empty. We keep this parallel to
// navHistory rather than widening its element type — navHistory is
// consumed everywhere as `WikiPage[]` and that contract stays cleaner
// if the system-view sentinel lives in its own ref.
const navFromSystemView = ref<'' | 'index' | 'log'>('')
// Index and log pages are now state refs (loaded by their own endpoints
// at startup) rather than computed over the full page list. The old
// computed implementation required pulling every page into memory just
// to pluck two system pages.

// typeOrder drives the order of groups in the sidebar. Keep in sync
// with WIKI_PAGE_TYPES on the backend; unknown types fall through to
// the "other" bucket at the bottom of groupedPages.
const typeOrder = ['summary', 'entity', 'concept', 'synthesis', 'comparison']

// groupedPages projects the bucketed state into the shape the sidebar
// template expects: one {type, label, items, total, loading, hasMore}
// per displayed group. Groups with zero total are hidden (nothing to
// show) but groups with total > 0 but items.length === 0 still render
// so the collapse header can trigger a lazy fetch.
const groupedPages = computed(() => {
  const out: {
    type: string
    label: string
    pages: WikiPage[]
    total: number
    loading: boolean
    hasMore: boolean
  }[] = []
  const seen = new Set<string>()
  const push = (type: string) => {
    const bucket = pagesByType.value[type]
    if (!bucket) return
    if (bucket.total === 0) return
    out.push({
      type,
      label: getTypeLabel(type),
      pages: bucket.items,
      total: bucket.total,
      loading: bucket.loading,
      hasMore: bucket.items.length < bucket.total,
    })
    seen.add(type)
  }
  for (const type of typeOrder) push(type)
  // Any types present in the buckets but not in typeOrder go last in
  // insertion order so the sidebar doesn't suddenly hide a future type.
  for (const type of Object.keys(pagesByType.value)) {
    if (seen.has(type)) continue
    if (type === 'index' || type === 'log') continue
    push(type)
  }
  return out
})

// hasContentPages is the sidebar's empty-state gate. The old version
// looked at `contentPages.length === 0`, which forced a full load to
// decide whether the wiki was truly empty. Now we check bucket totals
// reported by the backend — zero everywhere means no content pages.
const hasContentPages = computed(() => {
  for (const bucket of Object.values(pagesByType.value)) {
    if (bucket.total > 0) return true
  }
  return false
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

// graphDrawerNeighborStatus describes, for the currently open drawer page,
// how the canvas relates to the KB-wide neighborhood of the node. The
// accounting is subtler than a simple "shown vs link_count" because three
// different situations produce different interpretations of a gap:
//
//   ego center — the backend already returned every neighbor reachable
//     through BFS at depth 1+. Any difference between `link_count` and
//     the visible degree is pages that couldn't be traversed (dead refs,
//     type-filtered pages, soft-deleted neighbors), NOT pages we can
//     still fetch. Expanding or blooming from the center does nothing
//     useful, so we flag it as fullyExplored and disable the buttons.
//
//   ego non-center — difference IS "neighbors not yet loaded". The user
//     can bloom to pull them in. This is the main signal for the dashed
//     expansion ring.
//
//   overview — difference is "neighbors that didn't make top-500", and
//     the fix isn't bloom (overview doesn't bloom) but pivoting to ego.
//     We still disable Bloom (it's an ego-only op) but leave Expand
//     enabled so the user can drill down.
const graphDrawerNeighborStatus = computed(() => {
  const page = graphDrawerPage.value
  if (!page) return null
  const data = graphData.value
  if (!data) return null
  const node = data.nodes.find(n => n.slug === page.slug)
  if (!node) {
    // Drawer is open on a page that isn't currently on the canvas (e.g.
    // the user just clicked a wiki-link that triggered an ego pivot and
    // we're between data update and re-render). Treat as unknown so the
    // button stays enabled — the pivot will populate neighbors shortly.
    return null
  }
  // Undirected degree within the current subgraph. Both incoming and
  // outgoing edges count toward a visible neighbor, matching how
  // link_count is computed server-side (in+out).
  const neighbors = new Set<string>()
  for (const e of data.edges) {
    if (e.source === page.slug) neighbors.add(e.target)
    else if (e.target === page.slug) neighbors.add(e.source)
  }
  const visible = neighbors.size
  const total = node.link_count || 0
  // hidden can go negative in a rare corner case — a neighbor might be
  // visible via an edge that the link_count counter didn't know about
  // (e.g. a broken-link cleanup happened after the snapshot). Clamp.
  const hidden = Math.max(0, total - visible)
  const isEgoCenter = data.meta?.mode === 'ego' && data.meta.center === page.slug
  const isOverview = data.meta?.mode === 'overview'
  return {
    visible,
    total,
    hidden,
    isEgoCenter,
    isOverview,
    // fullyExplored drives the disabled state of the expand/bloom buttons.
    // True when either there's genuinely nothing to load, or when we're
    // on the ego center and any remaining gap is unreachable (dead refs
    // / filtered out).
    fullyExplored: total === 0 || visible >= total || isEgoCenter,
  }
})

const graphDrawerNeighborHint = computed(() => {
  const status = graphDrawerNeighborStatus.value
  if (!status) return ''
  if (status.total === 0) {
    return t('knowledgeEditor.wikiBrowser.neighborsNone')
  }
  if (status.visible >= status.total) {
    // All neighbors already visible. Expand still does something useful
    // though — it pivots the canvas to just this node's neighborhood,
    // giving the user a focused N-node view instead of wading through
    // the 500-node overview. Say so rather than sounding like a dead end.
    return t('knowledgeEditor.wikiBrowser.neighborsAllShown', { total: status.total })
  }
  if (status.isEgoCenter) {
    // hidden > 0 but can't be loaded — distinguish from "未加载".
    return t('knowledgeEditor.wikiBrowser.neighborsCenterUnreachable', {
      visible: status.visible,
      total: status.total,
      hidden: status.hidden,
    })
  }
  if (status.isOverview) {
    // hidden means "not in the top-500 subgraph"; bloom doesn't help
    // here, expand/pivot does.
    return t('knowledgeEditor.wikiBrowser.neighborsOverviewHidden', {
      visible: status.visible,
      total: status.total,
      hidden: status.hidden,
    })
  }
  return t('knowledgeEditor.wikiBrowser.neighborsProgress', {
    visible: status.visible,
    total: status.total,
    hidden: status.hidden,
  })
})

// graphDrawerCanBloom is true when clicking Bloom would actually add
// new nodes to the canvas. Bloom is additive so we only disable it when
// there's nothing to add: either the node is the ego center (BFS already
// gave us everything reachable) or every one of its neighbors is already
// on screen.
const graphDrawerCanBloom = computed(() => {
  const status = graphDrawerNeighborStatus.value
  if (!status) return true
  if (status.isEgoCenter) return false
  return status.hidden > 0
})

// graphFrontierCount powers the legend's "Grow frontier (N)" button. It
// counts nodes on the current ego canvas that the user can still expand
// outward from — matches the filter used by growFrontier() itself so
// the button count can never disagree with what the click actually
// expands. Hidden when 0 so the button disappears once the local
// neighborhood is fully explored (or only Index/Log super-nodes remain).
const graphFrontierCount = computed(() => {
  const data = graphData.value
  if (!data || data.meta?.mode !== 'ego') return 0
  const visibleDegree = new Map<string, number>()
  for (const e of data.edges) {
    visibleDegree.set(e.source, (visibleDegree.get(e.source) ?? 0) + 1)
    visibleDegree.set(e.target, (visibleDegree.get(e.target) ?? 0) + 1)
  }
  let count = 0
  const centerSlug = data.meta?.center || ''
  for (const n of data.nodes) {
    if (isFrontierCandidate(n, centerSlug, visibleDegree.get(n.slug) ?? 0)) {
      count += 1
    }
  }
  return count
})

// graphStatusCard drives the little summary panel below the legend.
//
// The old design ("以 A 为中心 · 1 跳 · 7 个节点" / "showing 500 / 40000,
// click a node to expand neighbors") crammed four pieces of info into a
// single line of running prose — the most important bit (what page the
// user is focused on) got lost between the jargon ("1 跳") and the
// imperative tail ("click a node...").
//
// The card version separates the three jobs into visible slots:
//   header  → icon + short mode name, tells the user "am I looking at
//             the whole wiki or at one page's neighborhood"
//   primary → the noun that identifies the current view (page title in
//             ego mode, "X / Y 个节点" in overview)
//   secondary → optional subline with type badge / hint / progress
//
// We also resolve `meta.center` (a slug) to the actual page title via
// graphData.nodes so users see "北京市昌职…" instead of "entity/beijing-..."
// — a common complaint with the old hint.
// graphHelpRows is the content of the ? popup. Keeping it in a computed
// rather than the template lets us i18n each action/description in one
// place and also makes it trivially extensible — new shortcuts land as
// one row addition each rather than a full template rewrite. The order
// below is "most common → rarest"; users don't typically read past the
// first few rows.
const graphHelpRows = computed(() => [
  { action: t('knowledgeEditor.wikiBrowser.helpClickAction'), desc: t('knowledgeEditor.wikiBrowser.helpClickDesc') },
  { action: t('knowledgeEditor.wikiBrowser.helpDblClickAction'), desc: t('knowledgeEditor.wikiBrowser.helpDblClickDesc') },
  { action: t('knowledgeEditor.wikiBrowser.helpShiftClickAction'), desc: t('knowledgeEditor.wikiBrowser.helpShiftClickDesc') },
  { action: t('knowledgeEditor.wikiBrowser.helpHoverPlusAction'), desc: t('knowledgeEditor.wikiBrowser.helpHoverPlusDesc') },
  { action: t('knowledgeEditor.wikiBrowser.helpDragAction'), desc: t('knowledgeEditor.wikiBrowser.helpDragDesc') },
  { action: t('knowledgeEditor.wikiBrowser.helpPanAction'), desc: t('knowledgeEditor.wikiBrowser.helpPanDesc') },
  { action: t('knowledgeEditor.wikiBrowser.helpZoomAction'), desc: t('knowledgeEditor.wikiBrowser.helpZoomDesc') },
])

const graphStatusCard = computed((): { icon: string; title: string; primary: string; secondary: string } | null => {
  const data = graphData.value
  if (!data?.meta) return null
  const meta = data.meta
  if (meta.mode === 'ego' && meta.center) {
    const centerNode = data.nodes.find(n => n.slug === meta.center)
    const centerTitle = centerNode?.title || meta.center
    const typeLabel = centerNode ? getTypeLabel(centerNode.page_type) : ''
    // Subtract 1 so the count means "related nodes" (excluding the
    // center itself) — matches how users count "connections". If the
    // count is 0 the center is an isolated page.
    const relatedCount = Math.max(0, meta.returned - 1)
    const secondaryParts: string[] = []
    if (typeLabel) secondaryParts.push(typeLabel)
    secondaryParts.push(t('knowledgeEditor.wikiBrowser.cardRelatedNodes', { count: relatedCount }))
    return {
      icon: 'focus',
      title: t('knowledgeEditor.wikiBrowser.cardEgoTitle'),
      primary: centerTitle,
      secondary: secondaryParts.join(' · '),
    }
  }
  if (meta.mode === 'overview') {
    const secondary = meta.truncated
      ? t('knowledgeEditor.wikiBrowser.cardOverviewHintTruncated')
      : t('knowledgeEditor.wikiBrowser.cardOverviewHintFull')
    return {
      icon: 'chart-bubble',
      title: t('knowledgeEditor.wikiBrowser.cardOverviewTitle'),
      primary: t('knowledgeEditor.wikiBrowser.cardOverviewPrimary', {
        returned: meta.returned,
        total: meta.total,
      }),
      secondary,
    }
  }
  return null
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

// activeTab drives which page_type's list is visible in the sidebar.
// Pre-tabbed UX stacked collapsible groups, but on a 40k-page KB the
// expanded groups nest RecycleScroller viewports and scroll events get
// ambiguous — "which list am I scrolling?" The tabbed version removes
// that ambiguity by mounting exactly one scroller at a time.
const activeTab = ref<string>('')
// The outer scroll container — the RecycleScroller runs in page-mode and
// delegates scrolling here, so we need a handle to reset scrollTop on
// tab switches. Otherwise the retained scroll position from the old tab
// re-triggers the sentinel on the new tab's (shorter) list and cascades
// load-more calls until the new bucket catches up.
const pageListRef = ref<HTMLElement | null>(null)
// Handle on the active RecycleScroller. In page-mode the scroller only
// recomputes its visible window on scroll events; when we extend `items`
// in place the previously-rendered tail remains mounted at its old
// offsets, so newly appended rows appear out of order at the bottom
// until the user jiggles the scroll. Calling `updateVisibleItems` after
// a batch arrives forces the recompute and avoids that "ghost last page"
// artifact.
const groupScrollerRef = ref<{ updateVisibleItems?: (force: boolean) => void } | null>(null)

function setActiveTab(type: string) {
  if (activeTab.value === type) return
  activeTab.value = type
  // Snap back to the top before the new list renders so the sentinel
  // has to be scrolled to, not simply appear at a retained scroll depth.
  if (pageListRef.value) pageListRef.value.scrollTop = 0
  const bucket = pagesByType.value[type]
  if (bucket && !bucket.initialized && !bucket.loading) {
    loadPagesForType(type)
  }
}

// visibleTabs mirrors groupedPages but is meant for rendering the
// horizontal tab bar: only non-empty types survive, in typeOrder with
// any unknown types appended after.
const visibleTabs = computed(() =>
  groupedPages.value.map(g => ({ type: g.type, label: g.label, total: g.total }))
)

// activeGroup resolves activeTab into the current group descriptor,
// or null when the active type has been deselected (e.g. after a
// filter toggle zeroed out every bucket).
const activeGroup = computed(() => {
  if (!activeTab.value) return null
  return groupedPages.value.find(g => g.type === activeTab.value) || null
})

// Keep activeTab in sync with what's available. When loadPages first
// populates buckets, pick the first non-empty tab. When a user deletes
// the last page of the active type we transparently switch to the next
// available one so the sidebar never shows "tab selected with no list".
watch(visibleTabs, (tabs) => {
  if (tabs.length === 0) {
    activeTab.value = ''
    return
  }
  if (!tabs.some(t => t.type === activeTab.value)) {
    activeTab.value = tabs[0].type
  }
})

// IntersectionObserver-driven infinite scroll for the active tab.
// In page-mode the RecycleScroller doesn't emit scroll-end, so we
// observe a 1px sentinel placed after the list. When it enters the
// viewport we pull the next page for the active bucket; guards in
// loadPagesForType prevent double-fetching. We re-bind whenever the
// sentinel element changes (tab switch, empty/has-more transitions).
const groupSentinelRef = ref<HTMLElement | null>(null)
let groupSentinelObserver: IntersectionObserver | null = null
watch(groupSentinelRef, (el) => {
  if (groupSentinelObserver) {
    groupSentinelObserver.disconnect()
    groupSentinelObserver = null
  }
  if (!el) return
  groupSentinelObserver = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue
      const type = (entry.target as HTMLElement).dataset.type
      if (type) loadPagesForType(type)
    }
  }, { rootMargin: '200px' })
  groupSentinelObserver.observe(el)
}, { flush: 'post' })

// WIKI_PAGE_ITEM_HEIGHT must match the rendered height of .wiki-page-item
// INCLUDING its bottom margin. RecycleScroller absolutely positions
// items at multiples of this value; if the CSS renders at a different
// height, siblings overlap. Measured height with 1-line title + 2-line
// 12/1.5 summary + meta + 10/10 padding = ~95px; we lock the item to
// exactly 100px below so the math is independent of summary text length.
const WIKI_PAGE_ITEM_HEIGHT = 100

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

// Label shown next to the back arrow on page headers. Prefers the
// nearest page-history entry when available so the user sees where
// they'll land; falls back to the Index/Log label when the current
// page was opened directly from a system view.
const backLabel = computed(() => {
  if (navHistory.value.length > 0) {
    return navHistory.value[navHistory.value.length - 1].title
  }
  if (navFromSystemView.value === 'index') {
    return t('knowledgeEditor.wikiBrowser.indexTitle')
  }
  if (navFromSystemView.value === 'log') {
    return t('knowledgeEditor.wikiBrowser.logTitle')
  }
  return ''
})

// Rendered markdown for the incremental index view. Re-runs every time
// indexMarkdown grows (initial intro load or a loadMore section append).
const renderedIndexMarkdown = computed(() => {
  if (!indexMarkdown.value) return ''
  return renderMarkdown(indexMarkdown.value)
})

// True while another section or page is available to load. Starts true
// after the first fetch (intro only loaded, sections untouched), becomes
// false once the last section in INDEX_SECTION_ORDER is exhausted.
const indexHasMore = computed(() => {
  if (!indexAvailable.value) return false
  if (indexSectionIdx.value >= INDEX_SECTION_ORDER.length) return false
  return true
})

watch(renderedContent, async () => {
  await nextTick()
  if (readerBodyRef.value) {
    await hydrateProtectedFileImages(readerBodyRef.value)
  }
})

// Index body may contain image markdown from an LLM-generated intro;
// hydrate the same way as regular page content so protected URLs
// resolve. Also re-applied after every loadMore append.
watch(renderedIndexMarkdown, async () => {
  await nextTick()
  if (indexBodyRef.value) {
    await hydrateProtectedFileImages(indexBodyRef.value)
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

// WIKI_SIDEBAR_PAGE_SIZE is the per-type fetch batch. Small enough that
// the initial paint is snappy even on a big KB, large enough that the
// virtualized scroller normally gets everything it needs in one request
// for common wikis. Later pages are pulled on scroll.
const WIKI_SIDEBAR_PAGE_SIZE = 50

// CONTENT_PAGE_TYPES is the list of page_type buckets the sidebar
// renders as collapsible groups. We initialize all of them up-front so
// the sidebar scaffolding renders immediately with "loading" markers —
// if a bucket truly has 0 pages server-side, the total field comes back
// as 0 and groupedPages hides it.
const CONTENT_PAGE_TYPES = ['summary', 'entity', 'concept', 'synthesis', 'comparison']

function emptyBucket(): PageTypeBucket {
  return { items: [], nextPage: 1, total: 0, loading: false, initialized: false }
}

function ensureBucket(type: string): PageTypeBucket {
  if (!pagesByType.value[type]) {
    pagesByType.value[type] = emptyBucket()
  }
  return pagesByType.value[type]
}

// loadPagesForType fetches the next page for a single type bucket. The
// first call seeds `total` from the backend so subsequent `hasMore`
// checks work without another round-trip. Guard against concurrent
// invocations for the same type (e.g. scroll event fires rapidly while
// a network request is still in flight).
async function loadPagesForType(type: string, opts: { reset?: boolean } = {}) {
  const bucket = ensureBucket(type)
  if (bucket.loading) return
  if (opts.reset) {
    bucket.items = []
    bucket.nextPage = 1
    bucket.total = 0
    bucket.initialized = false
  }
  if (bucket.initialized && bucket.items.length >= bucket.total) return

  bucket.loading = true
  try {
    const res = await listWikiPages(props.knowledgeBaseId, {
      page_type: type,
      page: bucket.nextPage,
      page_size: WIKI_SIDEBAR_PAGE_SIZE,
    })
    const body: any = (res as any).data || res
    const batch: WikiPage[] = body?.pages || []
    const reportedTotal = Number(body?.total) || 0

    bucket.items.push(...batch)
    bucket.total = reportedTotal
    bucket.nextPage += 1
    bucket.initialized = true

    // Mirror the newly arrived rows into the flat pages list so
    // slugDisplayName and friends keep working.
    if (batch.length > 0) {
      const seen = new Set(pages.value.map(p => p.id))
      for (const p of batch) {
        if (!seen.has(p.id)) pages.value.push(p)
      }
    }
  } catch (e) {
    console.error(`Failed to load wiki pages of type ${type}:`, e)
  } finally {
    bucket.loading = false
  }

  // Kick the RecycleScroller into recomputing its visible window now
  // that `items` has grown. Without this, new rows appear in the wrong
  // order at the bottom until the user scrolls to trigger a recompute.
  await nextTick()
  groupScrollerRef.value?.updateVisibleItems?.(true)
}

// loadIndexAndLog probes the wiki index so the sidebar knows to show
// the pinned Index/Log entries. We ask the backend for intro only (zero
// group types) — a bounded response regardless of KB size. Sections are
// fetched lazily after the user actually opens the Index view; see
// loadMoreIndexSection.
//
// The log "page" is no longer stored in wiki_pages — it lives in the
// dedicated wiki_log_entries table. We don't need to pre-fetch anything
// here to decide whether to render the sidebar Log entry; the flag is
// always on, and the actual feed is fetched lazily when the user clicks
// the entry (see openLogView / loadMoreLog).
// stripLegacyIndexDirectory removes the inline "## Summary (N)\n[[...]]
// ..." directory listing from a legacy index row. Old wiki_pages rows
// stored "intro + directory markdown" in content; after the refactor
// intro is the whole payload, but pre-existing KBs still carry the
// directory until the next ingest batch rewrites it (see
// wikiIngestService.rebuildIndexPage). We don't want the stale
// directory to show up in the reader alongside the new live-fetched
// sections, so we clip everything from the first `\n## ` heading on.
function stripLegacyIndexDirectory(intro: string): string {
  if (!intro) return ''
  const idx = intro.indexOf('\n## ')
  if (idx < 0) return intro.trim()
  return intro.slice(0, idx).trim()
}

async function loadIndexAndLog() {
  try {
    // We only need intro on the initial probe — the directory groups
    // are fetched lazily once the user opens the Index view. Passing
    // an unknown type filter yields a cheap single count(*) + 0 rows
    // on the backend instead of scanning every directory group, and
    // the frontend discards the resulting empty group unconditionally.
    const idxRes = await getWikiIndex(props.knowledgeBaseId, { types: ['__intro_only__'], limit: 1 })
    const body: any = (idxRes as any).data || (idxRes as any)
    const intro: string = body?.intro || ''
    const cleanIntro = stripLegacyIndexDirectory(intro)
    indexMarkdown.value = cleanIntro ? cleanIntro + '\n' : ''
    indexAvailable.value = true
    indexSections.value = {}
    indexSectionIdx.value = 0
    logAvailable.value = true
  } catch (e) {
    console.error('Failed to load wiki index:', e)
  }
}

// openIndexView switches the reader into the markdown-rendered index
// overview. Re-uses the intro already fetched during loadPages(); only
// re-fetches on first ever open or if a prior attempt failed.
async function openIndexView() {
  selectedPage.value = null
  activeSystemView.value = 'index'
  if (!indexMarkdown.value) {
    indexLoading.value = true
    try {
      await loadIndexAndLog()
    } finally {
      indexLoading.value = false
    }
  }
  // Observer is mounted/unmounted from a watch on activeSystemView
  // + indexSentinelRef below, so nothing else to do here — entering
  // the view is a render-time concern.
}

// loadMoreIndexSection advances the directory one step forward. The
// order is fixed (Summary → Entity → Concept → …); within a section we
// paginate with the backend's cursor, and only move to the next section
// when the current one is exhausted. Each call produces one network
// round trip that appends a markdown block to indexMarkdown.
//
// Rendering is append-only markdown rather than a structured list so
// the viewer feels like a regular wiki page — [[wiki-link]] clicks flow
// through handleContentClick just like every other page body. Entries
// are rendered as plain lines (not list items) so the reader doesn't
// carry list bullets next to every link.
async function loadMoreIndexSection() {
  if (indexLoading.value) return
  if (indexSectionIdx.value >= INDEX_SECTION_ORDER.length) return

  const type = INDEX_SECTION_ORDER[indexSectionIdx.value]
  const state = indexSections.value[type] || { loaded: false, cursor: '', total: 0 }
  const isFirstChunkOfSection = !state.loaded

  indexLoading.value = true
  try {
    const res = await getWikiIndex(props.knowledgeBaseId, {
      types: [type],
      limit: 50,
      cursor: isFirstChunkOfSection ? undefined : state.cursor || undefined,
    })
    const body: any = (res as any).data || (res as any)
    const group = (body?.groups || []).find((g: WikiIndexGroup) => g.type === type)

    const items: WikiIndexEntryDTO[] = group?.items || []
    const total: number = group?.total || 0
    const nextCursor: string = group?.next_cursor || ''

    // Only emit a section heading the first time we see entries for a
    // type. An empty section is skipped entirely so the reader doesn't
    // see "## Entity (0)" for a KB with no entities.
    let appended = ''
    if (isFirstChunkOfSection && items.length > 0) {
      const label = getTypeLabel(type)
      appended += `\n## ${label} (${total})\n\n`
    }
    for (const entry of items) {
      // Plain lines rather than `- [[slug]]`: marked renders the latter
      // as <ul><li>, which adds a disc bullet before every slug. Each
      // entry occupies one line thanks to `breaks: true` in renderMarkdown.
      //
      // Use the `[[slug|display]]` form so the anchor text shows the
      // human-readable title (e.g. "东城区") instead of the URL-safe slug
      // ("entity/dongcheng-qu"). The [[ ]] preprocessor in renderMarkdown
      // splits on the pipe and uses the right-hand side for display text
      // while the left-hand side drives navigation via handleContentClick.
      //
      // Fall back to the slug when the page has no title — the backend
      // guarantees title is non-empty for published pages, but drafts
      // or partially-indexed pages can slip through.
      const display = entry.title || entry.slug
      if (entry.summary) {
        appended += `[[${entry.slug}|${display}]] — ${entry.summary}\n`
      } else {
        appended += `[[${entry.slug}|${display}]]\n`
      }
    }
    if (appended) {
      indexMarkdown.value = indexMarkdown.value + appended
    }

    indexSections.value[type] = {
      loaded: true,
      cursor: nextCursor,
      total,
    }

    // Advance to the next section when this one has no more pages.
    // When the section is flat-out empty (total === 0), skip the
    // heading entirely and move on without emitting any markdown.
    if (!nextCursor) {
      indexSectionIdx.value += 1
    }
  } catch (e) {
    console.error(`Failed to load more index entries for ${INDEX_SECTION_ORDER[indexSectionIdx.value]}:`, e)
  } finally {
    indexLoading.value = false
  }

  // IntersectionObserver does NOT re-fire while the target stays
  // continuously intersecting. On small KBs (say a wiki with only
  // 3 summary pages and no entities / concepts) the sentinel sits
  // inside the viewport from the moment we finish the first section,
  // so without this nudge the remaining sections would never load.
  //
  // After every append we yield a tick (so the DOM reflows and the
  // sentinel's new rect is valid) then re-check: if it's still in
  // view and we have more to load, recurse. The recursion bottoms
  // out when either hasMore turns off or the sentinel is pushed
  // below the fold by the accumulated entries.
  await nextTick()
  if (indexHasMore.value && sentinelInView()) {
    loadMoreIndexSection()
  }
}

// sentinelInView reports whether the load sentinel's rect currently
// overlaps the viewport (± the same 200px cushion the observer uses),
// so after loading a section we know whether to drain another round
// even though the observer itself won't fire again while the target
// stays visible.
function sentinelInView(): boolean {
  const el = indexSentinelRef.value
  if (!el) return false
  const rect = el.getBoundingClientRect()
  const vh = window.innerHeight || document.documentElement.clientHeight
  // 200px margin matches the observer's rootMargin so the drain
  // threshold and the scroll-triggered threshold stay consistent.
  return rect.top < vh + 200 && rect.bottom > -200
}

// Mount/unmount the IntersectionObserver around the Index sentinel.
// We use rootMargin to pre-load when the user scrolls within ~200px
// of the sentinel, which hides the network round-trip behind the
// scroll motion.
watch([indexSentinelRef, () => activeSystemView.value], async ([el, view]) => {
  if (indexObserver) {
    indexObserver.disconnect()
    indexObserver = null
  }
  if (view !== 'index' || !el) return
  indexObserver = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (entry.isIntersecting && indexHasMore.value && !indexLoading.value) {
        loadMoreIndexSection()
      }
    }
  }, { rootMargin: '200px' })
  indexObserver.observe(el)
})

onUnmounted(() => {
  if (indexObserver) {
    indexObserver.disconnect()
    indexObserver = null
  }
})

// openLogView switches the reader into the log feed and (re)loads the
// first page. Called when the user clicks the sidebar Log entry.
async function openLogView() {
  selectedPage.value = null
  activeSystemView.value = 'log'
  logEntries.value = []
  logNextCursor.value = ''
  logInitialized.value = false
  await loadMoreLog()
}

// loadMoreLog appends the next page of log entries using the cursor from
// the previous response. Guarded so overlapping scroll events don't fire
// multiple requests and double-append entries.
async function loadMoreLog() {
  if (logLoading.value) return
  // Once a previous request reported end-of-feed (empty next_cursor), we
  // stop — but only after the first fetch, so a fresh KB still runs the
  // initial empty request to populate logInitialized.
  if (logInitialized.value && !logNextCursor.value) return
  logLoading.value = true
  try {
    const res = await getWikiLog(props.knowledgeBaseId, {
      cursor: logNextCursor.value || undefined,
      limit: 50,
    })
    const body: any = (res as any).data || res
    const entries: WikiLogEntry[] = body?.entries || []
    logEntries.value.push(...entries)
    logNextCursor.value = body?.next_cursor || ''
    logInitialized.value = true
  } catch (e) {
    console.error('Failed to load wiki log:', e)
  } finally {
    logLoading.value = false
  }
}

// loadPages is the sidebar's top-level initialization. It wires up the
// empty buckets (so groupedPages produces stable group slots even
// before any fetch completes), pulls the pinned system pages, and then
// kicks off the first page of each content type bucket in parallel —
// cheap because each bucket caps at WIKI_SIDEBAR_PAGE_SIZE rows.
//
// Historically this function looped listWikiPages({page:1..50}) and
// accumulated up to 25k rows in `pages.value`. On a 4万-page KB that
// was multiple seconds of network + serialization + O(n) group
// computation before the user saw anything.
async function loadPages() {
  loading.value = true
  try {
    searchResults.value = null
    for (const type of CONTENT_PAGE_TYPES) ensureBucket(type)
    await loadIndexAndLog()
    await Promise.all(CONTENT_PAGE_TYPES.map(type => loadPagesForType(type, { reset: true })))

    // Pick the first non-empty bucket as the default active tab.
    // Keeping `activeTab` unset while buckets are still loading would
    // momentarily render no list at all, so we only overwrite it when
    // the current selection is empty or missing.
    if (!activeTab.value || !pagesByType.value[activeTab.value] || pagesByType.value[activeTab.value].total === 0) {
      for (const type of CONTENT_PAGE_TYPES) {
        const bucket = pagesByType.value[type]
        if (bucket && bucket.total > 0) {
          activeTab.value = type
          break
        }
      }
    }

    // Auto-select based on query string or default to the index
    // overview. The index is the natural landing view — it shows
    // intro + a paginated directory of every page type.
    if (!selectedPage.value && activeSystemView.value === '') {
      if (route.query.slug && typeof route.query.slug === 'string') {
        navigateToSlug(route.query.slug)
      } else if (indexAvailable.value) {
        openIndexView()
      }
    }
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

// graphFilterTypesToArray returns the active allow-list as an array, or
// `undefined` when every known type is selected (in which case we want
// the backend to rank over the full page population, not a subset).
// Callers must check for "no types selected at all" separately and avoid
// the fetch — passing an empty string list to the backend is ambiguous
// there (empty == no filter == return everything, the opposite of what
// the user meant).
function graphFilterTypesToArray(): string[] | undefined {
  const all = ['summary', 'entity', 'concept', 'synthesis', 'comparison', 'index', 'log']
  if (all.every(t => graphFilterTypes.value.has(t))) {
    return undefined
  }
  return Array.from(graphFilterTypes.value)
}

function graphFilterSelectsNothing(): boolean {
  return graphFilterTypes.value.size === 0
}

async function loadGraph() {
  graphLoading.value = true
  graphReady.value = false
  graphMode.value = 'overview'
  graphCenter.value = ''
  if (graphFilterSelectsNothing()) {
    // User has deselected every type — render an empty canvas without
    // hitting the backend.
    graphData.value = { nodes: [], edges: [], meta: { mode: 'overview', total: 0, returned: 0, truncated: false } }
    await nextTick()
    renderGraph()
    graphLoading.value = false
    return
  }
  try {
    const res = await getWikiGraph(props.knowledgeBaseId, {
      mode: 'overview',
      limit: GRAPH_OVERVIEW_LIMIT,
      types: graphFilterTypesToArray(),
    })
    graphData.value = (res as any).data || res as any
    // Seed the search dropdown's empty-state with this overview snapshot
    // so opening the select without typing shows the top-500 by link_count
    // — matching what the old client-filter dropdown used to surface.
    // We re-seed on every overview load so filter toggles / KB changes
    // propagate; ego loads intentionally skip seeding so drilling into a
    // neighborhood doesn't shrink the default dropdown to a 20-node subgraph.
    setGraphSearchDefaultFromNodes(graphData.value?.nodes)
    // Returning to overview clears accumulated bloom state; the next ego
    // dive should start fresh rather than inherit an orphan generation map.
    resetBloomGenerations(graphData.value?.nodes)
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

// loadEgoGraph fetches the neighborhood around a center slug and re-renders
// the canvas. Invoked when the user clicks "expand neighbors" in the drawer
// so they can drill into a page on a 4万+ wiki without ever having to
// download the full graph. Returning to the global top-N view is handled by
// loadGraph() again.
async function loadEgoGraph(slug: string, depth = GRAPH_EGO_DEFAULT_DEPTH) {
  if (!slug) return
  graphLoading.value = true
  graphReady.value = false
  if (graphFilterSelectsNothing()) {
    graphData.value = { nodes: [], edges: [], meta: { mode: 'ego', total: 0, returned: 0, truncated: false, center: slug, depth } }
    graphMode.value = 'ego'
    graphCenter.value = slug
    resetBloomGenerations(graphData.value.nodes)
    await nextTick()
    renderGraph()
    graphLoading.value = false
    return
  }
  try {
    const res = await getWikiGraph(props.knowledgeBaseId, {
      mode: 'ego',
      center: slug,
      depth,
      limit: GRAPH_EGO_LIMIT,
      types: graphFilterTypesToArray(),
    })
    graphData.value = (res as any).data || res as any
    graphMode.value = 'ego'
    graphCenter.value = slug
    // Entering (or re-entering) a fresh ego view resets the bloom
    // generation counter — we're no longer accumulating on top of the
    // previous canvas, so every node belongs to generation 0.
    resetBloomGenerations(graphData.value?.nodes)
    await nextTick()
    renderGraph()
    // After a fresh ego render, preselect the center so the highlight /
    // drawer context matches what the user just asked for.
    graphSelectedSlug.value = slug
  } catch (e) {
    console.error(`Failed to load ego graph for ${slug}:`, e)
  } finally {
    graphLoading.value = false
  }
}

// ─── Bloom: additive neighbor expansion ──────────────────────────────────
//
// While loadEgoGraph replaces the canvas with a fresh ego view, bloom lets
// the user add a second (or Nth) ego around a neighbor WITHOUT losing the
// nodes already on screen. This matches how humans explore a knowledge
// graph interactively — "show me what's around A", "now also show me
// what's around B, but keep A visible for context".
//
// Three pieces of state cooperate:
//   - bloomGenerations: slug -> generation number. Generation 0 is the
//     initial ego view; each bloom increments a counter and tags the
//     newly arrived nodes with that generation. LRU eviction walks by
//     generation, oldest first.
//   - BLOOM_MAX_NODES: hard cap on rendered nodes. Past this point each
//     bloom triggers LRU eviction to keep the force simulation responsive.
//   - We reuse the existing graphData.value as the accumulator — new ego
//     responses are merged into it in place, then handed back to renderGraph
//     in preserveLayout mode.
const BLOOM_MAX_NODES = 1500
const bloomGenerations = new Map<string, number>()
let bloomCurrentGeneration = 0

function resetBloomGenerations(nodes: { slug: string }[] | undefined) {
  bloomGenerations.clear()
  bloomCurrentGeneration = 0
  if (!nodes) return
  for (const n of nodes) {
    bloomGenerations.set(n.slug, 0)
  }
}

async function loadBloomNeighbors(anchorSlug: string, depth = GRAPH_EGO_DEFAULT_DEPTH) {
  if (!anchorSlug) return
  if (!graphData.value) return
  if (graphMode.value !== 'ego') {
    // Bloom only makes sense on top of an ego view. If we're still on the
    // overview, reuse loadEgoGraph to pivot cleanly — that's a less
    // surprising outcome than no-oping.
    await loadEgoGraph(anchorSlug, depth)
    return
  }
  graphLoading.value = true
  try {
    const res = await getWikiGraph(props.knowledgeBaseId, {
      mode: 'ego',
      center: anchorSlug,
      depth,
      limit: GRAPH_EGO_LIMIT,
      types: graphFilterTypesToArray(),
    })
    const incoming = (res as any).data || res as any
    if (!incoming || !Array.isArray(incoming.nodes)) return

    bloomCurrentGeneration += 1
    const merged = mergeGraphData(graphData.value, incoming, bloomCurrentGeneration)
    // Evict the oldest bloom generations if we've blown through the cap.
    // We never evict the ego center (the original anchor of the session),
    // the most recent bloom anchor, or the currently selected node — the
    // user's mental anchors must stay on screen.
    const protect = new Set<string>([
      graphCenter.value,
      anchorSlug,
      graphSelectedSlug.value || '',
    ].filter(Boolean))
    evictBloomOverflow(merged, protect)

    graphData.value = merged
    await nextTick()
    renderGraph({ preserveLayout: true, anchorSlug })
  } catch (e) {
    console.error(`Failed to bloom neighbors for ${anchorSlug}:`, e)
  } finally {
    graphLoading.value = false
  }
}

// mergeGraphData folds `incoming` into `base` in-place-style (returns a
// new object for Vue reactivity but shares page node shape). Dedupes
// nodes by slug and edges by (source, target). New node slugs are tagged
// with `gen` so LRU knows which generation they belong to.
function mergeGraphData(
  base: WikiGraphData,
  incoming: WikiGraphData,
  gen: number,
): WikiGraphData {
  const nodeBySlug = new Map<string, WikiGraphData['nodes'][number]>()
  for (const n of base.nodes) nodeBySlug.set(n.slug, n)
  for (const n of incoming.nodes) {
    if (!nodeBySlug.has(n.slug)) {
      nodeBySlug.set(n.slug, n)
      bloomGenerations.set(n.slug, gen)
    }
  }
  const edgeKey = (e: { source: string; target: string }) => `${e.source}→${e.target}`
  const edgeSeen = new Set<string>()
  const edges: WikiGraphData['edges'] = []
  for (const e of base.edges) {
    const k = edgeKey(e)
    if (!edgeSeen.has(k)) { edgeSeen.add(k); edges.push(e) }
  }
  for (const e of incoming.edges) {
    const k = edgeKey(e)
    if (!edgeSeen.has(k)) { edgeSeen.add(k); edges.push(e) }
  }
  return {
    nodes: Array.from(nodeBySlug.values()),
    edges,
    meta: {
      // Meta from the latest ego response describes the most recent
      // bloom, but we keep the overview denominator so the truncation
      // hint still reflects the KB-wide total.
      ...incoming.meta,
      returned: nodeBySlug.size,
    },
  }
}

// evictBloomOverflow walks generations oldest-first and drops nodes
// (plus their incident edges) until the total fits under BLOOM_MAX_NODES.
// `protect` holds slugs that must never be evicted (current center, most
// recent bloom anchor, current selection). Generation-0 nodes are
// protected too — those are the original ego view the user started with.
function evictBloomOverflow(data: WikiGraphData, protect: Set<string>) {
  if (data.nodes.length <= BLOOM_MAX_NODES) return

  // Group slugs by generation descending-safe: we only evict gen >= 1.
  const byGen = new Map<number, string[]>()
  for (const n of data.nodes) {
    const g = bloomGenerations.get(n.slug) ?? 0
    if (g === 0) continue
    if (protect.has(n.slug)) continue
    if (!byGen.has(g)) byGen.set(g, [])
    byGen.get(g)!.push(n.slug)
  }
  const gens = Array.from(byGen.keys()).sort((a, b) => a - b)

  const toRemove = new Set<string>()
  let remaining = data.nodes.length
  for (const g of gens) {
    if (remaining <= BLOOM_MAX_NODES) break
    for (const slug of byGen.get(g)!) {
      if (remaining <= BLOOM_MAX_NODES) break
      toRemove.add(slug)
      remaining -= 1
    }
  }
  if (toRemove.size === 0) return

  data.nodes = data.nodes.filter(n => !toRemove.has(n.slug))
  data.edges = data.edges.filter(e => !toRemove.has(e.source) && !toRemove.has(e.target))
  for (const slug of toRemove) bloomGenerations.delete(slug)
}

// GROW_FRONTIER_CONCURRENCY is the number of parallel ego fetches we
// allow when the user asks us to expand the whole frontier at once. A
// 4万-page wiki can have ~100 frontier nodes; firing all 100 requests in
// parallel would hammer the backend and most responses would compete for
// the same DB connection pool anyway. 6 is chosen empirically: it keeps
// latency for the "whole frontier" op under ~2s for typical frontiers
// without spiking DB CPU.
const GROW_FRONTIER_CONCURRENCY = 6

// GRAPH_SYSTEM_PAGE_TYPES are wiki page types that act as index-of-the-
// whole-KB rather than content nodes. They link out to every document
// page by design, so treating them as part of the frontier would cause
// one "Grow frontier" click to dump the entire wiki onto the canvas —
// exactly the opposite of what the user asked for ("show me more of the
// interesting neighborhood"). We keep them visible and individually
// expandable (double-click / shift-click / ⊕ all still work), but they
// don't participate in batch expansion.
const GRAPH_SYSTEM_PAGE_TYPES = new Set(['index', 'log'])

function isFrontierCandidate(
  node: { slug: string; page_type: string; link_count: number },
  centerSlug: string,
  visibleDegree: number,
): boolean {
  if (node.slug === centerSlug) return false
  if (GRAPH_SYSTEM_PAGE_TYPES.has(node.page_type)) return false
  return (node.link_count || 0) > visibleDegree
}

// growFrontier is the "one-click expand everything" operator. It finds
// every visible node that currently has an expansion ring (visible < link_count,
// not the ego center, not an Index/Log super-node), fires parallel ego
// fetches for them, merges all responses together and repaints the canvas
// preserving layout. This is the batch cousin of loadBloomNeighbors —
// one click grows the canvas along every branch instead of 100 individual
// click-by-click iterations.
async function growFrontier() {
  if (!graphData.value) return
  if (graphMode.value !== 'ego') {
    // Frontier expansion only makes sense on top of an ego layout.
    // Overview has its own pivot mechanism (expand a single node).
    return
  }
  if (graphFilterSelectsNothing()) return

  // Collect frontier nodes: visible degree < link_count AND not the ego
  // center AND not a system super-node. We compute visible degree inline
  // from edges so we don't depend on the stale adjacency snapshot from
  // the last render.
  const visibleDegree = new Map<string, number>()
  for (const e of graphData.value.edges) {
    visibleDegree.set(e.source, (visibleDegree.get(e.source) ?? 0) + 1)
    visibleDegree.set(e.target, (visibleDegree.get(e.target) ?? 0) + 1)
  }
  const frontier: string[] = []
  for (const n of graphData.value.nodes) {
    if (isFrontierCandidate(n, graphCenter.value, visibleDegree.get(n.slug) ?? 0)) {
      frontier.push(n.slug)
    }
  }
  if (frontier.length === 0) return

  graphLoading.value = true
  try {
    // Concurrency-limited fan-out. We collect responses in order of
    // completion (doesn't matter — merge is commutative on the edge /
    // node sets) and ignore individual failures so one slow/broken node
    // doesn't sink the whole batch.
    const responses: WikiGraphData[] = []
    let cursor = 0
    async function worker() {
      while (cursor < frontier.length) {
        const idx = cursor++
        const slug = frontier[idx]
        try {
          const res = await getWikiGraph(props.knowledgeBaseId, {
            mode: 'ego',
            center: slug,
            depth: GRAPH_EGO_DEFAULT_DEPTH,
            limit: GRAPH_EGO_LIMIT,
            types: graphFilterTypesToArray(),
          })
          const data = (res as any).data || res as any
          if (data?.nodes) responses.push(data)
        } catch (e) {
          console.error(`growFrontier: ego fetch failed for ${slug}:`, e)
        }
      }
    }
    const workers: Promise<void>[] = []
    const workerCount = Math.min(GROW_FRONTIER_CONCURRENCY, frontier.length)
    for (let i = 0; i < workerCount; i++) workers.push(worker())
    await Promise.all(workers)

    if (responses.length === 0) return

    // All new arrivals belong to a single bloom generation — the user
    // performed one logical action, so LRU should evict them together.
    bloomCurrentGeneration += 1
    const gen = bloomCurrentGeneration
    let merged = graphData.value
    for (const incoming of responses) {
      merged = mergeGraphData(merged, incoming, gen)
    }
    const protect = new Set<string>([
      graphCenter.value,
      graphSelectedSlug.value || '',
    ].filter(Boolean))
    evictBloomOverflow(merged, protect)

    graphData.value = merged
    await nextTick()
    // anchorSlug intentionally omitted — new nodes have no single natural
    // landing point, so we fall back to random canvas-center placement
    // and let the force simulation untangle them.
    renderGraph({ preserveLayout: true })
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
    } else if (!selectedPage.value && activeSystemView.value) {
      // Jumping out of a system view (Index / Log) onto a page.
      // navHistory only holds WikiPages, so we stash the origin
      // system view separately; goBack restores it when the history
      // stack is empty.
      navFromSystemView.value = activeSystemView.value
    }
    activeSystemView.value = ''
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
    } else if (!selectedPage.value && activeSystemView.value) {
      // Clicking a [[slug]] from inside Index / Log — same rationale
      // as selectPage above: record the system-view origin so the
      // reader's back arrow can return to it.
      navFromSystemView.value = activeSystemView.value
    }
    activeSystemView.value = ''
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
    return
  }
  // History stack is empty but we remember the page was opened from
  // a system view — restore that instead of leaving the reader empty.
  if (navFromSystemView.value) {
    const view = navFromSystemView.value
    navFromSystemView.value = ''
    selectedPage.value = null
    if (view === 'index') {
      openIndexView()
    } else if (view === 'log') {
      openLogView()
    }
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
  if (!searchQuery.value.trim()) {
    searchResults.value = null
    return
  }
  loading.value = true
  try {
    const res = await searchWikiPages(props.knowledgeBaseId, searchQuery.value)
    const hits: WikiPage[] = (res as any).data?.pages || (res as any).pages || []
    searchResults.value = hits
    // Also seed `pages.value` with hits so slugDisplayName / navigation
    // heuristics keep resolving titles correctly without re-fetching.
    const seen = new Set(pages.value.map(p => p.id))
    for (const p of hits) {
      if (!seen.has(p.id)) pages.value.push(p)
    }
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

// RenderGraphOpts tweaks how renderGraph initializes node positions when
// repainting the canvas. The default (no opts) does a full layout reset —
// every node gets a fresh circular starting position and the force
// simulation runs from scratch. With `preserveLayout: true` we reuse the
// x/y/vx/vy of any node that already existed in the previous graphNodes
// list, and only new nodes get initial positions. This is what the
// "bloom neighbors" interaction needs: when the user expands a second
// ego around a neighbor, the nodes they already see don't jump to new
// positions — only the newly arrived neighbors fly in.
interface RenderGraphOpts {
  preserveLayout?: boolean
  // anchorSlug: if set and the node is new, it is placed near the anchor
  // with a small random jitter so related nodes visually land together.
  anchorSlug?: string
}

function renderGraph(opts: RenderGraphOpts = {}) {
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

  // Snapshot prior node coordinates before we rebuild graphNodes. Used
  // when preserveLayout is true to avoid the whole canvas jumping during
  // an incremental bloom.
  const priorCoords = new Map<string, { x: number; y: number; vx: number; vy: number; pinned: boolean }>()
  if (opts.preserveLayout) {
    for (const n of graphNodes) {
      priorCoords.set(n.slug, { x: n.x, y: n.y, vx: n.vx, vy: n.vy, pinned: n.pinned })
    }
  }

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

  // Locate the anchor's prior coordinates so new nodes land near it in
  // bloom mode. Falls back to canvas center if the anchor is itself new
  // (e.g. ego was pivoted rather than bloomed).
  const anchorCoord = opts.anchorSlug ? priorCoords.get(opts.anchorSlug) : undefined
  const anchorX = anchorCoord?.x ?? width / 2
  const anchorY = anchorCoord?.y ?? height / 2

  // Build nodes
  const nodeMap = new Map<string, GNode>()
  graphNodes = data.nodes.map((n, i) => {
    const prior = opts.preserveLayout ? priorCoords.get(n.slug) : undefined
    let x: number
    let y: number
    let vx: number
    let vy: number
    let pinned: boolean
    if (prior) {
      // Reuse the node's existing position so the user's mental map of
      // the canvas stays stable across bloom iterations.
      x = prior.x
      y = prior.y
      vx = prior.vx
      vy = prior.vy
      pinned = prior.pinned
    } else if (opts.preserveLayout && opts.anchorSlug) {
      // New node arriving during a bloom — spawn it right next to the
      // anchor with a small random kick so the force simulation pushes
      // it into place alongside its siblings.
      const jitterR = 40
      const angle = Math.random() * Math.PI * 2
      x = anchorX + jitterR * Math.cos(angle)
      y = anchorY + jitterR * Math.sin(angle)
      vx = 0
      vy = 0
      pinned = false
    } else {
      // Full repaint — classic circular layout.
      const angle = (2 * Math.PI * i) / data.nodes.length
      const r = Math.min(width, height) * 0.35
      x = width / 2 + r * Math.cos(angle) + (Math.random() - 0.5) * 50
      y = height / 2 + r * Math.sin(angle) + (Math.random() - 0.5) * 50
      vx = 0
      vy = 0
      pinned = false
    }
    const node: GNode = {
      x, y, vx, vy,
      slug: n.slug, title: n.title, type: n.page_type,
      linkCount: n.link_count || 0, pinned,
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

    // Expansion hint ring — dashed outer circle that appears when the
    // node has neighbors the user hasn't loaded yet. Without this signal
    // users have no way to tell a fully-explored node from one that's
    // still hiding 80 more connections just out of view, so they either
    // click "bloom" on everything (wasteful) or on nothing (miss the
    // interesting pages). adjacency here is the undirected neighbor set
    // we've already built from data.edges; link_count is the KB-wide
    // in+out degree reported by the backend. Diff > 0 means there's
    // more to fetch.
    //
    // Exception: the ego-mode center node already received every
    // reachable neighbor from the BFS expansion, so any remaining gap
    // against link_count is dead refs / filtered pages, NOT loadable
    // neighbors. Drawing a dashed ring there would mislead users into
    // thinking there's something to click.
    const visibleNeighbors = adjacency.get(n.slug)?.size ?? 0
    const hiddenNeighbors = Math.max(0, n.linkCount - visibleNeighbors)
    const isEgoCenter = data.meta?.mode === 'ego' && data.meta.center === n.slug
    const showExpansionRing = hiddenNeighbors > 0 && !isEgoCenter
    const expansionRing = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
    expansionRing.setAttribute('r', String(r + 3))
    expansionRing.setAttribute('fill', 'none')
    expansionRing.setAttribute('stroke', nodeColorMap[n.type] || '#8c8c8c')
    expansionRing.setAttribute('stroke-width', '1.5')
    expansionRing.setAttribute('stroke-dasharray', '3 3')
    expansionRing.setAttribute('pointer-events', 'none')
    expansionRing.style.opacity = showExpansionRing ? '0.55' : '0'
    expansionRing.style.transition = 'opacity 0.2s'
    expansionRing.classList.add('node-expansion-ring')
    g.appendChild(expansionRing)

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

    // Hover bloom button — the ⊕ badge floating off the node's upper-right.
    // Invisible by default; fades in on mouseenter when bloom would
    // actually do something (node has hidden neighbors and isn't the ego
    // center / isn't on overview). Clicking it skips the drawer round-trip
    // and pulls the neighbors straight onto the canvas.
    //
    // Stacking order note: this element has to come AFTER text so SVG's
    // painter's algorithm draws it on top; the node-shadow filter and
    // the drawer cover it otherwise.
    let bloomBtn: SVGGElement | null = null
    const bloomBtnEligible = !isEgoCenter && data.meta?.mode === 'ego' && hiddenNeighbors > 0
    if (bloomBtnEligible) {
      bloomBtn = document.createElementNS('http://www.w3.org/2000/svg', 'g')
      bloomBtn.classList.add('node-bloom-btn')
      bloomBtn.style.opacity = '0'
      bloomBtn.style.transition = 'opacity 0.15s'
      bloomBtn.style.pointerEvents = 'none' // lit up only on hover
      bloomBtn.style.cursor = 'pointer'
      // Position at 45° up-right of the node center, just past the
      // expansion ring so it doesn't overlap the node glyph.
      const btnOffset = r + 6
      const btnX = Math.SQRT1_2 * btnOffset
      const btnY = -Math.SQRT1_2 * btnOffset

      const btnBg = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
      btnBg.setAttribute('cx', String(btnX))
      btnBg.setAttribute('cy', String(btnY))
      btnBg.setAttribute('r', '8')
      btnBg.setAttribute('fill', 'var(--td-bg-color-container, #fff)')
      btnBg.setAttribute('stroke', 'var(--td-brand-color, #0052d9)')
      btnBg.setAttribute('stroke-width', '1.5')
      bloomBtn.appendChild(btnBg)

      // ⊕ drawn as two short lines — cross-browser-safer than a text glyph
      const btnCrossV = document.createElementNS('http://www.w3.org/2000/svg', 'line')
      btnCrossV.setAttribute('x1', String(btnX))
      btnCrossV.setAttribute('x2', String(btnX))
      btnCrossV.setAttribute('y1', String(btnY - 4))
      btnCrossV.setAttribute('y2', String(btnY + 4))
      btnCrossV.setAttribute('stroke', 'var(--td-brand-color, #0052d9)')
      btnCrossV.setAttribute('stroke-width', '1.8')
      btnCrossV.setAttribute('stroke-linecap', 'round')
      bloomBtn.appendChild(btnCrossV)

      const btnCrossH = document.createElementNS('http://www.w3.org/2000/svg', 'line')
      btnCrossH.setAttribute('x1', String(btnX - 4))
      btnCrossH.setAttribute('x2', String(btnX + 4))
      btnCrossH.setAttribute('y1', String(btnY))
      btnCrossH.setAttribute('y2', String(btnY))
      btnCrossH.setAttribute('stroke', 'var(--td-brand-color, #0052d9)')
      btnCrossH.setAttribute('stroke-width', '1.8')
      btnCrossH.setAttribute('stroke-linecap', 'round')
      bloomBtn.appendChild(btnCrossH)

      bloomBtn.addEventListener('click', (e) => {
        e.stopPropagation()
        loadBloomNeighbors(n.slug)
      })
      g.appendChild(bloomBtn)
    }

    // Hover highlight
    // We debounce the "leave" side so that quickly sliding the pointer from
    // one node to the next doesn't flash through the fully-unhighlighted state
    // (which is what caused the whole-graph flickering).
    g.addEventListener('mouseenter', () => {
      if (graphHoverLeaveTimer) {
        clearTimeout(graphHoverLeaveTimer)
        graphHoverLeaveTimer = null
      }
      if (bloomBtn) {
        bloomBtn.style.opacity = '1'
        bloomBtn.style.pointerEvents = 'auto'
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
      if (bloomBtn) {
        bloomBtn.style.opacity = '0'
        bloomBtn.style.pointerEvents = 'none'
      }
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

    // Single-click behaviour + keyboard-modifier shortcuts to skip the
    // drawer round-trip for power-user navigation:
    //
    //   plain click  → select & open drawer (original behaviour)
    //   shift+click  → bloom this node's neighbors onto the canvas
    //   double-click → pivot to this node as the new ego center
    //
    // Drawer is by far the slower path (page fetch + render), so adding
    // canvas-direct expand / bloom removes a 2-3 second round-trip from
    // every exploration step. We still want shift+click to be
    // discoverable, so the drawer's buttons remain — they're the
    // keyboard-free fallback.
    //
    // Implementation note: we listen to click AND dblclick. The browser
    // fires both click events of a dblclick too, but we debounce via
    // `pendingSingleClick` — the first click sets a 220ms timer to open
    // the drawer; dblclick arriving inside that window cancels the
    // timer and runs expand instead. `event.detail` (click count) is
    // less portable across synthetic events, so we track state explicitly.
    let pendingSingleClick: ReturnType<typeof setTimeout> | null = null
    g.addEventListener('click', (e) => {
      e.stopPropagation()

      if (e.shiftKey) {
        // Shift = Bloom. Skip the drawer entirely, skip selection — the
        // user's intent is "bring in the neighbors", not "read this page".
        // Center / isOverview cases are handled inside loadBloomNeighbors
        // (center no-ops, overview pivots to ego).
        if (pendingSingleClick) { clearTimeout(pendingSingleClick); pendingSingleClick = null }
        loadBloomNeighbors(n.slug)
        return
      }

      if (pendingSingleClick) clearTimeout(pendingSingleClick)
      pendingSingleClick = setTimeout(() => {
        pendingSingleClick = null

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
      }, 220)
    })

    g.addEventListener('dblclick', (e) => {
      e.stopPropagation()
      if (pendingSingleClick) { clearTimeout(pendingSingleClick); pendingSingleClick = null }
      loadEgoGraph(n.slug)
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

// graphSearchOptions drives the search select dropdown. When the input is
// empty we fall back to the overview top-500 snapshot so users can still
// browse the most-connected pages without typing — matching the old
// client-filter UX. Once the user types we switch to a remote full-text
// search against the wiki API so the dropdown can reach pages that sit
// outside the canvas (up to the whole 4万-page KB).
const graphSearchOptions = ref<{ label: string; value: string }[]>([])
const graphSearchLoading = ref(false)
let graphSearchDebounce: ReturnType<typeof setTimeout> | null = null
let graphSearchSeq = 0

// graphSearchDefaultOptions is the snapshot of "global top-500 by link_count"
// used as the empty-keyword default. We populate it lazily from the first
// overview fetch and keep it across ego-mode navigations so drilling into
// a neighborhood doesn't shrink the search surface back to the ego subgraph.
const graphSearchDefaultOptions = ref<{ label: string; value: string }[]>([])

// Expose the empty-state list to the template too, so the initial popup
// open (before the user types) renders the snapshot immediately. Using a
// computed keeps graphSearchOptions.value representing "current keyword
// results" without having to remember which list is active.
const graphSearchEffectiveOptions = computed(() => {
  return graphSearchOptions.value.length > 0
    ? graphSearchOptions.value
    : graphSearchDefaultOptions.value
})

function setGraphSearchDefaultFromNodes(nodes: { slug: string; title: string }[] | undefined) {
  if (!nodes) return
  graphSearchDefaultOptions.value = nodes.map(n => ({ label: n.title, value: n.slug }))
}

async function handleGraphRemoteSearch(keyword: string) {
  const q = (keyword || '').trim()
  if (graphSearchDebounce) {
    clearTimeout(graphSearchDebounce)
    graphSearchDebounce = null
  }
  if (!q) {
    // No keyword — clear keyword-specific results; the computed
    // graphSearchEffectiveOptions will fall back to the top-500 snapshot.
    graphSearchOptions.value = []
    graphSearchLoading.value = false
    return
  }
  graphSearchLoading.value = true
  // Snapshot a monotonic sequence number so stale responses (user kept
  // typing while an earlier request was still in flight) don't overwrite
  // newer results with older ones.
  const seq = ++graphSearchSeq
  graphSearchDebounce = setTimeout(async () => {
    try {
      const res = await searchWikiPages(props.knowledgeBaseId, q, 20)
      if (seq !== graphSearchSeq) return
      const pages: WikiPage[] = (res as any)?.data?.pages || (res as any)?.pages || []
      graphSearchOptions.value = pages.map(p => ({ label: p.title, value: p.slug }))
    } catch (e) {
      if (seq !== graphSearchSeq) return
      console.error('Wiki search failed:', e)
      graphSearchOptions.value = []
    } finally {
      if (seq === graphSearchSeq) graphSearchLoading.value = false
    }
  }, 200)
}

let graphNodeElsRef: { g: SVGGElement; circle: SVGCircleElement; text: SVGTextElement; activeRing: SVGCircleElement; node: GNode }[] = []
let graphEdgeElsRef: { line: SVGLineElement; source: string; target: string; bidir: boolean }[] = []
let graphAdjacencyRef = new Map<string, Set<string>>()

// handleGraphSearchSelect is the single entry point every "jump to this
// slug" path funnels through — the graph search select, drawer wiki-link
// clicks, the ?slug= query param, and the global issues "去处理" button.
// On a 4万-page wiki, the current render contains at most GRAPH_OVERVIEW_LIMIT
// (500) nodes, so most of the wiki is NOT on screen at any given moment.
// If the requested slug is missing from the current canvas we reload the
// graph as an ego view centered on that slug, then finish the highlight
// and drawer flow once the new render is ready. This guarantees any
// navigable link can actually reach its destination regardless of where
// the target sits in the link_count ranking.
async function handleGraphSearchSelect(value: string) {
  if (!value) return

  let node = graphNodes.find(n => n.slug === value)
  if (!node) {
    // Target is outside the current subgraph — pivot to an ego view.
    // loadEgoGraph repopulates graphNodes as a side effect.
    await loadEgoGraph(value)
    node = graphNodes.find(n => n.slug === value)
    if (!node) {
      // The slug truly does not exist in the KB (e.g. stale URL, deleted
      // page). loadEgoGraph will have surfaced the backend error in the
      // console; still open the drawer so the user sees the not-found
      // page body rather than a silent no-op.
      openGraphDrawer(value)
      setTimeout(() => { graphSearchValue.value = '' }, 300)
      return
    }
  }

  // Under server-side filtering, every node currently in graphNodes has
  // already passed the active type filter — there is no longer a path
  // where we need to re-enable a filter to make the target visible.

  if (graphPanZoomRef) {
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

async function handleGraphSearchEnter(context: { inputValue: string }) {
  const value = context.inputValue?.trim()
  if (!value) return

  // First try the already-loaded remote suggestions — if the user picked a
  // keyword whose results are on screen, fire the first match immediately.
  const match = graphSearchOptions.value.find(opt =>
    opt.label.toLowerCase().includes(value.toLowerCase()) ||
    opt.value.toLowerCase().includes(value.toLowerCase())
  )
  if (match) {
    handleGraphSearchSelect(match.value)
    return
  }

  // Fallback: user hit Enter before suggestions came back (fast typing /
  // network still pending). Run a one-shot search so Enter still navigates
  // somewhere useful rather than silently doing nothing.
  try {
    const res = await searchWikiPages(props.knowledgeBaseId, value, 1)
    const pages: WikiPage[] = (res as any)?.data?.pages || (res as any)?.pages || []
    if (pages.length > 0) {
      handleGraphSearchSelect(pages[0].slug)
    }
  } catch (e) {
    console.error('Wiki search failed on enter:', e)
  }
}

// Load graph when switching to graph view
// Reload all pages when search query is cleared (backspace or clear button).
// `searchResults = null` snaps back to the bucketed view without refetching
// anything — the buckets still hold whatever the user scrolled in before
// they started searching.
let searchTimer: ReturnType<typeof setTimeout> | null = null
watch(searchQuery, (val) => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    if (!val || !val.trim()) {
      searchResults.value = null
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
  if (groupSentinelObserver) {
    groupSentinelObserver.disconnect()
    groupSentinelObserver = null
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

// In page-mode the RecycleScroller delegates scrolling to the nearest
// scrollable ancestor (`.wiki-page-list`), so the scroller itself
// must not constrain height or introduce its own overflow. We only
// reserve a minimum to keep the empty-state loader from collapsing.
.wiki-group-scroller {
  min-height: 60px;
  margin-bottom: 4px;
}

.wiki-group-sentinel {
  // Invisible sentinel watched by IntersectionObserver to trigger
  // the next page fetch. Height > 0 so it reliably enters the viewport.
  height: 1px;
  width: 100%;
}

.wiki-group-loading {
  display: flex;
  justify-content: center;
  padding: 6px 0;
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

.wiki-tab-bar {
  display: flex;
  gap: 4px;
  padding: 8px 0;
  overflow-x: auto;
  // Hide scrollbar while still allowing horizontal pan when types
  // overflow the sidebar width (rare but happens with long labels).
  scrollbar-width: none;
  &::-webkit-scrollbar { display: none; }
  position: sticky;
  top: 0;
  z-index: 10;
  background: var(--td-bg-color-container);
}

.wiki-tab {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  border-radius: 14px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
  transition: background 0.15s, color 0.15s;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &.active {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
    font-weight: 500;
  }

  .wiki-tab-count {
    font-size: 11px;
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 10px;
    padding: 0 6px;
    line-height: 16px;
    color: var(--td-text-color-placeholder);
  }

  &.active .wiki-tab-count {
    background: var(--td-brand-color-1, rgba(0, 82, 217, 0.12));
    color: var(--td-brand-color);
  }
}

.wiki-page-item {
  // Lock the rendered height so it matches WIKI_PAGE_ITEM_HEIGHT (100)
  // minus margin-bottom (2). RecycleScroller absolute-positions rows
  // at multiples of itemSize, so any variance between actual rendered
  // height and the constant causes neighbors to overlap.
  height: 98px;
  box-sizing: border-box;
  overflow: hidden;
  padding: 10px 12px;
  border-radius: 6px;
  cursor: pointer;
  margin-bottom: 2px;
  transition: background 0.15s;

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

// ── Log feed (system view) ──
// Rendered when activeSystemView === 'log'. Sits where the markdown body
// would be for a regular wiki page — so the header/meta rules above
// already apply. We just style the feed list itself.
.wiki-log-feed {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-top: 12px;
}

// ── Index overview (system view) ──
// The index view renders as markdown through the same pipeline as a
// normal wiki page, so it inherits .wiki-reader-body styling automatically.
// We use a sentinel below the body to drive auto-pagination via
// IntersectionObserver — the user never sees a "Load more" button.
.wiki-index-sentinel {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 32px;
  padding: 16px 0 24px;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}

.wiki-index-loading {
  opacity: 0.7;
}

.wiki-log-empty {
  color: var(--td-text-color-placeholder);
  text-align: center;
  padding: 40px 0;
  font-size: 13px;
}

.wiki-log-entry {
  border: 1px solid var(--td-border-level-1-color);
  border-radius: 8px;
  padding: 10px 12px;
  background: var(--td-bg-color-container);
}

.wiki-log-entry-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.wiki-log-entry-title {
  font-weight: 500;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}

.wiki-log-entry-time {
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  white-space: nowrap;
}

.wiki-log-entry-summary {
  color: var(--td-text-color-secondary);
  font-size: 13px;
  margin: 4px 0;
}

.wiki-log-entry-pages {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 10px;
  margin-top: 4px;
}

.wiki-log-entry-page {
  color: var(--td-brand-color);
  font-size: 12px;
  text-decoration: none;
}

.wiki-log-entry-page:hover {
  text-decoration: underline;
}

.wiki-log-load-more {
  display: flex;
  justify-content: center;
  padding: 12px 0;
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

.help-glyph-icon {
  font-size: 14px !important;
  font-weight: 600;
  line-height: 14px !important;
  text-align: center;
  width: 14px;
  color: inherit;
}

.wiki-graph-help {
  min-width: 240px;
  max-width: 320px;

  .help-section-title {
    font-size: 11px;
    line-height: 14px;
    color: var(--td-text-color-placeholder);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    margin-bottom: 8px;
    user-select: none;
  }

  .help-rows {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .help-row {
    display: grid;
    grid-template-columns: 110px 1fr;
    gap: 12px;
    font-size: 12px;
    line-height: 16px;
  }

  .help-key {
    color: var(--td-text-color-primary);
    font-weight: 500;
    white-space: nowrap;
  }

  .help-desc {
    color: var(--td-text-color-secondary);
  }
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

.wiki-graph-truncation-hint {
  font-size: 11px;
  line-height: 14px;
  color: var(--td-text-color-placeholder);
  user-select: none;
  max-width: 280px;
}

.wiki-graph-status-card {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-width: 240px;
  padding: 8px 2px 2px;
  user-select: none;

  .status-card-header {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 11px;
    line-height: 14px;
    color: var(--td-text-color-placeholder);
    text-transform: uppercase;
    letter-spacing: 0.04em;

    .t-icon {
      font-size: 12px;
    }
  }

  .status-card-primary {
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .status-card-secondary {
    font-size: 11px;
    line-height: 14px;
    color: var(--td-text-color-secondary);
  }
}

.wiki-drawer-neighbor-hint {
  font-size: 12px;
  line-height: 16px;
  color: var(--td-text-color-secondary);
  user-select: none;
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
