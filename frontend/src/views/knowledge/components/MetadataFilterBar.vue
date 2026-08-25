<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';

import type {
  MetadataCondition,
  MetadataDefinition,
  MetadataOperator,
} from '@/api/knowledge-base';

const props = defineProps<{
  definitions: MetadataDefinition[];
  modelValue: MetadataCondition[];
}>();
const emit = defineEmits<{
  (event: 'update:modelValue', value: MetadataCondition[]): void;
  (event: 'apply', value: MetadataCondition[]): void;
}>();
const { t } = useI18n();

interface FilterDraft {
  metadata_definition_id: string;
  operator: MetadataOperator;
  input: unknown;
}

const visible = ref(false);
const drafts = ref<FilterDraft[]>([]);
const filterableDefinitions = computed(() => props.definitions.filter(definition => definition.filterable));
const definitionMap = computed(() => new Map(props.definitions.map(definition => [definition.id, definition])));
const availableDefinitions = computed(() => {
  const used = new Set(drafts.value.map(draft => draft.metadata_definition_id));
  return filterableDefinitions.value
    .filter(definition => !used.has(definition.id))
    .map(definition => ({ label: definition.name, value: definition.id }));
});

const defaultOperator = (definition: MetadataDefinition): MetadataOperator => {
  switch (definition.value_type) {
    case 'text': return 'contains';
    case 'single_select': return 'in';
    case 'multi_select': return 'contains_any';
    case 'number': return 'eq';
    case 'date': return 'on';
    case 'boolean': return 'eq';
  }
};

const emptyInput = (definition: MetadataDefinition, operator: MetadataOperator): unknown => {
  if (operator === 'is_empty' || operator === 'is_not_empty') return undefined;
  if (operator === 'between') return [undefined, undefined];
  if (definition.value_type === 'multi_select') return [];
  return undefined;
};

const operatorsFor = (definition?: MetadataDefinition) => {
  if (!definition) return [];
  const values: MetadataOperator[] = (() => {
    switch (definition.value_type) {
      case 'text': return ['equals', 'contains'];
      case 'single_select': return ['in'];
      case 'multi_select': return ['contains_any', 'contains_all'];
      case 'number': return ['eq', 'gt', 'gte', 'lt', 'lte', 'between'];
      case 'date': return ['on', 'before', 'after', 'between'];
      case 'boolean': return ['eq'];
    }
  })();
  return [...values, 'is_empty', 'is_not_empty'].map(value => ({
    value,
    label: t(`metadata.operators.${value}`),
  }));
};

const addDefinition = (definitionID: string | number) => {
  const id = String(definitionID);
  const definition = definitionMap.value.get(id);
  if (!definition) return;
  const operator = defaultOperator(definition);
  drafts.value.push({ metadata_definition_id: id, operator, input: emptyInput(definition, operator) });
};

const changeOperator = (draft: FilterDraft, operator: string | number) => {
  draft.operator = String(operator) as MetadataOperator;
  const definition = definitionMap.value.get(draft.metadata_definition_id);
  if (definition) draft.input = emptyInput(definition, draft.operator);
};

const removeDraft = (index: number) => {
  drafts.value.splice(index, 1);
};

const toCondition = (draft: FilterDraft): MetadataCondition => {
  if (draft.operator === 'is_empty' || draft.operator === 'is_not_empty') {
    return { metadata_definition_id: draft.metadata_definition_id, operator: draft.operator, values: [] };
  }
  const values = Array.isArray(draft.input) ? draft.input : [draft.input];
  return { metadata_definition_id: draft.metadata_definition_id, operator: draft.operator, values };
};

const hasUsableValue = (draft: FilterDraft) => {
  if (draft.operator === 'is_empty' || draft.operator === 'is_not_empty') return true;
  if (Array.isArray(draft.input)) {
    return draft.input.length > 0 && draft.input.every(value => value !== undefined && value !== null && value !== '');
  }
  return draft.input !== undefined && draft.input !== null && draft.input !== '';
};

const apply = () => {
  const conditions = drafts.value.filter(hasUsableValue).map(toCondition);
  emit('update:modelValue', conditions);
  emit('apply', conditions);
  visible.value = false;
};

const clear = () => {
  drafts.value = [];
  emit('update:modelValue', []);
  emit('apply', []);
  visible.value = false;
};

watch(
  () => props.modelValue,
  conditions => {
    drafts.value = conditions.map(condition => ({
      metadata_definition_id: condition.metadata_definition_id,
      operator: condition.operator,
      input: condition.values.length > 1 ? [...condition.values] : condition.values[0],
    }));
  },
  { immediate: true, deep: true },
);
</script>

