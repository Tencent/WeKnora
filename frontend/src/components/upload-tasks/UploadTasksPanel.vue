<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useUploadTasksStore, type UploadBatch } from '@/stores/uploadTasks'
import {
  estimateRate,
  estimateRemainingSeconds,
  itemPhase,
  splitDuration,
  type RateSample,
  type UploadItem,
} from '@/stores/uploadTasksState'
import { formatFileSize } from '@/utils/files'
import ProgressRing from './ProgressRing.vue'
import UploadTaskRow from './UploadTaskRow.vue'

/** A run that finished cleanly leaves on its own after this long without hover. */
const AUTO_DISMISS_MS = 6000

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const store = useUploadTasksStore()
const { items, batches, summary, visible, collapsed } = storeToRefs(store)

const hovering = ref(false)
const filter = ref<'all' | 'issues'>('all')

// A panel dismissed under the pointer never sees mouseleave.
watch(visible, shown => {
  if (!shown) hovering.value = false
})

const issueCount = computed(() => summary.value.failed + summary.value.duplicate + summary.value.cancelled)
const isUploading = computed(() => summary.value.stage === 'uploading')
const pendingTransfers = computed(() => summary.value.waiting + summary.value.uploading)
const parseSettled = computed(() => summary.value.uploaded - summary.value.parsing)

watch(issueCount, count => {
  if (count === 0) filter.value = 'all'
})

// ---- transfer rate ------------------------------------------------------

const samples: RateSample[] = []
const rate = ref(0)
let sampleTimer: ReturnType<typeof setInterval> | null = null

const takeSample = () => {
  const bytes = summary.value.sentBytes
  const last = samples[samples.length - 1]
  // A failed, cancelled or retried transfer moves the count backwards; start over.
  if (last && bytes < last.bytes) samples.length = 0
  samples.push({ at: Date.now(), bytes })
  if (samples.length > 20) samples.shift()
  rate.value = estimateRate(samples)
}

const stopSampling = () => {
  if (sampleTimer) clearInterval(sampleTimer)
  sampleTimer = null
  samples.length = 0
  rate.value = 0
}

watch(isUploading, uploading => {
  if (uploading && !sampleTimer) {
    takeSample()
    sampleTimer = setInterval(takeSample, 1000)
  } else if (!uploading) {
    stopSampling()
  }
}, { immediate: true })

const remainingText = computed(() => {
  const seconds = estimateRemainingSeconds(summary.value.totalBytes - summary.value.loadedBytes, rate.value)
  if (seconds === null) return ''
  const { unit, value } = splitDuration(seconds)
  return t('uploadTasks.remaining', { time: t(`uploadTasks.eta.${unit}`, { n: value }) })
})

// ---- header -------------------------------------------------------------

const batchLabel = (batch: UploadBatch) =>
  batch.targetFolder ? `${batch.kbName} / ${batch.targetFolder}` : batch.kbName

const destination = computed(() => {
  const kbIds = new Set(batches.value.map(batch => batch.kbId))
  if (kbIds.size > 1) return t('uploadTasks.destinationMany', { count: kbIds.size })
  const batch = batches.value[0]
  if (!batch) return ''
  return t('uploadTasks.destination', { name: batchLabel(batch) })
})

const headline = computed(() => {
  const s = summary.value
  if (s.stage === 'uploading') {
    return t('uploadTasks.titleUploading', { done: s.transferSettled, total: s.transferTotal })
  }
  if (s.stage === 'parsing') {
    return t('uploadTasks.titleParsing', { done: parseSettled.value, total: s.uploaded })
  }
  if (s.ready === 0 && s.failed + s.duplicate === 0) return t('uploadTasks.titleCancelled')
  if (issueCount.value === 0) return t('uploadTasks.titleDone')
  return t('uploadTasks.titleDoneWithIssues', { ok: s.ready, bad: issueCount.value })
})

const subline = computed(() => {
  const s = summary.value
  if (s.stage !== 'uploading') return destination.value
  const transferred = `${formatFileSize(s.loadedBytes) || '0 B'} / ${formatFileSize(s.totalBytes) || '0 B'}`
  return remainingText.value ? `${transferred} · ${remainingText.value}` : transferred
})

