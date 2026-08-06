<template>
  <SettingDrawer v-model:visible="drawerVisible" class="wiki-revision-drawer" :title="drawerTitle" icon="history"
    width="760px" :min-width="560" :max-width="1280" storage-key="setting-drawer:width:wiki-revision-history"
    hide-footer>
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
              {{ t('knowledgeEditor.wikiBrowser.loadMoreShort') }}
            </t-button>
          </div>
        </div>
        <div v-if="!loadingList && revisions.length === 0" class="wiki-rev-empty">
          {{ t('knowledgeEditor.wikiBrowser.revisionEmpty') }}
        </div>
      </aside>

      <!-- Detail pane -->
      <div class="wiki-rev-detail">
        <template v-if="selectedVersion !== null && canShowDiff">
          <div class="wiki-rev-detail-head">
            <div class="wiki-rev-detail-context">
              <div class="wiki-rev-detail-range">{{ versionRangeLabel }}</div>
              <div v-if="contextHint" class="wiki-rev-detail-sub">{{ contextHint }}</div>
            </div>
            <div class="wiki-rev-detail-controls">
              <div v-if="viewModeOptions.length > 1" class="wiki-rev-view-switch" role="tablist"
                :aria-label="t('knowledgeEditor.wikiBrowser.revisionViewModeLabel')">
                <button v-for="option in viewModeOptions" :key="option.value" type="button"
                  class="wiki-rev-view-switch-btn" role="tab" :aria-selected="viewMode === option.value"
                  :class="{ active: viewMode === option.value }" @click="viewMode = option.value">
                  {{ option.label }}
                </button>
              </div>
              <t-popconfirm v-if="canEdit && selectedRevision"
                :content="t('knowledgeEditor.wikiBrowser.revertConfirm', { ver: selectedRevision.version })"
                @confirm="doRevert">
                <t-button size="small" variant="text" theme="warning" :loading="reverting">
                  <template #icon><t-icon name="rollback" /></template>
                  {{ t('knowledgeEditor.wikiBrowser.revertBtn') }}
                </t-button>
              </t-popconfirm>
            </div>
          </div>

          <div v-if="loadingDetail || diffLoading" class="wiki-rev-detail-loading">
            <t-loading size="small" />
            <span>{{ t('knowledgeEditor.wikiBrowser.loading') }}</span>
          </div>

          <div v-else-if="viewMode !== 'raw'" class="wiki-rev-diff">
            <div v-if="diffSections.length === 0" class="wiki-rev-empty-diff">
              {{ t('knowledgeEditor.wikiBrowser.revisionDiffEmpty') }}
            </div>
            <template v-for="section in diffSections" :key="section.field">
              <div class="wiki-rev-diff-block">
                <div class="wiki-rev-diff-block-label">{{ revisionDiffFieldLabel(section.field) }}</div>
                <pre class="wiki-rev-diff-block-body"><span v-for="(line, idx) in section.lines"
                  :key="`${section.field}-${idx}`"
                  :class="['wiki-rev-diff-line', `wiki-rev-diff-line--${line.type}`]">{{ diffPrefix(line.type) }}{{ line.text }}
</span></pre>
              </div>
            </template>
          </div>

          <div v-else-if="selectedRevision" class="wiki-rev-raw">
            <pre class="wiki-rev-raw-body">{{ rawRevisionText }}</pre>
          </div>
        </template>

        <div v-else class="wiki-rev-detail-hint">{{ t('knowledgeEditor.wikiBrowser.revisionSelectHint') }}</div>
      </div>
    </div>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import {
  listWikiRevisions,
  getWikiRevision,
  revertWikiPage,
  type WikiPage,
  type WikiPageRevision,
} from '@/api/wiki'
import { diffWikiRevision, type WikiRevisionDiffField, type WikiRevisionSnapshot } from '@/utils/wikiRevisionDiff'

type ViewMode = 'incremental' | 'cumulative' | 'raw'

interface DiffPair {
  fromVersion: number
  toVersion: number
  from: WikiRevisionSnapshot
  to: WikiRevisionSnapshot
}

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

const drawerVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val),
})

const drawerTitle = computed(() =>
  t('knowledgeEditor.wikiBrowser.historyTitle', { title: props.currentPage?.title || props.slug }),
)

const PAGE_SIZE = 50

const revisions = ref<WikiPageRevision[]>([])
const total = ref(0)
const loadingList = ref(false)

const selectedVersion = ref<number | null>(null)
const selectedRevision = ref<WikiPageRevision | null>(null)
const detailContent = ref('')
const loadingDetail = ref(false)
const viewMode = ref<ViewMode>('incremental')
const reverting = ref(false)
const diffLoading = ref(false)
const diffPair = ref<DiffPair | null>(null)

const snapshotCache = new Map<number, WikiRevisionSnapshot>()

