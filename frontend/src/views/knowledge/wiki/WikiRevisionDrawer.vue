<template>
  <t-drawer :visible="visible" size="760px" :footer="false" class="wiki-revision-drawer"
    :header="t('knowledgeEditor.wikiBrowser.historyTitle', { title: currentPage?.title || slug })"
    @update:visible="(v: boolean) => emit('update:visible', v)">
    <div class="wiki-rev-layout">
      <!-- Version list -->
      <div class="wiki-rev-list">
        <!-- Current version pseudo-entry -->
        <div v-if="currentPage" class="wiki-rev-item"
          :class="{ 'wiki-rev-item--active': selectedVersion === currentPage.version }"
          @click="selectCurrent">
          <div class="wiki-rev-item-head">
            <span class="wiki-rev-version">v{{ currentPage.version }}</span>
            <t-tag size="small" theme="primary" variant="light">
              {{ t('knowledgeEditor.wikiBrowser.revisionCurrent') }}
            </t-tag>
            <t-tag size="small" :theme="sourceTheme(currentPage.last_edit_source)" variant="light-outline">
              {{ sourceLabel(currentPage.last_edit_source) }}
            </t-tag>
          </div>
          <div class="wiki-rev-item-meta">{{ formatTime(currentPage.updated_at) }}</div>
        </div>

        <div v-for="rev in revisions" :key="rev.id" class="wiki-rev-item"
          :class="{ 'wiki-rev-item--active': selectedVersion === rev.version }" @click="selectRevision(rev)">
          <div class="wiki-rev-item-head">
            <span class="wiki-rev-version">v{{ rev.version }}</span>
            <t-tag size="small" :theme="sourceTheme(rev.edit_source)" variant="light-outline">
              {{ sourceLabel(rev.edit_source) }}
            </t-tag>
          </div>
          <div class="wiki-rev-item-meta">
            {{ formatTime(rev.edited_at) }}
            <span v-if="rev.editor_id" class="wiki-rev-editor">· {{ rev.editor_id }}</span>
          </div>
        </div>

        <div v-if="revisions.length < total" class="wiki-rev-load-more">
          <t-link theme="primary" hover="color" :disabled="loadingList" @click="loadMore">
            {{ t('knowledgeEditor.wikiBrowser.logLoadMore') }}
          </t-link>
        </div>
        <div v-if="!loadingList && revisions.length === 0" class="wiki-rev-empty">
          {{ t('knowledgeEditor.wikiBrowser.revisionEmpty') }}
        </div>
      </div>

      <!-- Detail pane -->
      <div class="wiki-rev-detail">
        <template v-if="selectedVersion !== null && currentPage && selectedVersion === currentPage.version">
          <div class="wiki-rev-detail-hint">{{ t('knowledgeEditor.wikiBrowser.revisionCurrentHint') }}</div>
        </template>

        <template v-else-if="selectedRevision">
          <div class="wiki-rev-detail-toolbar">
            <div class="wiki-rev-detail-title">
              v{{ selectedRevision.version }} · {{ selectedRevision.title }}
            </div>
            <div class="wiki-rev-detail-actions">
              <t-radio-group v-model="detailMode" variant="default-filled" size="small">
                <t-radio-button value="diff">{{ t('knowledgeEditor.wikiBrowser.revisionDiff') }}</t-radio-button>
                <t-radio-button value="raw">{{ t('knowledgeEditor.wikiBrowser.revisionRaw') }}</t-radio-button>
              </t-radio-group>
              <t-popconfirm v-if="canEdit" :content="t('knowledgeEditor.wikiBrowser.revertConfirm', { ver: selectedRevision.version })"
                @confirm="doRevert">
                <t-button size="small" theme="warning" variant="outline" :loading="reverting">
                  <template #icon><t-icon name="rollback" /></template>
                  {{ t('knowledgeEditor.wikiBrowser.revertBtn') }}
                </t-button>
              </t-popconfirm>
            </div>
          </div>

          <div v-if="loadingDetail" class="wiki-rev-detail-hint">{{ t('knowledgeEditor.wikiBrowser.logLoading') }}</div>

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

function sourceTheme(source?: string): 'primary' | 'success' | 'warning' | 'default' {
  switch (source) {
    case 'user':
      return 'success'
    case 'agent':
      return 'primary'
    case 'revert':
      return 'warning'
    default:
      return 'default'
  }
}

function formatTime(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}
</script>

<style scoped>
.wiki-rev-layout {
  display: flex;
  gap: 16px;
  height: 100%;
  min-height: 0;
}

.wiki-rev-list {
  width: 220px;
  flex-shrink: 0;
  overflow-y: auto;
  border-right: 1px solid var(--td-component-stroke);
  padding-right: 12px;
}

.wiki-rev-item {
  padding: 8px 10px;
  border-radius: 6px;
  cursor: pointer;
  margin-bottom: 4px;
}

.wiki-rev-item:hover {
  background: var(--td-bg-color-container-hover);
}

.wiki-rev-item--active {
  background: var(--td-brand-color-light);
}

.wiki-rev-item-head {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.wiki-rev-version {
  font-weight: 600;
  font-family: var(--td-font-family-mono, monospace);
}

.wiki-rev-item-meta {
  margin-top: 4px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  word-break: break-all;
}

.wiki-rev-load-more,
.wiki-rev-empty {
  text-align: center;
  padding: 12px 0;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.wiki-rev-detail {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.wiki-rev-detail-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.wiki-rev-detail-title {
  font-weight: 600;
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

.wiki-rev-detail-hint {
  color: var(--td-text-color-placeholder);
  padding: 40px 0;
  text-align: center;
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
  padding: 12px;
  background: var(--td-bg-color-page);
  border-radius: 6px;
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
  background: var(--td-success-color-1);
  color: var(--td-success-color-7);
}

.wiki-rev-diff-line--del {
  background: var(--td-error-color-1);
  color: var(--td-error-color-6);
  text-decoration: line-through;
}
</style>
