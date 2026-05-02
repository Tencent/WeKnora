<template>
  <div class="ks-container">
    <div class="ks-content">
      <div class="header" style="--wails-draggable: drag">
        <div class="header-title" style="--wails-draggable: drag">
          <h2 style="--wails-draggable: drag">{{ $t('knowledgeSearch.title') }}</h2>
          <p class="header-subtitle" style="--wails-draggable: drag">{{ $t('knowledgeSearch.subtitle') }}</p>
        </div>
        <div class="header-actions" style="--wails-draggable: no-drag">
          <t-button variant="text" shape="square" :class="{ active: showSettings }" style="--wails-draggable: no-drag" @click="showSettings = !showSettings">
            <template #icon><t-icon name="setting" /></template>
          </t-button>
        </div>
      </div>

      <!-- Retrieval settings drawer -->
      <t-drawer
        v-model:visible="showSettings"
        :header="$t('retrievalSettings.title')"
        size="420px"
        :footer="false"
        :close-on-overlay-click="true"
        class="retrieval-drawer"
      >
        <RetrievalSettings />
      </t-drawer>

      <!-- Tab 切换 -->
      <div class="search-tabs">
        <div
          :class="['search-tab', { active: activeTab === 'knowledge' }]"
          @click="switchTab('knowledge')"
        >
          <t-icon name="file-search" size="16px" />
          {{ $t('knowledgeSearch.tabKnowledge') }}
        </div>
        <div
          :class="['search-tab', { active: activeTab === 'messages' }]"
          @click="switchTab('messages')"
        >
          <t-icon name="chat" size="16px" />
          {{ $t('knowledgeSearch.tabMessages') }}
        </div>
      </div>

      <div class="search-bar">
        <t-input
          v-model="query"
          :placeholder="activeTab === 'knowledge' ? $t('knowledgeSearch.placeholder') : $t('knowledgeSearch.messagePlaceholder')"
          clearable
          class="search-input"
          @enter="handleSearch"
        >
          <template #prefixIcon>
            <t-icon name="search" />
          </template>
        </t-input>
        <t-select
          v-if="activeTab === 'knowledge'"
          v-model="selectedKbIds"
          :placeholder="$t('knowledgeSearch.allKb')"
          multiple
          clearable
          filterable
          class="kb-filter"
          :loading="kbLoading"
        >
          <t-option
            v-for="kb in knowledgeBases"
            :key="kb.id"
            :value="kb.id"
            :label="kb.name"
          >
            <div class="kb-option-row">
              <span class="kb-option-name">{{ kb.name }}</span>
              <span :class="['kb-type-badge', kb.type === 'faq' ? 'faq' : 'doc']">
                {{ kb.type === 'faq' ? 'FAQ' : 'DOC' }}
              </span>
            </div>
          </t-option>
        </t-select>
        <t-button
          theme="primary"
          :loading="loading"
          :disabled="!query.trim()"
          class="search-btn"
          @click="handleSearch"
        >
          {{ $t('knowledgeSearch.searchBtn') }}
        </t-button>
      </div>

      <div class="ks-main">
        <!-- ==================== Knowledge Search Tab ==================== -->
        <template v-if="activeTab === 'knowledge'">
          <!-- Before search -->
          <div v-if="!hasSearched && !loading" class="empty-hint">
            <div class="empty-hint-icon">
              <t-icon name="search" size="36px" />
            </div>
            <p>{{ $t('knowledgeSearch.emptyHint') }}</p>
          </div>

          <!-- Loading -->
          <div v-else-if="loading" class="empty-hint">
            <t-loading size="small" :text="$t('knowledgeSearch.searching')" />
          </div>

          <!-- No results -->
          <div v-else-if="hasSearched && groupedResults.length === 0" class="empty-hint">
            <div class="empty-hint-icon muted">
              <t-icon name="info-circle" size="36px" />
            </div>
            <p>{{ $t('knowledgeSearch.noResults') }}</p>
          </div>

          <!-- Results grouped by file -->
          <template v-else>
            <div class="results-summary">
              <span>
                {{ $t('knowledgeSearch.resultCount', { count: totalChunks }) }}
                <span class="results-file-count">&middot; {{ groupedResults.length }} {{ $t('knowledgeSearch.fileCount') }}</span>
              </span>
              <span class="start-chat-link" @click="startChat()">
                <t-icon name="chat" size="14px" />
                {{ $t('knowledgeSearch.startChat') }}
              </span>
            </div>

            <div class="file-groups">
              <div
                v-for="(group, gIdx) in groupedResults"
                :key="group.knowledgeId"
                class="file-group"
              >
                <div class="file-group-header" @click="toggleFileExpand(gIdx)">
                  <div class="file-group-left">
                    <svg class="file-icon" width="16" height="16" viewBox="0 0 24 24" fill="none">
                      <path d="M14 2H6C5.46957 2 4.96086 2.21071 4.58579 2.58579C4.21071 2.96086 4 3.46957 4 4V20C4 20.5304 4.21071 21.0391 4.58579 21.4142C4.96086 21.7893 5.46957 22 6 22H18C18.5304 22 19.0391 21.7893 19.4142 21.4142C19.7893 21.0391 20 20.5304 20 20V8L14 2Z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                      <path d="M14 2V8H20" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                    </svg>
                    <span class="file-group-title">{{ group.title }}</span>
                    <span class="file-group-kb" v-if="group.kbName">{{ group.kbName }}</span>
                  </div>
                  <div class="file-group-right">
                    <span class="chunk-count">{{ group.chunks.length }} {{ $t('knowledgeSearch.chunk') }}</span>
                    <span
                      class="go-detail-link"
                      @click.stop="startChat(group)"
                    >
                      <t-icon name="chat" size="14px" />
                      {{ $t('knowledgeSearch.chatWithFile') }}
                    </span>
                    <span
                      v-if="group.kbId"
                      class="go-detail-link"
                      @click.stop="goToDetail(group)"
                    >
                      {{ $t('knowledgeSearch.viewDetail') }}
                      <t-icon name="jump" size="14px" />
                    </span>
                    <t-icon :name="expandedFiles.has(gIdx) ? 'chevron-up' : 'chevron-down'" size="16px" />
                  </div>
                </div>

                <div v-if="expandedFiles.has(gIdx)" class="file-group-chunks">
                  <div
                    v-for="(chunk, cIdx) in group.chunks"
                    :key="chunk.id || cIdx"
                    class="chunk-item"
                  >
                    <div class="chunk-item-meta">
                      <span class="chunk-index">#{{ chunk.chunk_index }}</span>
                      <span :class="['match-badge', chunk.match_type === 'vector' ? 'vector' : 'keyword']">
                        {{ chunk.match_type === 'vector' ? $t('knowledgeSearch.matchTypeVector') : $t('knowledgeSearch.matchTypeKeyword') }}
                      </span>
                      <span class="chunk-score">{{ (chunk.score * 100).toFixed(1) }}%</span>
                    </div>
                    <div
                      class="chunk-content"
                      :class="{ expanded: expandedChunks.has(`${gIdx}-${cIdx}`) }"
                      @click="toggleChunkExpand(gIdx, cIdx)"
                      v-html="highlightText(chunk.matched_content || chunk.content)"
                    ></div>
                  </div>
                </div>
              </div>
            </div>
          </template>
        </template>

        <!-- ==================== Message Search Tab ==================== -->
        <template v-if="activeTab === 'messages'">
          <!-- Before search -->
          <div v-if="!msgHasSearched && !msgLoading" class="empty-hint">
            <div class="empty-hint-icon">
              <t-icon name="chat" size="36px" />
            </div>
            <p>{{ $t('knowledgeSearch.messageEmptyHint') }}</p>
          </div>

          <!-- Loading -->
          <div v-else-if="msgLoading" class="empty-hint">
            <t-loading size="small" :text="$t('knowledgeSearch.searching')" />
          </div>

          <!-- No results -->
          <div v-else-if="msgHasSearched && msgGroupedResults.length === 0" class="empty-hint">
            <div class="empty-hint-icon muted">
              <t-icon name="info-circle" size="36px" />
            </div>
            <p>{{ $t('knowledgeSearch.noResults') }}</p>
          </div>

          <!-- Message results grouped by session -->
          <template v-else>
            <div class="results-summary">
              <span>{{ $t('knowledgeSearch.resultCount', { count: msgTotal }) }}</span>
            </div>

            <div class="msg-session-groups">
              <div
                v-for="group in msgGroupedResults"
                :key="group.sessionId"
                class="msg-session-group"
              >
                <div class="msg-session-header" @click="goToSessionById(group.sessionId)">
                  <t-icon name="chat" size="16px" class="msg-session-icon" />
                  <span class="msg-session-name">{{ group.sessionTitle || $t('knowledgeSearch.untitledSession') }}</span>
                  <span class="msg-session-count">{{ group.items.length }} {{ $t('knowledgeSearch.matchCount') }}</span>
                  <t-icon name="jump" size="14px" class="msg-session-jump" />
                </div>

                <div class="msg-qa-list">
                  <div
                    v-for="(item, idx) in group.items"
                    :key="item.request_id || idx"
                    class="msg-qa-item"
                  >
                    <!-- Q -->
                    <div class="msg-qa-row" v-if="item.query_content">
                      <span class="msg-role-badge user">Q</span>
                      <div class="msg-qa-content" v-html="highlightText(item.query_content)"></div>
                      <span class="msg-time">{{ formatTime(item.created_at) }}</span>
                    </div>
                    <!-- A -->
                    <div class="msg-qa-row" v-if="item.answer_content">
                      <span class="msg-role-badge assistant">A</span>
                      <div class="msg-qa-content answer" v-html="highlightText(item.answer_content)"></div>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </template>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { listKnowledgeBases, knowledgeSemanticSearch } from '@/api/knowledge-base'