type BadgeTone = 'brand' | 'success' | 'warning' | 'neutral'

const badge = computed<{ tone: BadgeTone; icon: string; ring: number | null }>(() => {
  const s = summary.value
  if (s.stage === 'uploading') {
    return { tone: 'brand', icon: 'arrow-up', ring: s.totalBytes > 0 ? s.loadedBytes / s.totalBytes : 0 }
  }
  if (s.stage === 'parsing') {
    return { tone: 'brand', icon: 'file-search', ring: s.uploaded > 0 ? parseSettled.value / s.uploaded : 0 }
  }
  if (s.ready === 0 && s.failed + s.duplicate === 0) return { tone: 'neutral', icon: 'close', ring: null }
  if (issueCount.value > 0) return { tone: 'warning', icon: 'error', ring: null }
  return { tone: 'success', icon: 'check', ring: null }
})

// ---- progress bar -------------------------------------------------------

const percent = (fraction: number) => `${(fraction * 100).toFixed(2)}%`

const overallPercent = computed(() => {
  const { bar } = summary.value
  return Math.round((bar.ready + bar.failed + bar.duplicate) * 100)
})

const legend = computed(() => {
  const s = summary.value
  return [
    { key: 'ready', label: t('uploadTasks.legend.ready'), count: s.ready },
    { key: 'active', label: t('uploadTasks.legend.active'), count: s.uploading + s.parsing },
    { key: 'waiting', label: t('uploadTasks.legend.waiting'), count: s.waiting },
    { key: 'failed', label: t('uploadTasks.legend.failed'), count: s.failed },
    { key: 'duplicate', label: t('uploadTasks.legend.duplicate'), count: s.duplicate },
  ].filter(entry => entry.count > 0)
})

const hint = computed(() => {
  if (summary.value.stage === 'uploading') return { icon: 'info-circle', text: t('uploadTasks.hintUploading') }
  if (summary.value.stage === 'parsing') return { icon: 'check-circle', text: t('uploadTasks.hintParsing') }
  return null
})

// ---- list ---------------------------------------------------------------

const isIssue = (item: UploadItem) => {
  const phase = itemPhase(item)
  return phase === 'failed' || phase === 'duplicate' || phase === 'cancelled'
}

const groups = computed(() => {
  const visibleItems = filter.value === 'issues' ? items.value.filter(isIssue) : items.value
  const byBatch = new Map<string, UploadItem[]>()
  for (const item of visibleItems) {
    if (!byBatch.has(item.batchId)) byBatch.set(item.batchId, [])
    byBatch.get(item.batchId)!.push(item)
  }
  return batches.value
    .filter(batch => byBatch.has(batch.id))
    .map(batch => ({ batch, label: batchLabel(batch), items: byBatch.get(batch.id)! }))
})

const openItem = (item: UploadItem) => {
  const kbId = store.batchById.get(item.batchId)?.kbId
  if (!kbId || !item.knowledgeId) return
  // Same knowledge base page: vue-router dedupes an identical navigation, so
  // ask the page to open the document directly (as the command palette does).
  if (route.params.kbId === kbId) {
    window.dispatchEvent(new CustomEvent('weknora:open-knowledge', {
      detail: { kbId, knowledgeId: item.knowledgeId },
    }))
    return
  }
  router.push({ path: `/platform/knowledge-bases/${kbId}`, query: { knowledge_id: item.knowledgeId } })
}

// ---- auto dismiss -------------------------------------------------------

let dismissTimer: ReturnType<typeof setTimeout> | null = null

const finishedCleanly = computed(() => {
  const s = summary.value
  return s.stage === 'done' && s.ready > 0 && issueCount.value === 0
})

watch([finishedCleanly, hovering], ([clean, hover]) => {
  if (dismissTimer) clearTimeout(dismissTimer)
  dismissTimer = null
  if (clean && !hover) dismissTimer = setTimeout(() => store.dismiss(), AUTO_DISMISS_MS)
})

