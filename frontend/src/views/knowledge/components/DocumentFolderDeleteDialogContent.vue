<template>
  <div class="folder-delete-dialog-content">
    <div class="folder-delete-impact">
      <span class="folder-delete-impact__icon" aria-hidden="true">
        <t-icon name="folder-open" />
      </span>
      <div>
        <strong :title="folderName">{{ folderName }}</strong>
        <span>
          {{ t('knowledgeBase.deleteFolderImpactSummary', {
            folders: impact.folder_count,
            documents: impact.document_count,
          }) }}
        </span>
      </div>
    </div>

    <div
      class="folder-delete-options"
      role="radiogroup"
      :aria-label="t('knowledgeBase.deleteFolderChooseMode')"
    >
      <label
        class="folder-delete-option"
        :class="{
          selected: modelValue === 'keep_documents',
          disabled: keepDocumentsDisabled,
        }"
      >
        <input
          type="radio"
          name="document-folder-delete-mode"
          value="keep_documents"
          :checked="modelValue === 'keep_documents'"
          :disabled="keepDocumentsDisabled"
          @change="selectMode('keep_documents')"
        >
        <span class="folder-delete-option__content">
          <strong>{{ t('knowledgeBase.deleteFolderKeepDocuments') }}</strong>
          <span>
            {{ t('knowledgeBase.deleteFolderKeepDocumentsDescription', {
              count: impact.document_count,
            }) }}
          </span>
          <small>{{ t('knowledgeBase.deleteFolderStructureLost') }}</small>
        </span>
      </label>

      <label
        class="folder-delete-option folder-delete-option--danger"
        :class="{ selected: modelValue === 'delete_all' }"
      >
        <input
          type="radio"
          name="document-folder-delete-mode"
          value="delete_all"
          :checked="modelValue === 'delete_all'"
          @change="selectMode('delete_all')"
        >
        <span class="folder-delete-option__content">
          <strong>{{ t('knowledgeBase.deleteFolderDeleteAll') }}</strong>
          <span>{{ t('knowledgeBase.deleteFolderDeleteAllDescription') }}</span>
          <small>{{ t('knowledgeBase.deleteFolderIrreversible') }}</small>
        </span>
      </label>
    </div>

    <div v-if="keepDocumentsDisabled" class="folder-delete-processing" role="status">
      <t-icon name="time" aria-hidden="true" />
      <span>
        {{ t('knowledgeBase.deleteFolderDocumentsProcessing', {
          count: impact.active_document_count,
        }) }}
      </span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import type {
  DocumentFolderDeleteImpact,
  DocumentFolderDeleteMode,
} from '@/api/knowledge-base';

const props = defineProps<{
  folderName: string;
  impact: DocumentFolderDeleteImpact;
  modelValue: DocumentFolderDeleteMode | '';
  keepDocumentsDisabled: boolean;
}>();

const emit = defineEmits<{
  (event: 'update:modelValue', value: DocumentFolderDeleteMode): void;
}>();

const { t } = useI18n();

function selectMode(mode: DocumentFolderDeleteMode) {
  if (mode === 'keep_documents' && props.keepDocumentsDisabled) return;
  emit('update:modelValue', mode);
}
</script>

<style scoped lang="less">
.folder-delete-dialog-content {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding-top: 2px;
}

.folder-delete-impact {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  border-radius: 10px;
  background: var(--td-bg-color-container-hover);

  &__icon {
    display: grid;
    flex: 0 0 40px;
    width: 40px;
    height: 40px;
    place-items: center;
    border-radius: 10px;
    color: var(--td-brand-color);
    background: var(--td-brand-color-light);
    font-size: 22px;
  }

  > div {
    min-width: 0;
  }

  strong,
  span {
    display: block;
  }

  strong {
    overflow: hidden;
    margin-bottom: 3px;
    color: var(--td-text-color-primary);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  span {
    color: var(--td-text-color-secondary);
    font-size: 13px;
  }
}

.folder-delete-options {
  display: grid;
  gap: 10px;
}

.folder-delete-option {
  display: flex;
  gap: 10px;
  padding: 13px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease, box-shadow 0.15s ease;

  &:hover {
    border-color: var(--td-brand-color);
  }

  &.selected {
    border-color: var(--td-brand-color);
    background: var(--td-brand-color-light);
    box-shadow: 0 0 0 1px var(--td-brand-color-focus);
  }

  &.disabled {
    cursor: not-allowed;
    opacity: 0.56;
  }

  input {
    flex: 0 0 auto;
    margin-top: 3px;
    accent-color: var(--td-brand-color);
  }

  &__content {
    display: flex;
    min-width: 0;
    flex-direction: column;
    gap: 4px;
  }

  strong {
    color: var(--td-text-color-primary);
    font-size: 14px;
  }

  span,
  small {
    color: var(--td-text-color-secondary);
    font-size: 13px;
    line-height: 1.55;
  }

  small {
    color: var(--td-warning-color);
  }

  &--danger {
    &.selected,
    &:hover {
      border-color: var(--td-error-color);
    }

    &.selected {
      background: var(--td-error-color-1);
      box-shadow: 0 0 0 1px var(--td-error-color-focus);
    }

    input {
      accent-color: var(--td-error-color);
    }

    small {
      color: var(--td-error-color);
    }
  }
}

.folder-delete-processing {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 10px 12px;
  border-radius: 8px;
  color: var(--td-warning-color-8);
  background: var(--td-warning-color-1);
  font-size: 13px;
  line-height: 1.5;

  :deep(.t-icon) {
    flex: 0 0 auto;
    margin-top: 2px;
  }
}
</style>
