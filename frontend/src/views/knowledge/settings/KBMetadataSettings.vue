<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue';
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';

import {
  archiveMetadataDefinition,
  configureMetadataAutoRule,
  createMetadataDefinition,
  deleteMetadataAutoRule,
  getMetadataSchema,
  updateMetadataDefinition,
  type ConfigureMetadataDefinitionInput,
  type MetadataDefinition,
  type MetadataOption,
  type MetadataValueType,
} from '@/api/knowledge-base';

const props = defineProps<{ kbId: string }>();
const { t } = useI18n();

const loading = ref(false);
const saving = ref(false);
const definitions = ref<MetadataDefinition[]>([]);
const drawerVisible = ref(false);
const editing = ref<MetadataDefinition | null>(null);

interface DefinitionForm {
  name: string;
  desc: string;
  value_type: MetadataValueType;
  required: boolean;
  filterable: boolean;
  sort_order: number;
  options: Array<Partial<MetadataOption> & { label: string; sort_order: number }>;
  auto_rule_enabled: boolean;
  auto_rule_strategy: 'source_mapping' | 'llm_extract';
  source_key: string;
  instruction: string;
  model_id: string;
}

const blankForm = (): DefinitionForm => ({
  name: '',
  desc: '',
  value_type: 'text',
  required: false,
  filterable: true,
  sort_order: definitions.value.length,
  options: [],
  auto_rule_enabled: false,
  auto_rule_strategy: 'source_mapping',
  source_key: '',
  instruction: '',
  model_id: '',
});

const form = reactive<DefinitionForm>(blankForm());
const isSelectType = computed(() => form.value_type === 'single_select' || form.value_type === 'multi_select');
const typeOptions = computed(() => [
  { value: 'text', label: t('metadata.types.text') },
  { value: 'single_select', label: t('metadata.types.singleSelect') },
  { value: 'multi_select', label: t('metadata.types.multiSelect') },
  { value: 'number', label: t('metadata.types.number') },
  { value: 'date', label: t('metadata.types.date') },
  { value: 'boolean', label: t('metadata.types.boolean') },
]);

const resetForm = (definition?: MetadataDefinition) => {
  const next = blankForm();
  if (definition) {
    next.name = definition.name;
    next.desc = definition.desc || '';
    next.value_type = definition.value_type;
    next.required = definition.required;
    next.filterable = definition.filterable;
    next.sort_order = definition.sort_order;
    next.options = (definition.options || [])
      .filter(option => option.status === 'active')
      .map(option => ({ ...option }));
    if (definition.auto_rule) {
      next.auto_rule_enabled = true;
      next.auto_rule_strategy = definition.auto_rule.strategy;
      next.source_key = String(definition.auto_rule.config?.source_key || '');
      next.instruction = String(definition.auto_rule.config?.instruction || '');
      next.model_id = String(definition.auto_rule.config?.model_id || '');
    }
  }
  Object.assign(form, next);
};

const loadSchema = async () => {
  if (!props.kbId) return;
  loading.value = true;
  try {
    const response: any = await getMetadataSchema(props.kbId);
    definitions.value = response?.data?.definitions || [];
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('metadata.messages.loadFailed'));
  } finally {
    loading.value = false;
  }
};

const openCreate = () => {
  editing.value = null;
  resetForm();
  drawerVisible.value = true;
};

const openEdit = (definition: MetadataDefinition) => {
  editing.value = definition;
  resetForm(definition);
  drawerVisible.value = true;
};

const addOption = () => {
  form.options.push({ label: '', sort_order: form.options.length, status: 'active' });
};

const removeOption = (index: number) => {
  form.options.splice(index, 1);
  form.options.forEach((option, optionIndex) => { option.sort_order = optionIndex; });
};

const moveOption = (index: number, direction: -1 | 1) => {
  const target = index + direction;
  if (target < 0 || target >= form.options.length) return;
  [form.options[index], form.options[target]] = [form.options[target], form.options[index]];
  form.options.forEach((option, optionIndex) => { option.sort_order = optionIndex; });
};