import { searchMessages, type MessageSearchGroupItem } from '@/api/chat-history'
import RetrievalSettings from '@/views/settings/RetrievalSettings.vue'
import { useMenuStore } from '@/stores/menu'
import { useSettingsStore } from '@/stores/settings'

const { t } = useI18n()
const router = useRouter()
const menuStore = useMenuStore()
const settingsStore = useSettingsStore()

// ─── Shared state ───
const query = ref('')
const activeTab = ref<'knowledge' | 'messages'>('knowledge')
const showSettings = ref(false)

// ─── Knowledge search state ───
const loading = ref(false)
const kbLoading = ref(false)
const hasSearched = ref(false)
const selectedKbIds = ref<string[]>([])
const results = ref<any[]>([])
const expandedFiles = reactive(new Set<number>())
const expandedChunks = reactive(new Set<string>())
const knowledgeBases = ref<any[]>([])

// ─── Message search state ───
const msgLoading = ref(false)
const msgHasSearched = ref(false)
const msgResults = ref<MessageSearchGroupItem[]>([])
const msgTotal = ref(0)

interface MsgSessionGroup {
  sessionId: string
  sessionTitle: string
  items: MessageSearchGroupItem[]
}

const msgGroupedResults = computed<MsgSessionGroup[]>(() => {
  const map = new Map<string, MsgSessionGroup>()
  for (const item of msgResults.value) {
    const sid = item.session_id || 'unknown'
    if (!map.has(sid)) {
      map.set(sid, {
        sessionId: sid,
        sessionTitle: item.session_title || '',
        items: [],
      })
    }
    map.get(sid)!.items.push(item)
  }
  return Array.from(map.values())
})

