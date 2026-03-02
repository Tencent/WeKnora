<template>
  <div class="ks-container">
    <div class="ks-content">
      <div class="header">
        <div class="header-title">
          <h2>{{ $t('knowledgeSearch.title') }}</h2>
          <p class="header-subtitle">{{ $t('knowledgeSearch.subtitle') }}</p>
        </div>
      </div>

      <div class="search-bar">
        <t-input
          v-model="query"
          :placeholder="$t('knowledgeSearch.placeholder')"
          clearable
          class="search-input"
          @enter="handleSearch"
        >
          <template #prefixIcon>
            <t-icon name="search" />
          </template>
        </t-input>
        <t-select
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
                  >
                    {{ chunk.matched_content || chunk.content }}
                  </div>
                </div>
              </div>
            </div>
          </div>
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
import { useMenuStore } from '@/stores/menu'
import { useSettingsStore } from '@/stores/settings'

const { t } = useI18n()
const router = useRouter()
const menuStore = useMenuStore()
const settingsStore = useSettingsStore()

const query = ref('')
const loading = ref(false)
const kbLoading = ref(false)
const hasSearched = ref(false)
const selectedKbIds = ref<string[]>([])
const results = ref<any[]>([])
const expandedFiles = reactive(new Set<number>())
const expandedChunks = reactive(new Set<string>())
const knowledgeBases = ref<any[]>([])

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
  margin-bottom: 20px;

  .header-title {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  h2 {
    margin: 0;
    color: #000000e6;
    font-family: "PingFang SC", -apple-system, sans-serif;
    font-size: 24px;
    font-weight: 600;
    line-height: 32px;
  }
}

.header-subtitle {
  margin: 0;
  color: #00000099;
  font-family: "PingFang SC", -apple-system, sans-serif;
  font-size: 14px;
  font-weight: 400;
  line-height: 20px;
}

.search-bar {
  display: flex;
  gap: 10px;
  align-items: center;
  margin-bottom: 20px;

  :deep(.t-input) {
    font-size: 13px;
    background-color: #f7f9fc;
    border-color: #e5e9f2;
    border-radius: 6px;

    &:hover,
    &:focus,
    &.t-is-focused {
      border-color: #07C05F;
      background-color: #fff;
    }
  }

  :deep(.t-select .t-input) {
    font-size: 13px;
    background-color: #f7f9fc;
    border-color: #e5e9f2;
    border-radius: 6px;

    &:hover,
    &.t-is-focused {
      border-color: #07C05F;
      background-color: #fff;
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
  background: linear-gradient(135deg, #07c05f 0%, #00a67e 100%);
  border: none;
  color: #fff;
  border-radius: 6px;

  &:hover {
    background: linear-gradient(135deg, #05a04f 0%, #008a6a 100%);
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
    color: #07c05f;
  }
  &.faq {
    background: rgba(255, 152, 0, 0.1);
    color: #e8a735;
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
  color: #00000066;
  font-size: 14px;

  p {
    margin: 0;
  }
}

.empty-hint-icon {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  background: #f5f7fa;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #c0c4cc;

  &.muted {
    color: #dcdfe6;
  }
}

.results-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 13px;
  color: #00000099;
  margin-bottom: 16px;
  padding: 0 2px;
}

.start-chat-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  color: #07c05f;
  cursor: pointer;
  padding: 4px 10px;
  border-radius: 6px;
  border: 1px solid rgba(7, 192, 95, 0.3);
  transition: all 0.15s;

  &:hover {
    background: rgba(7, 192, 95, 0.08);
    border-color: #07c05f;
  }
}

.results-file-count {
  color: #00000066;
}

.file-groups {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.file-group {
  border: 1px solid #f0f0f0;
  border-radius: 10px;
  background: #fff;
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
    background: #fafbfc;
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
  color: #07c05f;
}

.file-group-title {
  font-size: 14px;
  font-weight: 600;
  color: #1d2129;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-group-kb {
  font-size: 12px;
  color: #00000066;
  padding: 1px 8px;
  background: #f5f7fa;
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
  color: #00000066;
}

.chunk-count {
  font-size: 12px;
  color: #00000066;
}

.go-detail-link {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 12px;
  color: #07c05f;
  cursor: pointer;
  padding: 2px 6px;
  border-radius: 4px;
  transition: all 0.15s;

  &:hover {
    background: rgba(7, 192, 95, 0.08);
    color: #05a04f;
  }
}

.file-group-chunks {
  border-top: 1px solid #f0f0f0;
}

.chunk-item {
  padding: 12px 18px 12px 44px;
  border-bottom: 1px solid #f8f8f8;

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
  color: #00000066;
  font-weight: 600;
  font-family: "SF Mono", "Monaco", monospace;
}

.match-badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 3px;
  font-weight: 500;

  &.vector {
    background: rgba(22, 119, 255, 0.08);
    color: #1677ff;
  }
  &.keyword {
    background: rgba(255, 152, 0, 0.08);
    color: #e8a735;
  }
}

.chunk-score {
  font-size: 11px;
  color: #00000044;
  font-family: "SF Mono", "Monaco", monospace;
}

.chunk-content {
  font-size: 13px;
  color: #000000cc;
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
    background: linear-gradient(transparent, #fff);
    pointer-events: none;
  }

  &.expanded {
    max-height: none;

    &::after {
      display: none;
    }
  }
}
</style>