const currentVersion = computed(() => props.currentPage?.version ?? null)

const isCurrentSelected = computed(() =>
  currentVersion.value !== null && selectedVersion.value === currentVersion.value,
)

const canShowDiff = computed(() => {
  if (selectedVersion.value === null || !props.currentPage) return false
  if (viewMode.value === 'raw') return true
  if (viewMode.value === 'cumulative') {
    return !isCurrentSelected.value && selectedVersion.value! < currentVersion.value!
  }
  // incremental: v1 diffs from empty; later versions diff from the previous one
  return selectedVersion.value! >= 1
})

const viewModeOptions = computed(() => {
  if (isCurrentSelected.value) return []
  const ver = selectedVersion.value!
  const cur = currentVersion.value!
  const options: Array<{ value: ViewMode; label: string }> = []
  options.push({ value: 'incremental', label: t('knowledgeEditor.wikiBrowser.revisionDiffIncremental') })
  if (ver < cur) {
    options.push({ value: 'cumulative', label: t('knowledgeEditor.wikiBrowser.revisionDiffCumulative') })
  }
  options.push({ value: 'raw', label: t('knowledgeEditor.wikiBrowser.revisionRaw') })
  return options
})

const versionRangeLabel = computed(() => {
  if (viewMode.value === 'raw') {
    return selectedRevision.value ? `v${selectedRevision.value.version}` : ''
  }
  if (!diffPair.value) return ''
  if (diffPair.value.fromVersion < 1) {
    return t('knowledgeEditor.wikiBrowser.revisionInitialRange', { ver: diffPair.value.toVersion })
  }
  return `v${diffPair.value.fromVersion} → v${diffPair.value.toVersion}`
})

const contextHint = computed(() => {
  if (viewMode.value === 'raw' && selectedRevision.value) {
    return [
      sourceLabel(selectedRevision.value.edit_source),
      formatShortTime(selectedRevision.value.edited_at),
    ].filter(Boolean).join(' · ')
  }
  if (viewMode.value === 'incremental') {
    if ((isCurrentSelected.value && (currentVersion.value ?? 0) <= 1)
      || selectedVersion.value === 1) {
      return t('knowledgeEditor.wikiBrowser.revisionInitialCreationHint')
    }
    return isCurrentSelected.value
      ? t('knowledgeEditor.wikiBrowser.revisionLatestChangeHint')
      : t('knowledgeEditor.wikiBrowser.revisionIncrementalHint', { ver: selectedVersion.value ?? 0 })
  }
  if (viewMode.value === 'cumulative') {
    return t('knowledgeEditor.wikiBrowser.revisionCumulativeHint')
  }
  return ''
})

const rawRevisionText = computed(() => {
  if (!selectedRevision.value) return detailContent.value
  const parts: string[] = []
  if (selectedRevision.value.title) parts.push(selectedRevision.value.title)
  if (selectedRevision.value.summary) {
    if (parts.length) parts.push('')
    parts.push(selectedRevision.value.summary)
  }
  if (detailContent.value) {
    if (parts.length) parts.push('')
    parts.push(detailContent.value)
  }
  return parts.join('\n')
})

const diffSections = computed(() => {
  if (!diffPair.value || viewMode.value === 'raw') return []
  return diffWikiRevision(diffPair.value.from, diffPair.value.to)
})

watch(
  () => [props.visible, props.slug] as const,
  ([visible]) => {
    if (visible && props.slug) {
      resetAndLoad()
    }
  },
)

watch(
  () => viewModeOptions.value,
  (options) => {
    if (options.length === 0) return
    if (!options.some((option) => option.value === viewMode.value)) {
      viewMode.value = options[0].value
    }
  },
)

watch(
  () => [selectedVersion.value, viewMode.value, props.currentPage?.version, props.currentPage?.content,
    props.currentPage?.title, props.currentPage?.summary] as const,
  () => {
    if (props.visible && viewMode.value !== 'raw') {
      void loadDiffPair()
    }
  },
)

function snapshotFromPage(page: WikiPage): WikiRevisionSnapshot {
  return {
    title: page.title || '',
    summary: page.summary || '',
    content: page.content || '',
  }
}

function snapshotFromRevisionData(data: WikiPageRevision, content: string): WikiRevisionSnapshot {
  return {
    title: data.title || '',
    summary: data.summary || '',
    content,
  }
}

async function loadVersionSnapshot(version: number): Promise<WikiRevisionSnapshot> {
  if (!props.currentPage) {
    return { title: '', summary: '', content: '' }
  }
  if (version === props.currentPage.version) {
    return snapshotFromPage(props.currentPage)
  }
  const cached = snapshotCache.get(version)
  if (cached) return cached
  const res = await getWikiRevision(props.kbId, props.slug, version)
  const data = (res as any).data || (res as any)
  const snap = snapshotFromRevisionData(data, data.content || '')
  snapshotCache.set(version, snap)
  return snap
}