// ---- leaving the page ---------------------------------------------------

// Closing the tab kills in-flight requests; parsing is server side and survives.
const handleBeforeUnload = (event: BeforeUnloadEvent) => {
  if (!isUploading.value) return
  event.preventDefault()
  event.returnValue = ''
}

onMounted(() => {
  window.addEventListener('beforeunload', handleBeforeUnload)
})

onBeforeUnmount(() => {
  window.removeEventListener('beforeunload', handleBeforeUnload)
  stopSampling()
  if (dismissTimer) clearTimeout(dismissTimer)
})
</script>

<template>
  <Transition name="upload-tasks-panel">
    <section
      v-if="visible && items.length > 0"
      class="upload-tasks-panel"
      role="region"
      :aria-label="t('uploadTasks.panelLabel')"
      @mouseenter="hovering = true"
      @mouseleave="hovering = false"
    >
      <header class="panel-header">
        <span class="panel-badge" :class="`is-${badge.tone}`" aria-hidden="true">
          <ProgressRing v-if="badge.ring !== null" class="panel-badge-ring" :value="badge.ring" :size="36" :stroke="2.5" />
          <t-icon :name="badge.icon" />
        </span>
        <div class="panel-heading">
          <div class="panel-title" aria-live="polite">{{ headline }}</div>
          <div class="panel-subtitle" :title="subline">{{ subline }}</div>
        </div>
        <div class="panel-header-actions">
          <t-button
            variant="text"
            shape="square"
            size="small"
            :aria-label="collapsed ? t('uploadTasks.expand') : t('uploadTasks.collapse')"
            :aria-expanded="!collapsed"
            @click="store.toggleCollapsed()"
          >
            <template #icon><t-icon :name="collapsed ? 'chevron-up' : 'chevron-down'" /></template>
          </t-button>
          <t-popconfirm
            v-if="isUploading"
            theme="warning"
            attach="body"
            placement="top-right"
            :content="t('uploadTasks.closeConfirm', { count: pendingTransfers })"
            :confirm-btn="{ content: t('uploadTasks.closeConfirmOk'), theme: 'danger' }"
            :cancel-btn="{ content: t('uploadTasks.closeConfirmKeep') }"
            @confirm="store.dismiss()"
          >
            <t-button variant="text" shape="square" size="small" :aria-label="t('uploadTasks.close')">
              <template #icon><t-icon name="close" /></template>
            </t-button>
          </t-popconfirm>
          <t-button
            v-else
            variant="text"
            shape="square"
            size="small"
            :aria-label="t('uploadTasks.close')"
            @click="store.dismiss()"
          >
            <template #icon><t-icon name="close" /></template>
          </t-button>
        </div>
      </header>

      <div class="panel-progress">
        <div
          class="panel-bar"
          role="progressbar"
          aria-valuemin="0"
          aria-valuemax="100"
          :aria-valuenow="overallPercent"
        >
          <span class="bar-segment is-ready" :style="{ width: percent(summary.bar.ready) }" />
          <span class="bar-segment is-active" :style="{ width: percent(summary.bar.active) }" />
          <span class="bar-segment is-failed" :style="{ width: percent(summary.bar.failed) }" />
          <span class="bar-segment is-duplicate" :style="{ width: percent(summary.bar.duplicate) }" />
        </div>
        <ul v-if="!collapsed && legend.length > 0" class="panel-legend">
          <li v-for="entry in legend" :key="entry.key" :class="`is-${entry.key}`">
            <i aria-hidden="true" />{{ entry.label }}<b>{{ entry.count }}</b>
          </li>
        </ul>
      </div>

      <div v-if="!collapsed" class="panel-body">
        <p v-if="hint" class="panel-hint" :class="`is-${summary.stage}`">
          <t-icon :name="hint.icon" />
          <span>{{ hint.text }}</span>
        </p>

        <div v-if="issueCount > 0 && items.length > 1" class="panel-filter" role="tablist">
          <button
            type="button"
            role="tab"
            :aria-selected="filter === 'all'"
            :class="{ 'is-active': filter === 'all' }"
            @click="filter = 'all'"
          >
            {{ t('uploadTasks.filterAll') }}<span>{{ items.length }}</span>
          </button>
          <button
            type="button"
            role="tab"
            :aria-selected="filter === 'issues'"
            :class="{ 'is-active': filter === 'issues' }"
            @click="filter = 'issues'"
          >
            {{ t('uploadTasks.filterIssues') }}<span class="is-issue">{{ issueCount }}</span>
          </button>
        </div>

        <div class="panel-list">
          <template v-for="group in groups" :key="group.batch.id">
            <div v-if="batches.length > 1" class="panel-group" :title="group.label">
              <span>{{ group.label }}</span>
              <span class="panel-group-count">{{ group.items.length }}</span>
            </div>
            <UploadTaskRow
              v-for="item in group.items"
              :key="item.id"
              :item="item"
              @cancel="store.cancelItem"
              @retry="store.retryItem"
              @open="openItem"
            />
          </template>
        </div>

        <footer v-if="isUploading || summary.retryable > 0" class="panel-footer">
          <t-button v-if="isUploading" variant="text" size="small" @click="store.cancelAll()">
            {{ t('uploadTasks.cancelAll') }}
          </t-button>
          <t-button
            v-if="summary.retryable > 0"
            class="panel-footer-retry"
            theme="primary"
            variant="text"
            size="small"
            @click="store.retryFailed()"
          >
            <template #icon><t-icon name="refresh" /></template>
            {{ t('uploadTasks.retryFailed', { count: summary.retryable }) }}
          </t-button>
        </footer>
      </div>
    </section>
  </Transition>
