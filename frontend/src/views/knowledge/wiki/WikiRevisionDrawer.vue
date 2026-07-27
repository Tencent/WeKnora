<template>
  <t-drawer :visible="visible" size="760px" :footer="false" class="wiki-revision-drawer"
    :header="t('knowledgeEditor.wikiBrowser.historyTitle', { title: currentPage?.title || slug })"
    @update:visible="(v: boolean) => emit('update:visible', v)">
    <div class="wiki-rev-layout">
      <!-- Version list -->
      <aside class="wiki-rev-list">
        <div class="wiki-rev-list-items">
          <div v-if="currentPage" class="wiki-rev-item"
            :class="{ 'wiki-rev-item--active': selectedVersion === currentPage.version }"
            @click="selectCurrent">
            <div class="wiki-rev-item-primary">
              <span class="wiki-rev-version">v{{ currentPage.version }}</span>
              <span class="wiki-rev-current-label">{{ t('knowledgeEditor.wikiBrowser.revisionCurrent') }}</span>
            </div>
            <div class="wiki-rev-item-secondary">
              <span>{{ sourceLabel(currentPage.last_edit_source) }}</span>
              <span class="wiki-rev-time">{{ formatShortTime(currentPage.updated_at) }}</span>
            </div>
          </div>

          <div v-for="rev in revisions" :key="rev.id" class="wiki-rev-item"
            :class="{ 'wiki-rev-item--active': selectedVersion === rev.version }" @click="selectRevision(rev)">
            <div class="wiki-rev-item-primary">
              <span class="wiki-rev-version">v{{ rev.version }}</span>
            </div>
            <div class="wiki-rev-item-secondary">
              <span>{{ sourceLabel(rev.edit_source) }}</span>
              <span class="wiki-rev-time">{{ formatShortTime(rev.edited_at) }}</span>
            </div>
          </div>

          <div v-if="revisions.length < total" class="wiki-rev-load-more">
            <t-button size="small" variant="outline" theme="default" :loading="loadingList" block @click="loadMore">
              {{ t('knowledgeEditor.wikiBrowser.logLoadMore') }}
            </t-button>
          </div>
        </div>
        <div v-if="!loadingList && revisions.length === 0" class="wiki-rev-empty">
          {{ t('knowledgeEditor.wikiBrowser.revisionEmpty') }}
        </div>
      </aside>

      <!-- Detail pane -->
      <div class="wiki-rev-detail">
        <template v-if="selectedVersion !== null && currentPage && selectedVersion === currentPage.version">
          <div class="wiki-rev-detail-hint">{{ t('knowledgeEditor.wikiBrowser.revisionCurrentHint') }}</div>
        </template>

        <template v-else-if="selectedRevision">
          <div class="wiki-rev-detail-toolbar">
            <div class="wiki-rev-detail-title">
              <span class="wiki-rev-detail-version">v{{ selectedRevision.version }}</span>
              <span class="wiki-rev-detail-name">{{ selectedRevision.title }}</span>
            </div>
            <div class="wiki-rev-detail-actions">
              <div class="wiki-rev-mode-toggle" role="group">
                <button type="button" class="wiki-rev-mode-btn"
                  :class="{ active: detailMode === 'diff' }" @click="detailMode = 'diff'">
                  {{ t('knowledgeEditor.wikiBrowser.revisionDiff') }}
                </button>
                <button type="button" class="wiki-rev-mode-btn"
                  :class="{ active: detailMode === 'raw' }" @click="detailMode = 'raw'">
                  {{ t('knowledgeEditor.wikiBrowser.revisionRaw') }}
                </button>
              </div>
              <t-popconfirm v-if="canEdit"
                :content="t('knowledgeEditor.wikiBrowser.revertConfirm', { ver: selectedRevision.version })"
                @confirm="doRevert">
                <t-button size="small" theme="warning" variant="outline" :loading="reverting">
                  <template #icon><t-icon name="rollback" /></template>
                  {{ t('knowledgeEditor.wikiBrowser.revertBtn') }}
                </t-button>
              </t-popconfirm>
            </div>
          </div>

          <div v-if="loadingDetail" class="wiki-rev-detail-loading">
            <t-loading size="small" />
            <span>{{ t('knowledgeEditor.wikiBrowser.logLoading') }}</span>
          </div>

          <!-- Diff vs current -->
          <div v-else-if="detailMode === 'diff'" class="wiki-rev-diff">
            <div class="wiki-rev-diff-caption">
              {{ t('knowledgeEditor.wikiBrowser.revisionDiffCaption', {
                from: selectedRevision.version, to: currentPage?.version ?? '?' }) }}
            </div>
            <pre class="wiki-rev-diff-body"><span v-for="(line, idx) in diffLines" :key="idx"
              :class="['wiki-rev-diff-line', `wiki-rev-diff-line--${line.type}`]">{{ diffPrefix(line.type) }}{{ line.text }}