interface FileGroup {
  knowledgeId: string
  kbId: string
  title: string
  kbName: string
  chunks: any[]
}

const groupedResults = computed<FileGroup[]>(() => {
  const map = new Map<string, FileGroup>()
  for (const item of results.value) {
    const kid = item.knowledge_id || 'unknown'
    if (!map.has(kid)) {
      map.set(kid, {
        knowledgeId: kid,
        kbId: item.knowledge_base_id || '',
        title: item.knowledge_title || item.knowledge_filename || kid,
        kbName: getKbName(item.knowledge_base_id),
        chunks: [],
      })
    }
    map.get(kid)!.chunks.push(item)
  }
  return Array.from(map.values())
})

const totalChunks = computed(() => results.value.length)

const switchTab = (tab: 'knowledge' | 'messages') => {
  activeTab.value = tab
}

const fetchKnowledgeBases = async () => {
  kbLoading.value = true
  try {
    const res: any = await listKnowledgeBases()
    if (res?.data) {
      knowledgeBases.value = res.data
    }
  } catch (e) {
    console.error('Failed to load knowledge bases', e)
  } finally {
    kbLoading.value = false
  }
}

const handleSearch = async () => {
  if (activeTab.value === 'knowledge') {
    await handleKnowledgeSearch()
  } else {
    await handleMessageSearch()
  }
}