const validate = () => {
  if (!form.name.trim()) {
    MessagePlugin.warning(t('metadata.messages.nameRequired'));
    return false;
  }
  if (isSelectType.value) {
    const labels = form.options.map(option => option.label.trim()).filter(Boolean);
    if (labels.length !== form.options.length || labels.length === 0) {
      MessagePlugin.warning(t('metadata.messages.optionRequired'));
      return false;
    }
    if (new Set(labels.map(label => label.toLocaleLowerCase())).size !== labels.length) {
      MessagePlugin.warning(t('metadata.messages.optionDuplicate'));
      return false;
    }
  }
  if (form.auto_rule_enabled && form.auto_rule_strategy === 'source_mapping' && !form.source_key.trim()) {
    MessagePlugin.warning(t('metadata.messages.sourceKeyRequired'));
    return false;
  }
  if (form.auto_rule_enabled && form.auto_rule_strategy === 'llm_extract' && !form.instruction.trim()) {
    MessagePlugin.warning(t('metadata.messages.instructionRequired'));
    return false;
  }
  return true;
};

const definitionPayload = (): ConfigureMetadataDefinitionInput => ({
  name: form.name.trim(),
  desc: form.desc.trim(),
  value_type: form.value_type,
  required: form.required,
  filterable: form.filterable,
  sort_order: form.sort_order,
  options: isSelectType.value
    ? form.options.map((option, index) => ({
        ...(option.id ? { id: option.id } : {}),
        label: option.label.trim(),
        status: option.status || 'active',
        sort_order: index,
      }))
    : [],
});

const save = async () => {
  if (!validate()) return;
  saving.value = true;
  try {
    const response: any = editing.value
      ? await updateMetadataDefinition(props.kbId, editing.value.id, definitionPayload())
      : await createMetadataDefinition(props.kbId, definitionPayload());
    const definition = response?.data as MetadataDefinition;
    if (form.auto_rule_enabled) {
      const config = form.auto_rule_strategy === 'source_mapping'
        ? { source_key: form.source_key.trim() }
        : {
            instruction: form.instruction.trim(),
            ...(form.model_id.trim() ? { model_id: form.model_id.trim() } : {}),
          };
      await configureMetadataAutoRule(props.kbId, definition.id, {
        strategy: form.auto_rule_strategy,
        config,
      });
    } else if (definition.auto_rule || editing.value?.auto_rule) {
      await deleteMetadataAutoRule(props.kbId, definition.id);
    }
    drawerVisible.value = false;
    MessagePlugin.success(t('metadata.messages.saved'));
    await loadSchema();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('metadata.messages.saveFailed'));
  } finally {
    saving.value = false;
  }
};

const archiveDefinition = (definition: MetadataDefinition) => {
  const dialog = DialogPlugin.confirm({
    header: t('metadata.archiveTitle'),
    body: t('metadata.archiveDescription', { name: definition.name }),
    confirmBtn: { content: t('metadata.archiveAction'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      try {
        await archiveMetadataDefinition(props.kbId, definition.id);
        MessagePlugin.success(t('metadata.messages.archived'));
        await loadSchema();
        dialog.hide();
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('metadata.messages.archiveFailed'));
      }
    },
  });
};

const moveDefinition = async (index: number, direction: -1 | 1) => {
  const targetIndex = index + direction;
  if (targetIndex < 0 || targetIndex >= definitions.value.length) return;
  const current = definitions.value[index];
  const target = definitions.value[targetIndex];
  const currentOrder = current.sort_order;
  try {
    await Promise.all([
      updateMetadataDefinition(props.kbId, current.id, {
        name: current.name, desc: current.desc, value_type: current.value_type,
        required: current.required, filterable: current.filterable, sort_order: target.sort_order,
        options: current.options,
      }),
      updateMetadataDefinition(props.kbId, target.id, {
        name: target.name, desc: target.desc, value_type: target.value_type,
        required: target.required, filterable: target.filterable, sort_order: currentOrder,
        options: target.options,
      }),
    ]);
    await loadSchema();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('metadata.messages.sortFailed'));
  }
};