</span></pre>
          </div>

          <!-- Raw content -->
          <pre v-else class="wiki-rev-raw">{{ detailContent }}</pre>
        </template>

        <div v-else class="wiki-rev-detail-hint">{{ t('knowledgeEditor.wikiBrowser.revisionSelectHint') }}</div>
      </div>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  listWikiRevisions,
  getWikiRevision,
  revertWikiPage,
  type WikiPage,
  type WikiPageRevision,
} from '@/api/wiki'
import { diffWikiLines, type WikiDiffLine } from '@/utils/wikiLineDiff'

const props = defineProps<{
  visible: boolean
  kbId: string
  slug: string
  currentPage: WikiPage | null
  canEdit?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:visible', visible: boolean): void
  (e: 'reverted', page: WikiPage): void
}>()

const { t } = useI18n()

const PAGE_SIZE = 50

const revisions = ref<WikiPageRevision[]>([])
const total = ref(0)
const loadingList = ref(false)

const selectedVersion = ref<number | null>(null)
const selectedRevision = ref<WikiPageRevision | null>(null)
const detailContent = ref('')
const loadingDetail = ref(false)
const detailMode = ref<'diff' | 'raw'>('diff')
const reverting = ref(false)

const diffLines = computed<WikiDiffLine[]>(() => {
  if (!props.currentPage || !selectedRevision.value) return []
  return diffWikiLines(detailContent.value, props.currentPage.content || '')
})

watch(
  () => [props.visible, props.slug] as const,
  ([visible]) => {
    if (visible && props.slug) {
      resetAndLoad()
    }
  },
)

function resetAndLoad() {
  detailRequestSeq++
  revisions.value = []
  total.value = 0
  selectedVersion.value = props.currentPage?.version ?? null
  selectedRevision.value = null
  detailContent.value = ''
  loadingDetail.value = false
  loadList(0)
}

async function loadList(offset: number) {
  loadingList.value = true
  try {
    const res = await listWikiRevisions(props.kbId, props.slug, { limit: PAGE_SIZE, offset })
    const data = (res as any).data || (res as any)
    const items: WikiPageRevision[] = data.revisions || []
    if (offset === 0) {
      revisions.value = items
    } else {
      // Snapshots are created while the user pages through, which shifts the
      // newest-first window. Drop versions we already hold so an overlapping
      // page cannot produce duplicate rows (and duplicate :key values).
      const seen = new Set(revisions.value.map((r) => r.version))
      revisions.value = [...revisions.value, ...items.filter((r) => !seen.has(r.version))]
    }
    total.value = data.total ?? revisions.value.length
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('knowledgeEditor.wikiBrowser.revisionLoadFailed'))
  } finally {
    loadingList.value = false
  }
}

function loadMore() {
  if (loadingList.value) return
  loadList(revisions.value.length)
}

function selectCurrent() {
  detailRequestSeq++
  selectedVersion.value = props.currentPage?.version ?? null
  selectedRevision.value = null
  detailContent.value = ''
  loadingDetail.value = false
}

// Monotonic token guarding the detail fetch: clicking through the list fires
// overlapping requests, and a slow earlier one must not overwrite the body of
// the revision the user is actually looking at.
let detailRequestSeq = 0

async function selectRevision(rev: WikiPageRevision) {
  const seq = ++detailRequestSeq
  selectedVersion.value = rev.version
  selectedRevision.value = rev
  detailContent.value = ''
  loadingDetail.value = true
  try {
    const res = await getWikiRevision(props.kbId, props.slug, rev.version)
    if (seq !== detailRequestSeq) return
    const data = (res as any).data || (res as any)
    detailContent.value = data.content || ''
  } catch (e: any) {
    if (seq !== detailRequestSeq) return
    MessagePlugin.error(e?.message || t('knowledgeEditor.wikiBrowser.revisionLoadFailed'))
  } finally {
    if (seq === detailRequestSeq) loadingDetail.value = false
  }
}

async function doRevert() {
  if (!selectedRevision.value) return
  reverting.value = true
  try {
    const res = await revertWikiPage(props.kbId, props.slug, selectedRevision.value.version)
    const updated = ((res as any).data || (res as any)) as WikiPage
    MessagePlugin.success(t('knowledgeEditor.wikiBrowser.revertSuccess', { ver: selectedRevision.value.version }))
    emit('reverted', updated)
    // Stay open: reload so the just-created snapshot of the pre-revert
    // version shows up and the "current" entry reflects the new version.
    resetAndLoad()
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('knowledgeEditor.wikiBrowser.revertFailed'))
  } finally {
    reverting.value = false
  }
}

