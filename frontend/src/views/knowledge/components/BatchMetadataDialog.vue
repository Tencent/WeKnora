<template>
  <t-dialog :visible="visible" :footer="false" width="560px" dialog-class-name="batch-metadata-dialog"
    :close-on-overlay-click="false" destroy-on-close @close="handleClose">
    <template #header>
      <div class="batch-metadata-heading">
        <div class="batch-metadata-heading-row">
          <t-icon name="edit" size="16px" class="batch-metadata-heading-icon" aria-hidden="true" />
          <span class="batch-metadata-title">{{ $t('knowledgeBase.batchMetadataDialogHeading') }}</span>
        </div>
        <p class="batch-metadata-subtitle">
          {{ $t('knowledgeBase.batchMetadataSubtitle', { count }) }}
        </p>
      </div>
    </template>

    <div class="batch-metadata-body">
      <p class="batch-metadata-hint">{{ $t('knowledgeBase.batchMetadataHint') }}</p>

      <div v-if="rows.length" class="batch-metadata-rows">
        <div v-for="row in rows" :key="row.id" class="batch-metadata-row">
          <t-input v-model="row.key" class="metadata-key-input" :maxlength="64"
            :placeholder="$t('knowledgeBase.metadataKeyPlaceholder')" />
          <t-select v-model="row.type" class="metadata-type-input" :options="metadataTypeOptions" />
          <t-select v-if="row.type === 'boolean'" v-model="row.value" class="metadata-value-input"
            :options="booleanOptions" />
          <t-input v-else-if="row.type !== 'null'" v-model="row.value" class="metadata-value-input"
            :placeholder="$t('knowledgeBase.metadataValuePlaceholder')" />
          <div v-else class="metadata-null-value">null</div>
          <t-button class="metadata-remove-btn" size="small" variant="text" shape="square"
            :aria-label="$t('common.delete')" @click="removeRow(row.id)">
            <template #icon><t-icon name="delete" size="15px" /></template>
          </t-button>
        </div>
      </div>
      <p v-else class="batch-metadata-empty">{{ $t('knowledgeBase.batchMetadataEmpty') }}</p>

      <button v-if="rows.length < maxMetadataFields" type="button" class="metadata-add-row" @click="addRow">
        <t-icon name="add" size="15px" />
        <span>{{ $t('knowledgeBase.addMetadataField') }}</span>
      </button>
    </div>

    <div class="batch-metadata-footer">
      <span class="batch-metadata-count">{{ rows.length }}/{{ maxMetadataFields }}</span>
      <div class="batch-metadata-footer-right">
        <t-button variant="outline" size="small" :disabled="confirmLoading" @click="handleClose">
          {{ $t('common.cancel') }}
        </t-button>
        <t-button theme="primary" size="small" :loading="confirmLoading" @click="handleConfirm">
          {{ $t('common.save') }}
        </t-button>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';

type MetadataValueType = 'text' | 'number' | 'boolean' | 'null';

interface MetadataDraftRow {
  id: number;
  key: string;
  value: string;
  type: MetadataValueType;
}

const maxMetadataFields = 20;
let rowSeed = 0;

const props = defineProps<{
  visible: boolean;
  count: number;
  confirmLoading?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void;
  (e: 'confirm', metadata: Record<string, unknown>): void;
}>();

const { t } = useI18n();
const rows = ref<MetadataDraftRow[]>([]);

const metadataTypeOptions = computed(() => [
  { label: t('knowledgeBase.metadataTypeText'), value: 'text' },
  { label: t('knowledgeBase.metadataTypeNumber'), value: 'number' },
  { label: t('knowledgeBase.metadataTypeBoolean'), value: 'boolean' },
  { label: t('knowledgeBase.metadataTypeNull'), value: 'null' },
]);
const booleanOptions = [
  { label: 'true', value: 'true' },
  { label: 'false', value: 'false' },
];

watch(() => props.visible, (visible) => {
  if (visible) rows.value = [];
});

function addRow() {
  if (rows.value.length >= maxMetadataFields) return;
  rows.value.push({ id: ++rowSeed, key: '', value: '', type: 'text' });
}