watch(() => props.kbId, loadSchema);
watch(isSelectType, (enabled) => {
  if (enabled && form.options.length === 0) addOption();
});
onMounted(loadSchema);
</script>

<template>
  <section class="metadata-settings">
    <header class="metadata-header">
      <div>
        <h2>{{ t('metadata.settingsTitle') }}</h2>
        <p>{{ t('metadata.settingsDescription') }}</p>
      </div>
      <t-button theme="primary" @click="openCreate">
        <template #icon><t-icon name="add" /></template>
        {{ t('metadata.createDefinition') }}
      </t-button>
    </header>

    <div class="definition-table" :class="{ loading }">
      <div class="definition-row definition-head">
        <span>{{ t('metadata.name') }}</span>
        <span>{{ t('metadata.type') }}</span>
        <span>{{ t('metadata.governance') }}</span>
        <span>{{ t('metadata.automaticRule') }}</span>
        <span></span>
      </div>
      <div v-if="loading" class="definition-empty"><t-loading size="small" /></div>
      <div v-else-if="definitions.length === 0" class="definition-empty">
        <t-icon name="filter" size="24px" />
        <span>{{ t('metadata.emptyDefinitions') }}</span>
      </div>
      <div v-for="(definition, index) in definitions" :key="definition.id" class="definition-row">
        <div class="definition-name">
          <strong>{{ definition.name }}</strong>
          <span>{{ definition.desc || t('metadata.noDescription') }}</span>
        </div>
        <div class="definition-type">
          <t-tag size="small" variant="light-outline">{{ t(`metadata.types.${definition.value_type === 'single_select' ? 'singleSelect' : definition.value_type === 'multi_select' ? 'multiSelect' : definition.value_type}`) }}</t-tag>
          <t-tooltip v-if="definition.type_locked" :content="t('metadata.typeLocked')">
            <t-icon name="lock-on" class="locked-icon" />
          </t-tooltip>
        </div>
        <div class="definition-flags">
          <span v-if="definition.required"><t-icon name="check-circle" />{{ t('metadata.required') }}</span>
          <span v-if="definition.filterable"><t-icon name="filter" />{{ t('metadata.filterable') }}</span>
          <span v-if="!definition.required && !definition.filterable">--</span>
        </div>
        <div class="rule-state">
          <span v-if="definition.auto_rule" class="rule-active">
            {{ t(`metadata.ruleStrategies.${definition.auto_rule.strategy}`) }}
            <small>r{{ definition.auto_rule.revision }}</small>
          </span>
          <span v-else class="muted">{{ t('metadata.manualOnly') }}</span>
        </div>
        <div class="definition-actions">
          <t-tooltip :content="t('metadata.moveUp')"><t-button shape="square" variant="text" size="small" :disabled="index === 0" @click="moveDefinition(index, -1)"><t-icon name="chevron-up" /></t-button></t-tooltip>
          <t-tooltip :content="t('metadata.moveDown')"><t-button shape="square" variant="text" size="small" :disabled="index === definitions.length - 1" @click="moveDefinition(index, 1)"><t-icon name="chevron-down" /></t-button></t-tooltip>
          <t-tooltip :content="t('common.edit')"><t-button shape="square" variant="text" size="small" @click="openEdit(definition)"><t-icon name="edit" /></t-button></t-tooltip>
          <t-tooltip :content="t('metadata.archiveAction')"><t-button shape="square" variant="text" theme="danger" size="small" @click="archiveDefinition(definition)"><t-icon name="delete" /></t-button></t-tooltip>
        </div>
      </div>
    </div>

    <t-drawer v-model:visible="drawerVisible" :header="editing ? t('metadata.editDefinition') : t('metadata.createDefinition')" size="520px" :footer="false">
      <div class="definition-form">
        <label>{{ t('metadata.name') }}<b>*</b></label>
        <t-input v-model="form.name" :maxlength="128" :placeholder="t('metadata.namePlaceholder')" />
        <label>{{ t('metadata.description') }}</label>
        <t-textarea v-model="form.desc" :maxlength="2000" :autosize="{ minRows: 2, maxRows: 5 }" />
        <label>{{ t('metadata.type') }}<b>*</b></label>
        <t-select v-model="form.value_type" :options="typeOptions" :disabled="!!editing?.type_locked" />
        <p v-if="editing?.type_locked" class="form-note"><t-icon name="lock-on" />{{ t('metadata.typeLocked') }}</p>

        <div class="toggle-row">
          <div><strong>{{ t('metadata.required') }}</strong><span>{{ t('metadata.requiredDescription') }}</span></div>
          <t-switch v-model="form.required" />
        </div>
        <div class="toggle-row">
          <div><strong>{{ t('metadata.filterable') }}</strong><span>{{ t('metadata.filterableDescription') }}</span></div>
          <t-switch v-model="form.filterable" />
        </div>

        <template v-if="isSelectType">
          <div class="option-heading"><label>{{ t('metadata.options') }}<b>*</b></label><t-button variant="text" size="small" @click="addOption"><template #icon><t-icon name="add" /></template>{{ t('metadata.addOption') }}</t-button></div>
          <div class="option-list">
            <div v-for="(option, index) in form.options" :key="option.id || index" class="option-row">
              <span class="option-grip">{{ index + 1 }}</span>
              <t-input v-model="option.label" :maxlength="128" />
              <t-button shape="square" variant="text" size="small" :disabled="index === 0" @click="moveOption(index, -1)"><t-icon name="chevron-up" /></t-button>
              <t-button shape="square" variant="text" size="small" :disabled="index === form.options.length - 1" @click="moveOption(index, 1)"><t-icon name="chevron-down" /></t-button>
              <t-button shape="square" variant="text" theme="danger" size="small" @click="removeOption(index)"><t-icon name="close" /></t-button>
            </div>
          </div>
        </template>

        <div class="rule-panel">
          <div class="toggle-row compact">
            <div><strong>{{ t('metadata.automaticRule') }}</strong><span>{{ t('metadata.automaticRuleDescription') }}</span></div>
            <t-switch v-model="form.auto_rule_enabled" />
          </div>
          <template v-if="form.auto_rule_enabled">
            <label>{{ t('metadata.ruleStrategy') }}</label>
            <t-radio-group v-model="form.auto_rule_strategy" variant="default-filled">
              <t-radio-button value="source_mapping">{{ t('metadata.ruleStrategies.source_mapping') }}</t-radio-button>
              <t-radio-button value="llm_extract">{{ t('metadata.ruleStrategies.llm_extract') }}</t-radio-button>
            </t-radio-group>
            <template v-if="form.auto_rule_strategy === 'source_mapping'">
              <label>{{ t('metadata.sourceKey') }}<b>*</b></label>
              <t-input v-model="form.source_key" :placeholder="t('metadata.sourceKeyPlaceholder')" />
            </template>
            <template v-else>
              <label>{{ t('metadata.instruction') }}<b>*</b></label>
              <t-textarea v-model="form.instruction" :autosize="{ minRows: 3, maxRows: 7 }" />
              <label>{{ t('metadata.modelId') }}</label>
              <t-input v-model="form.model_id" :placeholder="t('metadata.modelIdPlaceholder')" />
            </template>
          </template>
        </div>

        <div class="drawer-actions">
          <t-button variant="outline" @click="drawerVisible = false">{{ t('common.cancel') }}</t-button>
          <t-button theme="primary" :loading="saving" @click="save">{{ t('common.save') }}</t-button>
        </div>
      </div>
    </t-drawer>
  </section>