function diffPrefix(type: WikiDiffLine['type']): string {
  return type === 'add' ? '+ ' : type === 'del' ? '- ' : '  '
}

function sourceLabel(source?: string): string {
  switch (source) {
    case 'user':
      return t('knowledgeEditor.wikiBrowser.editSourceUser')
    case 'agent':
      return t('knowledgeEditor.wikiBrowser.editSourceAgent')
    case 'revert':
      return t('knowledgeEditor.wikiBrowser.editSourceRevert')
    default:
      return t('knowledgeEditor.wikiBrowser.editSourcePipeline')
  }
}

function formatShortTime(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const now = new Date()
  if (d.toDateString() === now.toDateString()) {
    return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  }
  return d.toLocaleString(undefined, {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

</script>

<style scoped>
.wiki-rev-layout {
  display: flex;
  flex: 1;
  min-height: 0;
  height: 100%;
  align-items: stretch;
}

.wiki-rev-list {
  width: 220px;
  flex-shrink: 0;
  min-height: 0;
  overflow: hidden;
  padding: 12px 0;
  border-right: 1px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
}

.wiki-rev-list-items {
  flex: 0 0 auto;
  overflow-y: auto;
  min-height: 0;
  padding: 0 8px 0 14px;
}

.wiki-rev-item {
  border: none;
  border-radius: 6px;
  padding: 6px 10px 6px 14px;
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;
}

.wiki-rev-item:hover,
.wiki-rev-item--active {
  background: var(--td-bg-color-container-hover);
}

.wiki-rev-item--active .wiki-rev-version,
.wiki-rev-item--active .wiki-rev-current-label {
  color: var(--td-brand-color);
}

.wiki-rev-item-primary {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.wiki-rev-version {
  font-weight: 400;
  font-size: 14px;
  line-height: 20px;
  font-family: var(--td-font-family-mono, monospace);
  color: var(--td-text-color-primary);
  transition: color 0.15s ease;
}

.wiki-rev-current-label {
  font-size: 12px;
  line-height: 16px;
  color: var(--td-text-color-placeholder);
  transition: color 0.15s ease;
}

.wiki-rev-item-secondary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 2px;
  min-width: 0;
  font-size: 11px;
  line-height: 16px;
  color: var(--td-text-color-placeholder);
}

.wiki-rev-time {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}

.wiki-rev-load-more {
  padding: 8px 0 4px;
}

.wiki-rev-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px 14px;
  text-align: center;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.wiki-rev-detail {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: 16px 20px;
}

.wiki-rev-detail-toolbar {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.wiki-rev-detail-title {
  min-width: 0;
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.wiki-rev-detail-version {
  font-weight: 600;
  font-family: var(--td-font-family-mono, monospace);
  font-size: 13px;
  color: var(--td-text-color-secondary);
  flex-shrink: 0;
}

.wiki-rev-detail-name {
  font-weight: 600;
  font-size: 15px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.wiki-rev-detail-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.wiki-rev-mode-toggle {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  border-radius: 6px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
}

.wiki-rev-mode-btn {
  padding: 4px 10px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.4;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.wiki-rev-mode-btn:hover {
  color: var(--td-text-color-primary);
}

.wiki-rev-mode-btn.active {
  color: var(--td-brand-color);
  background: var(--td-bg-color-container);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06);
  font-weight: 500;
}

.wiki-rev-detail-hint {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-placeholder);
  padding: 24px;
  text-align: center;
  font-size: 13px;
  line-height: 1.5;
}

.wiki-rev-detail-loading {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}

.wiki-rev-diff {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.wiki-rev-diff-caption {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  margin-bottom: 8px;
}

.wiki-rev-diff-body,
.wiki-rev-raw {
  flex: 1;
  overflow: auto;
  margin: 0;
  padding: 12px 14px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  font-size: 12px;
  line-height: 1.7;
  font-family: var(--td-font-family-mono, monospace);
  white-space: pre-wrap;
  word-break: break-word;
}

.wiki-rev-diff-line {
  display: block;
}

.wiki-rev-diff-line--add {
  background: rgba(7, 192, 95, 0.08);
  color: var(--td-text-color-primary);
}

.wiki-rev-diff-line--del {
  background: rgba(213, 73, 65, 0.06);
  color: var(--td-text-color-secondary);
}
</style>

<style lang="less">
.wiki-revision-drawer {
  .t-drawer__content-wrapper,
  .t-drawer__content {
    display: flex;
    flex-direction: column;
    height: 100%;
  }

  .t-drawer__header {
    padding: 20px 24px;
    border-bottom: 1px solid var(--td-component-stroke);
    flex-shrink: 0;
  }

  .t-drawer__body {
    flex: 1;
    min-height: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    background: var(--td-bg-color-container);
  }
}
</style>
