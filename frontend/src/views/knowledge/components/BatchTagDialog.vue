<template>
  <t-dialog :visible="visible" :footer="false" width="420px" dialog-class-name="batch-tag-dialog"
    :close-on-overlay-click="false" destroy-on-close @close="handleClose">
    <template #header>
      <div class="batch-tag-heading">
        <div class="batch-tag-heading-row">
          <t-icon name="discount" size="16px" class="batch-tag-heading-icon" aria-hidden="true" />
          <span class="batch-tag-title">{{ $t('knowledgeBase.batchTagDialogHeading') }}</span>
        </div>
        <p class="batch-tag-subtitle">{{ $t('knowledgeBase.batchTagSubtitle', { count }) }}</p>
      </div>
    </template>

    <KnowledgeTagPicker v-if="visible" :kb-id="kbId" :selected-ids="selectedIds"
      @update:selected-ids="selectedIds = $event" @changed="emit('tags-changed', $event)" @busy-change="tagBusy = $event" />

    <div class="batch-tag-footer">
      <span class="batch-tag-selected-count">
        {{ $t('knowledgeBase.tagSelectedCount', { count: selectedIds.length }) }}
      </span>
      <div class="batch-tag-footer-right">
        <t-button variant="outline" size="small" :disabled="confirmLoading" @click="handleClose">
          {{ $t('common.cancel') }}
        </t-button>
        <t-button theme="primary" size="small" :loading="confirmLoading" :disabled="tagBusy" @click="handleConfirm">
          {{ $t('common.confirm') }}
        </t-button>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import KnowledgeTagPicker from './KnowledgeTagPicker.vue';

const props = defineProps<{
  visible: boolean;
  count: number;
  kbId: string;
  preSelectedTagIds?: string[];
  confirmLoading?: boolean;
}>();
const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void;
  (e: 'confirm', tagIds: string[]): void;
  (e: 'tags-changed', payload?: { deletedTagId?: string }): void;
}>();
const selectedIds = ref<string[]>([]);
const tagBusy = ref(false);
watch(() => props.visible, visible => {
  if (visible) selectedIds.value = [...(props.preSelectedTagIds ?? [])];
}, { immediate: true });

function handleConfirm() {
  if (props.confirmLoading || tagBusy.value) return;
  emit('confirm', [...selectedIds.value]);
}
function handleClose() {
  if (props.confirmLoading || tagBusy.value) return;
  emit('update:visible', false);
}
</script>

<style>
.batch-tag-dialog {
  overflow: hidden;
  padding: 0;
  border-radius: var(--app-radius-xs);
}

.batch-tag-dialog .t-dialog__header {
  min-height: auto;
  padding: 20px 20px 0;
}

.batch-tag-dialog .t-dialog__body {
  padding: 0 20px 20px;
}

.batch-tag-dialog .t-dialog__close {
  top: 16px;
  right: 16px;
  width: 28px;
  height: 28px;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-secondary);
  transition: background 0.18s ease;
}

.batch-tag-dialog .t-dialog__close:hover {
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container-hover);
}

@media (max-width: 480px) {
  .batch-tag-dialog {
    width: calc(100vw - 24px) !important;
  }
}
</style>

<style scoped>
.batch-tag-heading {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  padding-right: 28px;
}

.batch-tag-heading-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.batch-tag-heading-icon {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
}

.batch-tag-title {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-lg);
  font-weight: 600;
  line-height: 22px;
  letter-spacing: 0.2px;
}

.batch-tag-subtitle {
  margin: 0;
  min-width: 0;
  overflow: hidden;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  font-weight: 400;
  line-height: 18px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.batch-tag-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid var(--td-component-stroke);
}

.batch-tag-selected-count {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
}

.batch-tag-footer-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
</style>