</template>

<style scoped lang="less">
.metadata-settings { padding: 28px 32px; color: var(--td-text-color-primary); }
.metadata-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 24px; margin-bottom: 22px; }
.metadata-header h2 { margin: 0 0 6px; font-size: 20px; letter-spacing: 0; }
.metadata-header p { margin: 0; max-width: 640px; color: var(--td-text-color-secondary); font-size: 13px; line-height: 1.6; }
.definition-table { border: 1px solid var(--td-component-stroke); border-radius: 8px; overflow: hidden; }
.definition-row { display: grid; grid-template-columns: minmax(170px, 1.5fr) 140px minmax(150px, 1fr) minmax(150px, 1fr) 170px; min-height: 64px; align-items: center; border-bottom: 1px solid var(--td-component-stroke); padding: 0 14px; gap: 12px; }
.definition-row:last-child { border-bottom: 0; }
.definition-head { min-height: 40px; color: var(--td-text-color-secondary); background: var(--td-bg-color-secondarycontainer); font-size: 12px; font-weight: 600; }
.definition-name { min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.definition-name strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 14px; }
.definition-name span, .muted { color: var(--td-text-color-placeholder); font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.definition-type, .definition-flags, .definition-actions { display: flex; align-items: center; gap: 5px; }
.definition-flags { flex-wrap: wrap; color: var(--td-text-color-secondary); font-size: 12px; }
.definition-flags span { display: inline-flex; align-items: center; gap: 3px; }
.definition-actions { justify-content: flex-end; }
.locked-icon { color: var(--td-text-color-placeholder); }
.rule-active { display: inline-flex; align-items: center; gap: 6px; color: var(--td-success-color); font-size: 12px; }
.rule-active small { color: var(--td-text-color-placeholder); }
.definition-empty { min-height: 180px; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 10px; color: var(--td-text-color-placeholder); font-size: 13px; }
.definition-form { display: flex; flex-direction: column; gap: 10px; padding: 0 4px 20px; }
.definition-form label { margin-top: 6px; color: var(--td-text-color-secondary); font-size: 13px; font-weight: 600; }
.definition-form label b { color: var(--td-error-color); margin-left: 3px; }
.form-note { display: flex; align-items: center; gap: 5px; margin: -3px 0 4px; color: var(--td-text-color-placeholder); font-size: 12px; }
.toggle-row { display: flex; justify-content: space-between; align-items: center; padding: 13px 0; border-bottom: 1px solid var(--td-component-stroke); }
.toggle-row > div { display: flex; flex-direction: column; gap: 4px; }
.toggle-row strong { font-size: 13px; }
.toggle-row span { color: var(--td-text-color-placeholder); font-size: 12px; }
.option-heading { display: flex; align-items: center; justify-content: space-between; margin-top: 4px; }
.option-list { display: flex; flex-direction: column; gap: 7px; }
.option-row { display: grid; grid-template-columns: 24px minmax(0, 1fr) 30px 30px 30px; gap: 4px; align-items: center; }
.option-grip { width: 22px; height: 22px; display: inline-flex; align-items: center; justify-content: center; border-radius: 4px; background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-placeholder); font-size: 11px; }
.rule-panel { margin-top: 10px; padding: 4px 14px 14px; border: 1px solid var(--td-component-stroke); border-radius: 8px; background: var(--td-bg-color-secondarycontainer); display: flex; flex-direction: column; gap: 9px; }
.toggle-row.compact { border-bottom: 0; }
.drawer-actions { position: sticky; bottom: 0; display: flex; justify-content: flex-end; gap: 10px; margin-top: 14px; padding: 14px 0 0; background: var(--td-bg-color-container); border-top: 1px solid var(--td-component-stroke); }
@media (max-width: 900px) { .definition-row { grid-template-columns: minmax(150px, 1fr) 120px 130px; } .definition-head > :nth-child(3), .definition-head > :nth-child(4), .definition-row > :nth-child(3), .definition-row > :nth-child(4) { display: none; } }
</style>