const handleKnowledgeSearch = async () => {
  const q = query.value.trim()
  if (!q) return

  loading.value = true
  hasSearched.value = true
  expandedFiles.clear()
  expandedChunks.clear()

  try {
    const kbIds = selectedKbIds.value.length > 0
      ? selectedKbIds.value
      : knowledgeBases.value.map((kb: any) => kb.id)

    const res: any = await knowledgeSemanticSearch({
      query: q,
      knowledge_base_ids: kbIds,
    })
    if (res?.success && res.data) {
      results.value = res.data
    } else {
      results.value = []
    }
  } catch (e: any) {
    console.error('Search failed', e)
    MessagePlugin.error(e?.message || 'Search failed')
    results.value = []
  } finally {
    loading.value = false
  }
}

const handleMessageSearch = async () => {
  const q = query.value.trim()
  if (!q) return

  msgLoading.value = true
  msgHasSearched.value = true

  try {
    const res: any = await searchMessages({
      query: q,
      mode: 'hybrid',
      limit: 30,
    })
    if (res?.success && res.data) {
      msgResults.value = res.data.items || []
      msgTotal.value = res.data.total || 0
    } else {
      msgResults.value = []
      msgTotal.value = 0
    }
  } catch (e: any) {
    console.error('Message search failed', e)
    MessagePlugin.error(e?.message || 'Search failed')
    msgResults.value = []
    msgTotal.value = 0
  } finally {
    msgLoading.value = false
  }
}

const toggleFileExpand = (idx: number) => {
  if (expandedFiles.has(idx)) {
    expandedFiles.delete(idx)
  } else {
    expandedFiles.add(idx)
  }
}

const toggleChunkExpand = (gIdx: number, cIdx: number) => {
  const key = `${gIdx}-${cIdx}`
  if (expandedChunks.has(key)) {
    expandedChunks.delete(key)
  } else {
    expandedChunks.add(key)
  }
}

const goToDetail = (group: FileGroup) => {
  if (!group.kbId) return
  router.push({
    path: `/platform/knowledge-bases/${group.kbId}`,
    query: { knowledge_id: group.knowledgeId },
  })
}

const startChat = (group?: FileGroup) => {
  const q = query.value.trim()
  if (!q) return

  let kbIds: string[] = []
  let fileIds: string[] = []

  if (group) {
    if (group.kbId) {
      kbIds = [group.kbId]
    }
    fileIds = [group.knowledgeId]
  } else {
    kbIds = selectedKbIds.value.length > 0
      ? selectedKbIds.value
      : knowledgeBases.value.map((kb: any) => kb.id)
  }

  settingsStore.selectKnowledgeBases(kbIds)
  for (const fid of fileIds) {
    settingsStore.addFile(fid)
  }

  menuStore.setPrefillQuery(q)
  router.push('/platform/creatChat')
}

const goToSessionById = (sessionId: string) => {
  if (sessionId) {
    router.push(`/platform/chat/${sessionId}`)
  }
}

const highlightText = (text: string): string => {
  const q = query.value.trim()
  if (!q || !text) return escapeHtml(text || '')
  // Escape HTML first, then highlight
  const escaped = escapeHtml(text)
  const keywords = q.split(/\s+/).filter(Boolean)
  let result = escaped
  for (const kw of keywords) {
    const escapedKw = escapeHtml(kw).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const regex = new RegExp(`(${escapedKw})`, 'gi')
    result = result.replace(regex, '<mark class="search-highlight">$1</mark>')
  }
  return result
}

const escapeHtml = (str: string): string => {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;')
}

const formatTime = (timeStr: string) => {
  if (!timeStr) return ''
  try {
    const d = new Date(timeStr)
    return d.toLocaleDateString() + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  } catch {
    return timeStr
  }
}

const getKbName = (kbId: string): string => {
  if (!kbId) return ''
  const kb = knowledgeBases.value.find((k: any) => k.id === kbId)
  return kb?.name || ''
}

onMounted(() => {
  fetchKnowledgeBases()
})
</script>