function removeRow(id: number) {
  rows.value = rows.value.filter((row) => row.id !== id);
}

function parseValue(row: MetadataDraftRow): unknown {
  if (row.type === 'null') return null;
  if (row.type === 'boolean') return row.value === 'true';
  if (row.type === 'number') {
    const value = Number(row.value);
    if (!row.value.trim() || !Number.isFinite(value)) {
      throw new Error(t('knowledgeBase.metadataNumberRequired', { key: row.key }));
    }
    return value;
  }
  return row.value;
}

function handleConfirm() {
  if (props.confirmLoading) return;
  try {
    const metadata: Record<string, unknown> = {};
    for (const row of rows.value) {
      const key = row.key.trim();
      if (!key) throw new Error(t('knowledgeBase.metadataKeyRequired'));
      if (Object.prototype.hasOwnProperty.call(metadata, key)) {
        throw new Error(t('knowledgeBase.metadataKeyDuplicate', { key }));
      }
      metadata[key] = parseValue(row);
    }
    emit('confirm', metadata);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.saveFailed'));
  }
}

function handleClose() {
  if (props.confirmLoading) return;
  emit('update:visible', false);
}
</script>

<style>
.batch-metadata-dialog {
  overflow: hidden;
  padding: 0;
  border-radius: 4px;
}

.batch-metadata-dialog .t-dialog__header {
  min-height: auto;
  padding: 20px 20px 0;
}

.batch-metadata-dialog .t-dialog__body {
  padding: 0 20px 20px;
}

.batch-metadata-dialog .t-dialog__close {
  top: 16px;
  right: 16px;
  width: 28px;
  height: 28px;
  border-radius: 4px;
  color: var(--td-text-color-secondary);
}

@media (max-width: 600px) {
  .batch-metadata-dialog {
    width: calc(100vw - 24px) !important;
  }
}
</style>
<style scoped>
.batch-metadata-heading {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  padding-right: 28px;
}

.batch-metadata-heading-row,
.batch-metadata-footer,
.batch-metadata-footer-right {
  display: flex;
  align-items: center;
}

.batch-metadata-heading-row {
  gap: 8px;
}

.batch-metadata-heading-icon {
  color: var(--td-text-color-secondary);
}

.batch-metadata-title {
  color: var(--td-text-color-primary);
  font-size: 15px;
  font-weight: 600;
  line-height: 22px;
}

.batch-metadata-subtitle,
.batch-metadata-hint {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 18px;
}

.batch-metadata-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.batch-metadata-rows {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.batch-metadata-row {
  display: grid;
  grid-template-columns: minmax(110px, 1fr) 105px minmax(110px, 1fr) 28px;
  align-items: center;
  gap: 6px;
}

.metadata-remove-btn {
  width: 28px;
  min-width: 28px;
  height: 28px;
  padding: 0;
  color: var(--td-text-color-secondary);
}

.metadata-remove-btn:hover {
  color: var(--td-error-color);
}

.metadata-null-value {
  height: 32px;
  padding: 0 8px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 32px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 3px;
  box-sizing: border-box;
}

.batch-metadata-empty {
  margin: 0;
  padding: 16px;
  color: var(--td-text-color-placeholder);
  text-align: center;
  border: 1px dashed var(--td-component-stroke);
  border-radius: 4px;
}

.metadata-add-row {
  display: inline-flex;
  align-items: center;
  align-self: flex-start;
  gap: 4px;
  padding: 0;
  color: var(--td-brand-color);
  font-size: 12px;
  background: transparent;
  border: 0;
  cursor: pointer;
}

.batch-metadata-footer {
  justify-content: space-between;
  gap: 12px;
  margin-top: 18px;
}

.batch-metadata-count {
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.batch-metadata-footer-right {
  gap: 8px;
}

@media (max-width: 520px) {
  .batch-metadata-row {
    grid-template-columns: 1fr 1fr 28px;
  }

  .metadata-key-input {
    grid-column: 1 / 3;
  }
}
</style>