let diffRequestSeq = 0

async function loadDiffPair() {
  const seq = ++diffRequestSeq
  if (!props.currentPage || selectedVersion.value === null || !canShowDiff.value) {
    diffPair.value = null
    diffLoading.value = false
    return
  }

  const currentVer = props.currentPage.version
  let fromVer = 0
  let toVer = 0

  if (viewMode.value === 'incremental') {
    toVer = isCurrentSelected.value ? currentVer : selectedVersion.value!
    fromVer = toVer - 1
  } else {
    fromVer = selectedVersion.value!
    toVer = currentVer
  }

  if (fromVer < 0 || toVer < 1 || fromVer >= toVer) {
    diffPair.value = null
    diffLoading.value = false
    return
  }

  diffLoading.value = true
  try {
    const from = fromVer < 1
      ? { title: '', summary: '', content: '' }
      : await loadVersionSnapshot(fromVer)
    if (seq !== diffRequestSeq) return
    const to = await loadVersionSnapshot(toVer)
    if (seq !== diffRequestSeq) return
    diffPair.value = { fromVersion: fromVer, toVersion: toVer, from, to }
  } catch (e: any) {
    if (seq !== diffRequestSeq) return
    diffPair.value = null
    MessagePlugin.error(e?.message || t('knowledgeEditor.wikiBrowser.revisionLoadFailed'))
  } finally {
    if (seq === diffRequestSeq) diffLoading.value = false
  }
}

function resetAndLoad() {
  detailRequestSeq++
  diffRequestSeq++
  snapshotCache.clear()
  revisions.value = []
  total.value = 0
  selectedVersion.value = props.currentPage?.version ?? null
  selectedRevision.value = null
  detailContent.value = ''
  loadingDetail.value = false
  diffPair.value = null
  diffLoading.value = false
  viewMode.value = 'incremental'
  loadList(0)
  void loadDiffPair()
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
  viewMode.value = 'incremental'
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
  viewMode.value = 'incremental'
  loadingDetail.value = true
  try {
    const res = await getWikiRevision(props.kbId, props.slug, rev.version)
    if (seq !== detailRequestSeq) return
    const data = (res as any).data || (res as any)
    selectedRevision.value = { ...rev, ...data }
    detailContent.value = data.content || ''
    snapshotCache.set(rev.version, snapshotFromRevisionData(data, data.content || ''))
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

function diffPrefix(type: 'same' | 'add' | 'del'): string {
  return type === 'add' ? '+ ' : type === 'del' ? '- ' : '  '
}

function revisionDiffFieldLabel(field: WikiRevisionDiffField): string {
  switch (field) {
    case 'title':
      return t('knowledgeEditor.wikiBrowser.revisionDiffTitle')
    case 'summary':
      return t('knowledgeEditor.wikiBrowser.revisionDiffSummary')
    default:
      return t('knowledgeEditor.wikiBrowser.revisionDiffContent')
  }
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
  padding: 14px 18px 18px;
}

.wiki-rev-detail-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 14px;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.wiki-rev-detail-context {
  min-width: 0;
  flex: 1;
}

.wiki-rev-detail-range {
  font-family: var(--td-font-family-mono, monospace);
  font-size: 15px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
}

.wiki-rev-detail-sub {
  margin-top: 4px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.wiki-rev-detail-controls {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.wiki-rev-view-switch {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
}

.wiki-rev-view-switch-btn {
  padding: 5px 10px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.4;
  white-space: nowrap;
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;
}

.wiki-rev-view-switch-btn:hover {
  color: var(--td-text-color-primary);
}

.wiki-rev-view-switch-btn.active {
  color: var(--td-brand-color);
  background: var(--td-bg-color-container);
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
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.wiki-rev-empty-diff {
  padding: 24px 0;
  text-align: center;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.wiki-rev-diff-block {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.wiki-rev-diff-block-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--td-text-color-placeholder);
}

.wiki-rev-diff-block-body {
  overflow: auto;
  margin: 0;
  padding: 10px 12px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 8px;
  font-size: 12px;
  line-height: 1.7;
  font-family: var(--td-font-family-mono, monospace);
  white-space: pre-wrap;
  word-break: break-word;
}

.wiki-rev-raw {
  flex: 1;
  min-height: 0;
  overflow: auto;
}

.wiki-rev-raw-body {
  margin: 0;
  padding: 12px 14px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 8px;
  font-size: 13px;
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

  .t-drawer__body {
    flex: 1;
    min-height: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .setting-drawer__body {
    flex: 1;
    min-height: 0;
    padding: 0;
    gap: 0;
    animation: none;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
}
</style>
