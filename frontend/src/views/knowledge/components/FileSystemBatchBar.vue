<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { selectionCapabilities, selectionCount } from '@/views/knowledge/folderModel';
import type { FileSystemSelection } from '@/types/knowledgeFolder';

const props = defineProps<{
  /** Current typed selection. Drives count + capability gating. */
  selection: FileSystemSelection;
  deleteLoading?: boolean;
  reparseLoading?: boolean;
  moveLoading?: boolean;
  // When true the reparse action is disabled because the known document count
  // exceeds the backend per-request cap (REPARSE_LIMIT). Pre-disable avoids a
  // rejected round-trip; the button title explains why it is disabled.
  // (Folder selections disable reparse separately via reparseBlockedByFolder.)
  reparseOverLimit?: boolean;
  // When true the bar stays visible even with 0 selections, so users can exit
  // batch mode from here without selecting anything first (mirrors
  // DocumentBatchBar's `visible` prop).
  visible?: boolean;
}>();

const emit = defineEmits<{
  (e: 'clear'): void;
  (e: 'move-folder'): void;
  (e: 'delete'): void;
  (e: 'reparse'): void;
}>();

const { t } = useI18n();

const capabilities = computed(() => selectionCapabilities(props.selection));
const totalCount = computed(() => selectionCount(props.selection));
const folderCount = computed(() => props.selection.folderIds.size);
const busy = computed(
  () =>
    props.deleteLoading ||
    props.reparseLoading ||
    props.moveLoading,
);
// Reparse is documents-only - any folder in the selection disables the action.
// The title explains why (folder unsupported vs. over-limit) so the user knows
// to deselect folders rather than guess at a hung button.
const reparseBlockedByFolder = computed(() => folderCount.value > 0);
const reparseTitle = computed(() => {
  if (reparseBlockedByFolder.value) return t('knowledgeBase.reparseFolderUnsupported');
  if (props.reparseOverLimit) return t('knowledgeBase.folderReparseLimit');
  return undefined;
});
</script>

<template>
  <transition name="fs-batch-bar-fade">
    <div v-if="visible || totalCount > 0" class="fs-batch-bar" role="region"
      :aria-label="t('knowledgeBase.selectedCount', { count: totalCount })">
      <div class="fs-batch-bar-inner">
        <div class="fs-batch-bar-left">
          <span class="fs-batch-bar-count">
            {{ t('knowledgeBase.selectedCount', { count: totalCount }) }}
          </span>
          <t-button variant="text" theme="default" size="small" class="fs-batch-bar-clear"
            :disabled="busy" @click="emit('clear')">
            {{ t('knowledgeBase.clearSelection') }}
          </t-button>
        </div>
        <div class="fs-batch-bar-actions">
          <t-button v-if="capabilities.canMove" theme="default" variant="outline" size="small"
            :disabled="totalCount === 0 || busy" :loading="moveLoading" @click="emit('move-folder')">
            <template #icon><t-icon name="folder" size="14px" /></template>
            {{ t('knowledgeBase.batchMoveFolder') }}
          </t-button>

          <t-button v-if="capabilities.canReparse" theme="default" variant="outline" size="small"
            :disabled="totalCount === 0 || busy || props.reparseOverLimit || reparseBlockedByFolder" :loading="reparseLoading"
            :title="reparseTitle"
            @click="emit('reparse')">
            <template #icon><t-icon name="refresh" size="14px" /></template>
            {{ t('knowledgeBase.rebuildDocument') }}
          </t-button>

          <t-button v-if="capabilities.canDelete" theme="danger" variant="outline" size="small"
            :disabled="totalCount === 0 || busy" :loading="deleteLoading" @click="emit('delete')">
            <template #icon><t-icon name="delete" size="14px" /></template>
            {{ t('knowledgeBase.batchDelete') }}
          </t-button>
        </div>
      </div>
    </div>
  </transition>
</template>

<style scoped lang="less">
.fs-batch-bar {
  position: relative;
  z-index: 5;
  width: 100%;
  max-width: 560px;
  margin: 0 auto;
  padding: 0 4px;
  box-sizing: border-box;
}

.fs-batch-bar-inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  box-shadow: 0 6px 16px rgba(0, 0, 0, 0.08);
}

.fs-batch-bar-left {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  flex: 1;
}

.fs-batch-bar-count {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.fs-batch-bar-clear {
  flex-shrink: 0;
  padding: 0 6px !important;
  height: 28px !important;
  font-size: 12px;
  color: var(--td-text-color-secondary) !important;

  &:hover {
    color: var(--td-brand-color) !important;
  }
}

.fs-batch-bar-actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 8px;
}

.fs-batch-bar-fade-enter-active,
.fs-batch-bar-fade-leave-active {
  transition: transform 0.2s ease, opacity 0.2s ease;
}

.fs-batch-bar-fade-enter-from,
.fs-batch-bar-fade-leave-to {
  opacity: 0;
  transform: translateY(6px);
}
</style>