<style lang="less" scoped>
.ks-container {
  margin: 0 16px 0 0;
  height: calc(100vh);
  box-sizing: border-box;
  flex: 1;
  display: flex;
  position: relative;
  min-height: 0;
}

.ks-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 24px 32px 0 32px;
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;

  .header-title {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  h2 {
    margin: 0;
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: 24px;
    font-weight: 600;
    line-height: 32px;
  }
}

.header-subtitle {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-family: var(--app-font-family);
  font-size: 14px;
  font-weight: 400;
  line-height: 20px;
}

.header-actions {
  :deep(.t-button) {
    color: var(--td-text-color-secondary);

    &.active {
      color: var(--td-brand-color);
      background: rgba(7, 192, 95, 0.08);
    }
  }
}

/* Tab 切换 */
.search-tabs {
  display: flex;
  gap: 4px;
  margin-bottom: 16px;
  padding: 3px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 8px;
  width: fit-content;
}

.search-tab {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 7px 16px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  transition: all 0.2s ease;
  user-select: none;

  &:hover {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &.active {
    color: var(--td-brand-color);
    background: var(--td-bg-color-container);
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.06);
  }
}

.search-bar {
  display: flex;
  gap: 10px;
  align-items: center;
  margin-bottom: 20px;

  :deep(.t-input) {
    font-size: 13px;
    background-color: var(--td-bg-color-container);
    border-color: var(--td-component-stroke);
    border-radius: 6px;

    &:hover,
    &:focus,
    &.t-is-focused {
      border-color: var(--td-brand-color);
      background-color: var(--td-bg-color-container);
    }
  }

  :deep(.t-select .t-input) {
    font-size: 13px;
    background-color: var(--td-bg-color-container);
    border-color: var(--td-component-stroke);
    border-radius: 6px;

    &:hover,
    &.t-is-focused {
      border-color: var(--td-brand-color);
      background-color: var(--td-bg-color-container);
    }
  }
}

.search-input {
  flex: 1;
  min-width: 0;
}

.kb-filter {
  width: 200px;
  flex-shrink: 0;
}

.search-btn {
  flex-shrink: 0;
  background: linear-gradient(135deg, var(--td-brand-color) 0%, #00a67e 100%);
  border: none;
  color: var(--td-text-color-anti);
  border-radius: 6px;

  &:hover {
    background: linear-gradient(135deg, var(--td-brand-color) 0%, var(--td-brand-color-active) 100%);
  }
}

.kb-option-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  gap: 8px;
}

.kb-option-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.kb-type-badge {
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 3px;
  font-weight: 500;
  flex-shrink: 0;

  &.doc {
    background: rgba(7, 192, 95, 0.1);
    color: var(--td-brand-color);
  }
  &.faq {
    background: rgba(255, 152, 0, 0.1);
    color: var(--td-warning-color);
  }
}

.ks-main {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-bottom: 24px;
}

.empty-hint {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 100px 0 60px;
  gap: 12px;
  color: var(--td-text-color-disabled);
  font-size: 14px;

  p {
    margin: 0;
  }
}

.empty-hint-icon {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  background: var(--td-bg-color-secondarycontainer);
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-disabled);

  &.muted {
    color: var(--td-text-color-disabled);
  }
}

.results-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  margin-bottom: 16px;
  padding: 0 2px;
}

.start-chat-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  color: var(--td-brand-color);
  cursor: pointer;
  padding: 4px 10px;
  border-radius: 6px;
  border: 1px solid rgba(7, 192, 95, 0.3);
  transition: all 0.15s;

  &:hover {
    background: rgba(7, 192, 95, 0.08);
    border-color: var(--td-brand-color);
  }
}

.results-file-count {
  color: var(--td-text-color-disabled);
}

.file-groups {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.file-group {
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  overflow: hidden;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.03);
  transition: box-shadow 0.2s;

  &:hover {
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
  }
}

.file-group-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 18px;
  cursor: pointer;
  user-select: none;
  transition: background 0.15s;

  &:hover {
    background: var(--td-bg-color-container);
  }
}

.file-group-left {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
}

.file-icon {
  flex-shrink: 0;
  color: var(--td-brand-color);
}