</template>

<style scoped lang="less">
.upload-tasks-panel {
  position: fixed;
  right: var(--app-space-6);
  bottom: var(--app-space-6);
  // Above page content, below modal shells and drawers.
  z-index: var(--z-dropdown);
  display: flex;
  flex-direction: column;
  width: 380px;
  max-width: calc(100vw - var(--app-space-8));
  max-height: calc(100vh - var(--app-space-10) * 2);
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xl);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-2);
  font-family: var(--app-font-family);

  @media (max-width: 640px) {
    right: var(--app-space-4);
    bottom: var(--app-space-4);
  }
}

.upload-tasks-panel-enter-active,
.upload-tasks-panel-leave-active {
  transition: opacity var(--app-motion-slow) ease, transform var(--app-motion-slow) ease;
}

.upload-tasks-panel-enter-from,
.upload-tasks-panel-leave-to {
  opacity: 0;
  transform: translateY(12px);
}

// ---- header ---------------------------------------------------------------

.panel-header {
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
  padding: var(--app-space-4) var(--app-space-3) 0 var(--app-space-4);
}

.panel-badge {
  position: relative;
  display: grid;
  flex-shrink: 0;
  place-items: center;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  font-size: var(--app-text-xl);

  &.is-brand,
  &.is-success {
    background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
    color: var(--td-brand-color);
  }

  &.is-warning {
    background: color-mix(in srgb, var(--td-warning-color) 12%, transparent);
    color: var(--td-warning-color);
  }

  &.is-neutral {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-placeholder);
  }
}

.panel-badge-ring {
  position: absolute;
  inset: 0;
}

.panel-heading {
  flex: 1;
  min-width: 0;
}

.panel-title {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-base);
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  line-height: 22px;
}

.panel-subtitle {
  overflow: hidden;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  font-variant-numeric: tabular-nums;
  line-height: 18px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.panel-header-actions {
  display: flex;
  flex-shrink: 0;
  align-self: flex-start;
  gap: 2px;
  color: var(--td-text-color-secondary);
}

// ---- progress -------------------------------------------------------------

.panel-progress {
  padding: var(--app-space-3) var(--app-space-4);
}

.panel-bar {
  display: flex;
  height: 6px;
  overflow: hidden;
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-component);
}

