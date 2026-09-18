<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatFileSize, getFileIcon } from '@/utils/files'
import { itemPhase, type UploadItem } from '@/stores/uploadTasksState'
import ProgressRing from './ProgressRing.vue'

const props = defineProps<{ item: UploadItem }>()
const emit = defineEmits<{
  cancel: [id: string]
  retry: [id: string]
  open: [item: UploadItem]
}>()

const { t } = useI18n()

const phase = computed(() => itemPhase(props.item))
/** Folder uploads repeat names like README.md; the subfolder tells them apart. */
const folder = computed(() => props.item.relativePath.split('/').slice(0, -1).join('/'))
const ratio = computed(() => (props.item.size > 0 ? props.item.loaded / props.item.size : 0))
const bytes = (value: number) => formatFileSize(value) || '0 B'

type RowAction = 'cancel' | 'retry' | 'open'

const action = computed<RowAction | null>(() => {
  const { item } = props
  switch (phase.value) {
    case 'waiting':
    case 'uploading':
      return 'cancel'
    case 'saving':
      return null
    case 'failed':
    case 'cancelled':
      if (item.transfer === 'failed' || item.transfer === 'cancelled') return 'retry'
      return item.knowledgeId && item.parseStatus !== 'deleted' ? 'open' : null
    default:
      return item.knowledgeId ? 'open' : null
  }
})

const actionMeta: Record<RowAction, { icon: string; label: string }> = {
  cancel: { icon: 'close', label: 'uploadTasks.cancel' },
  retry: { icon: 'refresh', label: 'uploadTasks.retry' },
  open: { icon: 'chevron-right', label: 'uploadTasks.open' },
}

/** Failure reason, shown after the phase label and in full on hover. */
const reason = computed(() => {
  if (phase.value !== 'failed') return ''
  return (props.item.transfer === 'failed' ? props.item.error : props.item.parseError) || ''
})

const meta = computed(() => {
  const { item } = props
  switch (phase.value) {
    case 'waiting':
      return `${t('uploadTasks.phaseWaiting')} · ${bytes(item.size)}`
    case 'uploading':
      return `${bytes(item.loaded)} / ${bytes(item.size)}`
    case 'saving':
      return t('uploadTasks.phaseSaving')
    case 'parsing':
      return t(item.parseStatus === 'processing' ? 'uploadTasks.phaseParsing' : 'uploadTasks.phasePending')
    case 'ready':
      return t(item.parseStatus === 'finalizing' ? 'uploadTasks.phaseFinalizing' : 'uploadTasks.phaseReady')
    case 'failed': {
      const label = t(item.transfer === 'failed' ? 'uploadTasks.phaseUploadFailed' : 'uploadTasks.phaseParseFailed')
      return reason.value ? `${label} · ${reason.value}` : label
    }
    case 'duplicate':
      return t('uploadTasks.phaseDuplicate')
    case 'cancelled':
      return t(item.parseStatus === 'deleted' ? 'uploadTasks.phaseDeleted' : 'uploadTasks.phaseCancelled')
  }
  return ''
})

const runAction = () => {
  if (action.value === 'cancel') emit('cancel', props.item.id)
  else if (action.value === 'retry') emit('retry', props.item.id)
  else if (action.value === 'open') emit('open', props.item)
}

const handleRowClick = () => {
  if (action.value === 'open') emit('open', props.item)
}
</script>

<template>
  <div
    class="upload-task-row"
    :class="[`is-${phase}`, { 'is-openable': action === 'open' }]"
    @click="handleRowClick"
  >
    <span class="row-file-icon" aria-hidden="true">
      <t-icon :name="getFileIcon(item.name)" />
    </span>
    <span class="row-main">
      <span class="row-name" :title="item.relativePath || item.name">
        <span class="row-name-text">{{ item.name }}</span>
        <span v-if="folder" class="row-name-folder">{{ folder }}</span>
      </span>
      <span class="row-meta" :title="reason || undefined">{{ meta }}</span>
    </span>
    <span class="row-end">
      <span class="row-status" :class="{ 'has-action': !!action }" aria-hidden="true">
        <ProgressRing v-if="phase === 'uploading'" :value="ratio" :size="20" :stroke="2.5" />
        <t-icon v-else-if="phase === 'waiting'" name="time" />
        <t-icon v-else-if="phase === 'saving' || phase === 'parsing'" name="loading" class="row-spin" />
        <t-icon v-else-if="phase === 'ready'" name="check-circle-filled" />
        <t-icon v-else-if="phase === 'failed'" name="error-circle-filled" />
        <t-icon v-else-if="phase === 'duplicate'" name="info-circle-filled" />
        <t-icon v-else name="minus-circle" />
      </span>
      <button
        v-if="action"
        type="button"
        class="row-action"
        :aria-label="t(actionMeta[action].label)"
        :title="t(actionMeta[action].label)"
        @click.stop="runAction"
      >
        <t-icon :name="actionMeta[action].icon" />
      </button>
    </span>
  </div>
</template>

<style scoped lang="less">
.upload-task-row {
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
  padding: var(--app-space-2);
  border-radius: var(--app-radius-md);
  transition: background-color var(--app-motion-fast) ease;

  &:hover,
  &:focus-within {
    background: var(--td-bg-color-container-hover);
  }

  &.is-openable {
    cursor: pointer;
  }
}

.row-file-icon {
  display: grid;
  flex-shrink: 0;
  place-items: center;
  width: 32px;
  height: 32px;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-2xl);
}

.row-main {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}

.row-name-text,
.row-name-folder,
.row-meta {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row-name {
  display: flex;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  line-height: 20px;
}

.row-name-text {
  flex: 0 1 auto;
  min-width: 0;
}

// Gives way long before the file name does.
.row-name-folder {
  flex: 0 100 auto;
  min-width: 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
}

.row-meta {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  font-variant-numeric: tabular-nums;
  line-height: 18px;

  .is-failed & {
    color: var(--td-error-color);
  }

  .is-duplicate & {
    color: var(--td-warning-color);
  }
}

.row-end {
  display: grid;
  flex-shrink: 0;
  place-items: center;
  width: 28px;
  height: 28px;

  > * {
    grid-area: 1 / 1;
  }
}

.row-status {
  display: grid;
  place-items: center;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xl);

  .is-uploading &,
  .is-saving &,
  .is-parsing &,
  .is-ready & {
    color: var(--td-brand-color);
  }

  .is-failed & {
    color: var(--td-error-color);
  }

  .is-duplicate & {
    color: var(--td-warning-color);
  }
}

.row-spin {
  animation: wk-spin 0.8s linear infinite;
}

.row-action {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xl);
  cursor: pointer;
  opacity: 0;
  transition: opacity var(--app-motion-fast) ease, background-color var(--app-motion-fast) ease;

  &:hover {
    background: var(--td-bg-color-container-active);
    color: var(--td-text-color-primary);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: -2px;
    opacity: 1;
  }
}

// The status icon and the row action share one slot: hovering (or focusing)
// a row swaps the indicator for what the user can do about it.
.upload-task-row:hover,
.upload-task-row:focus-within {
  .row-status.has-action {
    opacity: 0;
  }

  .row-action {
    opacity: 1;
  }
}

// Without hover there is no way to discover the swap, so show actions outright.
@media (hover: none) {
  .row-status.has-action {
    opacity: 0;
  }

  .row-action {
    opacity: 1;
  }
}

@media (prefers-reduced-motion: reduce) {
  .row-spin {
    animation: none;
  }
}
</style>