.file-group-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-group-kb {
  font-size: 12px;
  color: var(--td-text-color-disabled);
  padding: 1px 8px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 4px;
  flex-shrink: 0;
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-group-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
  color: var(--td-text-color-disabled);
}

.chunk-count {
  font-size: 12px;
  color: var(--td-text-color-disabled);
}

.go-detail-link {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 12px;
  color: var(--td-brand-color);
  cursor: pointer;
  padding: 2px 6px;
  border-radius: 4px;
  transition: all 0.15s;

  &:hover {
    background: rgba(7, 192, 95, 0.08);
    color: var(--td-brand-color-active);
  }
}

.file-group-chunks {
  border-top: 1px solid var(--td-component-stroke);
}

.chunk-item {
  padding: 12px 18px 12px 44px;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.chunk-item-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.chunk-index {
  font-size: 11px;
  color: var(--td-text-color-disabled);
  font-weight: 600;
  font-family: var(--app-font-family-mono);
}

.match-badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 3px;
  font-weight: 500;

  &.vector {
    background: rgba(22, 119, 255, 0.08);
    color: var(--td-brand-color);
  }
  &.keyword {
    background: rgba(255, 152, 0, 0.08);
    color: var(--td-warning-color);
  }
}

.chunk-score {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  font-family: var(--app-font-family-mono);
}

.chunk-content {
  font-size: 13px;
  color: var(--td-text-color-primary);
  line-height: 1.7;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 66px;
  overflow: hidden;
  cursor: pointer;
  position: relative;
  transition: max-height 0.3s ease;

  &::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    height: 24px;
    background: linear-gradient(transparent, var(--td-bg-color-container));
    pointer-events: none;
  }

  &.expanded {
    max-height: none;

    &::after {
      display: none;
    }
  }
}

/* ─── Message search results (session-grouped Q&A) ─── */
.msg-session-groups {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.msg-session-group {
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  overflow: hidden;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.03);
  transition: box-shadow 0.2s;

  &:hover {
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
  }
}

.msg-session-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 18px;
  cursor: pointer;
  user-select: none;
  border-bottom: 1px solid var(--td-component-stroke);
  transition: background 0.15s;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
  }
}

.msg-session-icon {
  flex-shrink: 0;
  color: var(--td-brand-color);
}

.msg-session-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}

.msg-session-count {
  font-size: 12px;
  color: var(--td-text-color-disabled);
  flex-shrink: 0;
}

.msg-session-jump {
  flex-shrink: 0;
  color: var(--td-text-color-disabled);
  transition: color 0.15s;

  .msg-session-header:hover & {
    color: var(--td-brand-color);
  }
}

.msg-qa-list {
  display: flex;
  flex-direction: column;
}

.msg-qa-item {
  padding: 12px 18px;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.msg-time {
  font-size: 11px;
  color: var(--td-text-color-disabled);
  flex-shrink: 0;
  align-self: flex-start;
  margin-top: 2px;
  white-space: nowrap;
}

.msg-qa-row {
  display: flex;
  gap: 10px;
  margin-bottom: 6px;

  &:last-child {
    margin-bottom: 0;
  }
}

.msg-role-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 4px;
  font-size: 11px;
  font-weight: 700;
  flex-shrink: 0;
  margin-top: 1px;

  &.user {
    background: rgba(22, 119, 255, 0.1);
    color: #1677ff;
  }
  &.assistant {
    background: rgba(7, 192, 95, 0.1);
    color: var(--td-brand-color);
  }
}

.msg-qa-content {
  font-size: 13px;
  color: var(--td-text-color-primary);
  line-height: 1.7;
  word-break: break-word;
  flex: 1;
  min-width: 0;
  max-height: 66px;
  overflow: hidden;
  position: relative;

  &::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    height: 24px;
    background: linear-gradient(transparent, var(--td-bg-color-container));
    pointer-events: none;
  }

  &.answer {
    color: var(--td-text-color-secondary);
  }
}

/* ─── Search highlight ─── */
:deep(.search-highlight) {
  background: rgba(255, 213, 0, 0.35);
  color: inherit;
  padding: 0 1px;
  border-radius: 2px;
}

</style>

<style lang="less">
/* Unscoped: drawer renders outside component scope */
.retrieval-drawer {
  .section-header {
    display: none;
  }
}
</style>