<template>
  <t-popup v-if="filterableDefinitions.length" v-model="visible" trigger="click" placement="bottom-left" destroy-on-close>
    <t-button variant="outline" theme="default" class="metadata-filter-trigger">
      <template #icon><t-icon name="filter" /></template>
      {{ t('metadata.filter') }}
      <span v-if="modelValue.length" class="filter-count">{{ modelValue.length }}</span>
    </t-button>
    <template #content>
      <div class="metadata-filter-popover">
        <header>
          <strong>{{ t('metadata.filterTitle') }}</strong>
          <span>{{ t('metadata.filterDescription') }}</span>
        </header>

        <div v-if="drafts.length" class="condition-list">
          <div v-for="(draft, index) in drafts" :key="draft.metadata_definition_id" class="condition-row">
            <span class="condition-name">{{ definitionMap.get(draft.metadata_definition_id)?.name }}</span>
            <t-select :value="draft.operator" :options="operatorsFor(definitionMap.get(draft.metadata_definition_id))" @change="(value: string | number) => changeOperator(draft, value)" />

            <template v-if="draft.operator !== 'is_empty' && draft.operator !== 'is_not_empty'">
              <t-select
                v-if="definitionMap.get(draft.metadata_definition_id)?.value_type === 'single_select'"
                v-model="draft.input"
                :options="definitionMap.get(draft.metadata_definition_id)?.options.filter(option => option.status === 'active').map(option => ({ label: option.label, value: option.id }))"
              />
              <t-select
                v-else-if="definitionMap.get(draft.metadata_definition_id)?.value_type === 'multi_select'"
                v-model="draft.input"
                multiple
                :options="definitionMap.get(draft.metadata_definition_id)?.options.filter(option => option.status === 'active').map(option => ({ label: option.label, value: option.id }))"
              />
              <div v-else-if="definitionMap.get(draft.metadata_definition_id)?.value_type === 'number' && draft.operator === 'between'" class="range-inputs">
                <t-input-number v-model="(draft.input as any[])[0]" theme="normal" />
                <span>–</span>
                <t-input-number v-model="(draft.input as any[])[1]" theme="normal" />
              </div>
              <t-input-number v-else-if="definitionMap.get(draft.metadata_definition_id)?.value_type === 'number'" v-model="draft.input as number" theme="normal" />
              <t-date-range-picker v-else-if="definitionMap.get(draft.metadata_definition_id)?.value_type === 'date' && draft.operator === 'between'" v-model="draft.input as any" />
              <t-date-picker v-else-if="definitionMap.get(draft.metadata_definition_id)?.value_type === 'date'" v-model="draft.input as string" />
              <t-select
                v-else-if="definitionMap.get(draft.metadata_definition_id)?.value_type === 'boolean'"
                v-model="draft.input"
                :options="[{ label: t('metadata.trueValue'), value: true }, { label: t('metadata.falseValue'), value: false }]"
              />
              <t-input v-else v-model="draft.input as string" />
            </template>
            <span v-else class="no-value">{{ t('metadata.noValueNeeded') }}</span>

            <t-button shape="square" variant="text" theme="danger" @click="removeDraft(index)"><t-icon name="close" /></t-button>
          </div>
        </div>
        <div v-else class="condition-empty">{{ t('metadata.filterEmpty') }}</div>

        <t-select
          v-if="availableDefinitions.length"
          :value="undefined"
          :options="availableDefinitions"
          :placeholder="t('metadata.addCondition')"
          @change="addDefinition"
        />

        <footer>
          <t-button variant="text" :disabled="!drafts.length && !modelValue.length" @click="clear">{{ t('common.clear') }}</t-button>
          <t-button theme="primary" @click="apply">{{ t('metadata.applyFilter') }}</t-button>
        </footer>
      </div>
    </template>
  </t-popup>
</template>

<style scoped lang="less">
.metadata-filter-trigger { min-width: 112px; }
.filter-count { min-width: 18px; height: 18px; display: inline-flex; align-items: center; justify-content: center; border-radius: 9px; margin-left: 4px; color: var(--td-brand-color); background: var(--td-brand-color-light); font-size: 11px; font-weight: 700; }
.metadata-filter-popover { width: min(720px, calc(100vw - 32px)); padding: 16px; border: 1px solid var(--td-component-stroke); border-radius: 8px; background: var(--td-bg-color-container); box-shadow: var(--td-shadow-2); }
.metadata-filter-popover header { display: flex; flex-direction: column; gap: 4px; margin-bottom: 14px; }
.metadata-filter-popover header strong { font-size: 14px; }
.metadata-filter-popover header span { color: var(--td-text-color-placeholder); font-size: 12px; }
.condition-list { display: flex; flex-direction: column; gap: 8px; margin-bottom: 10px; }
.condition-row { display: grid; grid-template-columns: minmax(110px, 0.8fr) minmax(130px, 0.9fr) minmax(180px, 1.3fr) 32px; align-items: center; gap: 8px; }
.condition-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--td-text-color-secondary); font-size: 13px; font-weight: 600; }
.range-inputs { display: grid; grid-template-columns: 1fr 12px 1fr; align-items: center; gap: 4px; }
.range-inputs > span { text-align: center; color: var(--td-text-color-placeholder); }
.no-value { color: var(--td-text-color-placeholder); font-size: 12px; }
.condition-empty { display: flex; align-items: center; justify-content: center; min-height: 72px; margin-bottom: 10px; border: 1px dashed var(--td-component-stroke); border-radius: 6px; color: var(--td-text-color-placeholder); font-size: 12px; }
.metadata-filter-popover footer { display: flex; align-items: center; justify-content: flex-end; gap: 8px; margin-top: 14px; padding-top: 12px; border-top: 1px solid var(--td-component-stroke); }
@media (max-width: 720px) { .condition-row { grid-template-columns: 1fr 32px; } .condition-row > :nth-child(2), .condition-row > :nth-child(3) { grid-column: 1 / -1; } }
</style>