.bar-segment {
  height: 100%;
  transition: width var(--app-motion-slow) ease;

  &.is-ready {
    background: var(--td-brand-color);
  }

  // Uploaded or uploading but not yet searchable: a moving sheen says "working".
  &.is-active {
    background-color: color-mix(in srgb, var(--td-brand-color) 32%, transparent);
    background-image: linear-gradient(
      90deg,
      transparent 0%,
      color-mix(in srgb, var(--td-brand-color) 38%, transparent) 50%,
      transparent 100%
    );
    background-size: 200% 100%;
    animation: bar-sheen 1.6s linear infinite;
  }

  &.is-failed {
    background: var(--td-error-color);
  }

  &.is-duplicate {
    background: var(--td-warning-color);
  }
}

@keyframes bar-sheen {
  from {
    background-position: 100% 0;
  }

  to {
    background-position: -100% 0;
  }
}

.panel-legend {
  display: flex;
  flex-wrap: wrap;
  gap: var(--app-space-1) var(--app-space-3);
  margin: var(--app-space-2) 0 0;
  padding: 0;
  list-style: none;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  line-height: 18px;

  li {
    display: inline-flex;
    align-items: center;
    gap: var(--app-space-1);
  }

  i {
    width: 6px;
    height: 6px;
    border-radius: 50%;
  }

  b {
    color: var(--td-text-color-primary);
    font-variant-numeric: tabular-nums;
    font-weight: 600;
  }

  .is-ready i {
    background: var(--td-brand-color);
  }

  .is-active i {
    background: color-mix(in srgb, var(--td-brand-color) 45%, transparent);
  }

  .is-waiting i {
    background: var(--td-bg-color-component-active);
  }

  .is-failed i {
    background: var(--td-error-color);
  }

  .is-duplicate i {
    background: var(--td-warning-color);
  }
}

// ---- body -----------------------------------------------------------------

.panel-body {
  display: flex;
  flex-direction: column;
  min-height: 0;
  border-top: 1px solid var(--td-component-stroke);
}

.panel-hint {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin: var(--app-space-3) var(--app-space-4) 0;
  padding: var(--app-space-2) var(--app-space-3);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  line-height: 18px;

  .t-icon {
    flex-shrink: 0;
    margin-top: 2px;
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-base);
  }

  &.is-parsing {
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);

    .t-icon {
      color: var(--td-brand-color);
    }
  }
}

.panel-filter {
  display: inline-flex;
  align-self: flex-start;
  gap: 2px;
  margin: var(--app-space-3) var(--app-space-4) 0;
  padding: 2px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);

  button {
    display: inline-flex;
    align-items: center;
    gap: var(--app-space-1);
    padding: 2px var(--app-space-3);
    border: 0;
    border-radius: var(--app-radius-sm);
    background: transparent;
    color: var(--td-text-color-secondary);
    font: inherit;
    font-size: var(--app-text-sm);
    line-height: 20px;
    cursor: pointer;
    transition: background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

    &:hover {
      color: var(--td-text-color-primary);
    }

    &.is-active {
      background: var(--td-bg-color-container);
      color: var(--td-text-color-primary);
      box-shadow: var(--td-shadow-1);
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 1px;
    }
  }

  span {
    color: var(--td-text-color-placeholder);
    font-variant-numeric: tabular-nums;

    &.is-issue {
      color: var(--td-error-color);
    }
  }
}

.panel-list {
  flex: 1;
  min-height: 0;
  max-height: 300px;
  padding: var(--app-space-2);
  overflow-y: auto;
  overscroll-behavior: contain;
}

.panel-group {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2) var(--app-space-2) var(--app-space-1);
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  font-weight: 500;

  span:first-child {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.panel-group-count {
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}

.panel-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--app-space-2) var(--app-space-3);
  border-top: 1px solid var(--td-component-stroke);
}

.panel-footer-retry {
  margin-left: auto;
}

@media (prefers-reduced-motion: reduce) {
  .bar-segment.is-active {
    animation: none;
  }

  .upload-tasks-panel-enter-active,
  .upload-tasks-panel-leave-active,
  .bar-segment {
    transition: none;
  }
}
</style>
