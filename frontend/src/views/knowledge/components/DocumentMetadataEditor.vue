<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';

import {
  changeDocumentMetadata,
  confirmDocumentMetadata,
  getDocumentMetadata,
  rerunDocumentMetadataAutoFill,
  type DocumentMetadata,
  type DocumentMetadataField,
} from '@/api/knowledge-base';
import MetadataStatus from './MetadataStatus.vue';

const props = defineProps<{
  visible: boolean;
  knowledgeId: string;
  knowledgeName?: string;
  canEdit: boolean;
}>();
const emit = defineEmits<{ (event: 'update:visible', value: boolean): void }>();
const { t } = useI18n();

const loading = ref(false);
const savingID = ref('');
const rerunning = ref(false);
const metadata = ref<DocumentMetadata | null>(null);
const drafts = reactive<Record<string, any>>({});
const policyDrafts = reactive<Record<string, boolean>>({});
const dirty = reactive<Record<string, boolean>>({});
const overwriteDialogVisible = ref(false);
const pendingField = ref<DocumentMetadataField | null>(null);
const pendingOverwrite = ref(false);

const pendingFields = computed(() => metadata.value?.values.filter(field => field.value?.review_status === 'pending' && field.definition.status !== 'archived') || []);
const isArchivedField = (field: DocumentMetadataField) => field.definition.status === 'archived';
const fieldEditable = (field: DocumentMetadataField) => props.canEdit && !isArchivedField(field);
const fieldOptions = (field: DocumentMetadataField) => {
  const selected = new Set(
    Array.isArray(drafts[field.definition.id])
      ? drafts[field.definition.id]
      : drafts[field.definition.id]
        ? [drafts[field.definition.id]]
        : [],
  );
  return (field.definition.options || [])
    .filter(option => option.status === 'active' || selected.has(option.id))
    .map(option => ({ label: option.label, value: option.id }));
};

const load = async () => {
  if (!props.visible || !props.knowledgeId) return;
  loading.value = true;
  try {
    const response: any = await getDocumentMetadata(props.knowledgeId);
    metadata.value = response?.data || null;
    for (const field of metadata.value?.values || []) {
      drafts[field.definition.id] = field.value?.value ?? null;
      policyDrafts[field.definition.id] = field.value?.allow_auto_overwrite ?? false;
      dirty[field.definition.id] = false;
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('metadata.messages.documentLoadFailed'));
  } finally {
    loading.value = false;
  }
};

const markDirty = (definitionID: string) => {
  dirty[definitionID] = true;
};

const requestSave = (field: DocumentMetadataField) => {
  if (!props.canEdit) return;
  pendingField.value = field;
  pendingOverwrite.value = false;
  overwriteDialogVisible.value = true;
};

const executeSave = async () => {
  const field = pendingField.value;
  if (!field) return;
  overwriteDialogVisible.value = false;
  savingID.value = field.definition.id;
  try {
    const change: {
      metadata_definition_id: string;
      value?: unknown;
      allow_auto_overwrite?: boolean;
      expected_version?: number;
    } = {
      metadata_definition_id: field.definition.id,
      value: drafts[field.definition.id],
      allow_auto_overwrite: pendingOverwrite.value,
      expected_version: field.value?.version || 0,
    };
    const response: any = await changeDocumentMetadata(props.knowledgeId, [change]);
    metadata.value = response?.data || metadata.value;
    MessagePlugin.success(t('metadata.messages.valueSaved'));
    await load();
  } catch (error: any) {
    if (error?.status === 409) {
      MessagePlugin.warning(t('metadata.messages.versionConflict'));
      await load();
    } else {
      MessagePlugin.error(error?.message || t('metadata.messages.valueSaveFailed'));
    }
  } finally {
    savingID.value = '';
    pendingField.value = null;
  }
};

const savePolicy = async (field: DocumentMetadataField, value: boolean) => {
  if (!field.value || !props.canEdit) return;
  savingID.value = field.definition.id;
  try {
    await changeDocumentMetadata(props.knowledgeId, [{
      metadata_definition_id: field.definition.id,
      allow_auto_overwrite: value,
      expected_version: field.value.version,
    }]);
    await load();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('metadata.messages.policySaveFailed'));
    await load();
  } finally {
    savingID.value = '';
  }
};

const clearField = (field: DocumentMetadataField) => {
  drafts[field.definition.id] = null;
  dirty[field.definition.id] = true;
  requestSave(field);
};

const confirmFields = async (definitionIDs: string[]) => {
  try {
    const response: any = await confirmDocumentMetadata(props.knowledgeId, definitionIDs);
    metadata.value = response?.data || metadata.value;
    MessagePlugin.success(t('metadata.messages.confirmed'));
    await load();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('metadata.messages.confirmFailed'));
  }
};

const rerunAutoFill = async () => {
  rerunning.value = true;
  try {
    await rerunDocumentMetadataAutoFill(props.knowledgeId);
    MessagePlugin.success(t('metadata.messages.autoFillQueued'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('metadata.messages.autoFillFailed'));
  } finally {
    rerunning.value = false;
  }
};

watch(() => [props.visible, props.knowledgeId], load, { immediate: true });
</script>

<template>
  <t-drawer
    :visible="visible"
    :header="knowledgeName || t('metadata.documentTitle')"
    size="560px"
    :footer="false"
    @update:visible="(value: boolean) => emit('update:visible', value)"
  >
    <div class="document-metadata">
      <div class="document-metadata-summary">
        <div>
          <strong>{{ t('metadata.documentFields') }}</strong>
          <span v-if="metadata?.incomplete_count">{{ t('metadata.incompleteCount', { count: metadata.incomplete_count }) }}</span>
          <span v-else>{{ t('metadata.allComplete') }}</span>
        </div>
        <div v-if="canEdit" class="summary-actions">
          <t-button variant="text" size="small" :loading="rerunning" @click="rerunAutoFill">
            <template #icon><t-icon name="refresh" /></template>
            {{ t('metadata.rerunAutoFill') }}
          </t-button>
          <t-button v-if="pendingFields.length" variant="outline" size="small" @click="confirmFields([])">
            <template #icon><t-icon name="check" /></template>
            {{ t('metadata.confirmAll') }}
          </t-button>
        </div>
      </div>

      <div v-if="loading" class="metadata-loading"><t-loading /></div>
      <div v-else-if="!metadata?.values.length" class="metadata-loading">{{ t('metadata.emptyDefinitions') }}</div>
      <div v-else class="metadata-field-list">
        <section v-for="field in metadata.values" :key="field.definition.id" class="metadata-field">
          <header>
            <div class="field-title">
              <strong>{{ field.definition.name }}<b v-if="field.definition.required">*</b></strong>
              <span v-if="field.definition.desc">{{ field.definition.desc }}</span>
            </div>
            <MetadataStatus :field="field" />
          </header>

          <t-input
            v-if="field.definition.value_type === 'text'"
            v-model="drafts[field.definition.id]"
            :disabled="!fieldEditable(field)"
            clearable
            @change="markDirty(field.definition.id)"
          />
          <t-select
            v-else-if="field.definition.value_type === 'single_select'"
            v-model="drafts[field.definition.id]"
            :disabled="!fieldEditable(field)"
            clearable
            :options="fieldOptions(field)"
            @change="markDirty(field.definition.id)"
          />
          <t-select
            v-else-if="field.definition.value_type === 'multi_select'"
            v-model="drafts[field.definition.id]"
            :disabled="!fieldEditable(field)"
            multiple
            clearable
            :options="fieldOptions(field)"
            @change="markDirty(field.definition.id)"
          />
          <t-input-number
            v-else-if="field.definition.value_type === 'number'"
            v-model="drafts[field.definition.id]"
            :disabled="!fieldEditable(field)"
            theme="normal"
            @change="markDirty(field.definition.id)"
          />
          <t-date-picker
            v-else-if="field.definition.value_type === 'date'"
            v-model="drafts[field.definition.id]"
            :disabled="!fieldEditable(field)"
            clearable
            @change="markDirty(field.definition.id)"
          />
          <t-radio-group
            v-else
            v-model="drafts[field.definition.id]"
            :disabled="!fieldEditable(field)"
            variant="default-filled"
            @change="markDirty(field.definition.id)"
          >
            <t-radio-button :value="true">{{ t('metadata.trueValue') }}</t-radio-button>
            <t-radio-button :value="false">{{ t('metadata.falseValue') }}</t-radio-button>
          </t-radio-group>

          <footer v-if="fieldEditable(field)">
            <div v-if="field.value" class="policy-toggle">
              <span>{{ t('metadata.allowAutoOverwrite') }}</span>
              <t-switch
                :value="policyDrafts[field.definition.id]"
                :loading="savingID === field.definition.id"
                @change="(value: boolean) => savePolicy(field, value)"
              />
            </div>
            <div class="field-actions">
              <t-button v-if="field.value?.review_status === 'pending'" variant="outline" size="small" @click="confirmFields([field.definition.id])">
                {{ t('metadata.confirmOne') }}
              </t-button>
              <t-button v-if="field.value" variant="text" size="small" @click="clearField(field)">{{ t('common.clear') }}</t-button>
              <t-button theme="primary" size="small" :loading="savingID === field.definition.id" :disabled="!dirty[field.definition.id]" @click="requestSave(field)">
                {{ t('common.save') }}
              </t-button>
            </div>
          </footer>
        </section>
      </div>
    </div>

    <t-dialog
      v-model:visible="overwriteDialogVisible"
      :header="t('metadata.overwritePromptTitle')"
      :confirm-btn="{ content: t('common.save'), loading: !!savingID }"
      :cancel-btn="t('common.cancel')"
      width="440px"
      @confirm="executeSave"
    >
      <div class="overwrite-prompt">
        <p>{{ t('metadata.overwritePromptDescription') }}</p>
        <div>
          <span>{{ t('metadata.allowAutoOverwrite') }}</span>
          <t-switch v-model="pendingOverwrite" />
        </div>
      </div>
    </t-dialog>
  </t-drawer>
</template>

<style scoped lang="less">
.document-metadata { padding: 0 4px 24px; }
.document-metadata-summary { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 0 0 16px; border-bottom: 1px solid var(--td-component-stroke); }
.document-metadata-summary > div { display: flex; flex-direction: column; gap: 4px; }
.document-metadata-summary strong { font-size: 14px; }
.document-metadata-summary span { color: var(--td-text-color-secondary); font-size: 12px; }
.summary-actions { display: flex; align-items: center; gap: 6px; }
.metadata-loading { min-height: 180px; display: flex; align-items: center; justify-content: center; color: var(--td-text-color-placeholder); }
.metadata-field-list { display: flex; flex-direction: column; }
.metadata-field { padding: 18px 0; border-bottom: 1px solid var(--td-component-stroke); }
.metadata-field > header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 12px; }
.field-title { min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.field-title strong { font-size: 13px; }
.field-title strong b { color: var(--td-error-color); margin-left: 3px; }
.field-title span { color: var(--td-text-color-placeholder); font-size: 12px; line-height: 1.4; }
.metadata-field > footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 12px; }
.policy-toggle, .field-actions { display: flex; align-items: center; gap: 8px; }
.policy-toggle span { color: var(--td-text-color-secondary); font-size: 12px; }
.overwrite-prompt p { margin: 0 0 18px; color: var(--td-text-color-secondary); line-height: 1.6; }
.overwrite-prompt > div { display: flex; align-items: center; justify-content: space-between; padding: 12px; border: 1px solid var(--td-component-stroke); border-radius: 6px; }
</style>
