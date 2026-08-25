<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';

import {
  getMetadataSchema,
  type KBMetadataFilter,
  type MetadataCondition,
  type MetadataDefinition,
} from '@/api/knowledge-base';
import MetadataFilterBar from '@/views/knowledge/components/MetadataFilterBar.vue';

interface SelectedKnowledgeBase {
  id: string;
  name: string;
}

const props = defineProps<{
  knowledgeBases: SelectedKnowledgeBase[];
  modelValue: KBMetadataFilter[];
}>();
const emit = defineEmits<{
  (event: 'update:modelValue', value: KBMetadataFilter[]): void;
}>();

const { t } = useI18n();
const visible = ref(false);
const loading = ref(false);
const definitionsByKnowledgeBase = ref<Record<string, MetadataDefinition[]>>({});

const activeCount = computed(() => props.modelValue.reduce((total, item) => total + item.conditions.length, 0));

const conditionsFor = (knowledgeBaseID: string) => (
  props.modelValue.find(item => item.knowledge_base_id === knowledgeBaseID)?.conditions || []
);

const updateConditions = (knowledgeBaseID: string, conditions: MetadataCondition[]) => {
  const next = props.modelValue.filter(item => item.knowledge_base_id !== knowledgeBaseID);
  if (conditions.length) {
    next.push({ knowledge_base_id: knowledgeBaseID, conditions });
  }
  emit('update:modelValue', next);
};

const pruneConditions = () => {
  const selectedIDs = new Set(props.knowledgeBases.map(item => item.id));
  const next = props.modelValue.flatMap(item => {
    if (!selectedIDs.has(item.knowledge_base_id)) return [];
    const definitions = definitionsByKnowledgeBase.value[item.knowledge_base_id];
    if (!definitions) return [item];
    const validIDs = new Set(definitions.filter(definition => definition.filterable).map(definition => definition.id));
    const conditions = item.conditions.filter(condition => validIDs.has(condition.metadata_definition_id));
    return conditions.length ? [{ ...item, conditions }] : [];
  });
  if (JSON.stringify(next) !== JSON.stringify(props.modelValue)) {
    emit('update:modelValue', next);
  }
};

const loadSchemas = async () => {
  const missing = props.knowledgeBases.filter(item => !definitionsByKnowledgeBase.value[item.id]);
  if (!missing.length) {
    pruneConditions();
    return;
  }
  loading.value = true;
  await Promise.all(missing.map(async item => {
    try {
      const response: any = await getMetadataSchema(item.id);
      definitionsByKnowledgeBase.value[item.id] = response?.data?.definitions || [];
    } catch {
      definitionsByKnowledgeBase.value[item.id] = [];
    }
  }));
  loading.value = false;
  pruneConditions();
};

watch(
  () => props.knowledgeBases.map(item => item.id),
  () => {
    pruneConditions();
    if (visible.value) loadSchemas();
  },
  { immediate: true },
);

watch(visible, value => {
  if (value) loadSchemas();
});
</script>

<template>
  <t-popup v-model="visible" trigger="click" placement="top-left" destroy-on-close>
    <t-tooltip :content="t('metadata.chatFilter')" placement="top" theme="light">
      <button
        class="chat-metadata-trigger"
        :class="{ active: activeCount > 0 }"
        type="button"
        :aria-label="t('metadata.chatFilter')"
        @click.stop
      >
        <t-icon name="filter" size="18px" />
        <span v-if="activeCount" class="condition-count">{{ activeCount }}</span>
      </button>
    </t-tooltip>

    <template #content>
      <section class="chat-metadata-panel" @click.stop>
        <header>
          <strong>{{ t('metadata.chatFilter') }}</strong>
          <span>{{ t('metadata.chatFilterDescription') }}</span>
        </header>

        <t-loading :loading="loading" size="small">
          <div class="knowledge-filter-list">
            <div v-for="knowledgeBase in knowledgeBases" :key="knowledgeBase.id" class="knowledge-filter-row">
              <div class="knowledge-name" :title="knowledgeBase.name">
                <t-icon name="folder" />
                <span>{{ knowledgeBase.name }}</span>
              </div>
              <MetadataFilterBar
                :definitions="definitionsByKnowledgeBase[knowledgeBase.id] || []"
                :model-value="conditionsFor(knowledgeBase.id)"
                @update:model-value="conditions => updateConditions(knowledgeBase.id, conditions)"
              />
              <span v-if="!(definitionsByKnowledgeBase[knowledgeBase.id] || []).some(item => item.filterable)" class="empty-hint">
                {{ t('metadata.noFilterableFields') }}
              </span>
            </div>
          </div>
        </t-loading>
      </section>
    </template>
  </t-popup>
</template>

<style scoped lang="less">
.chat-metadata-trigger {
  position: relative;
  width: 34px;
  height: 34px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 6px;
  color: var(--td-text-color-secondary);
  background: transparent;
  cursor: pointer;
}
.chat-metadata-trigger:hover,
.chat-metadata-trigger.active { color: var(--td-brand-color); background: var(--td-brand-color-light); }
.condition-count {
  position: absolute;
  top: 1px;
  right: 1px;
  min-width: 15px;
  height: 15px;
  padding: 0 3px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  color: white;
  background: var(--td-brand-color);
  font-size: 10px;
  line-height: 1;
}
.chat-metadata-panel { width: min(620px, calc(100vw - 24px)); padding: 16px; background: var(--td-bg-color-container); }
.chat-metadata-panel header { display: flex; flex-direction: column; gap: 3px; margin-bottom: 12px; }
.chat-metadata-panel header strong { font-size: 14px; }
.chat-metadata-panel header span { color: var(--td-text-color-placeholder); font-size: 12px; }
.knowledge-filter-list { max-height: min(360px, 50vh); display: flex; flex-direction: column; gap: 8px; overflow-y: auto; }
.knowledge-filter-row { min-height: 48px; display: grid; grid-template-columns: minmax(140px, 1fr) auto; align-items: center; gap: 12px; padding: 8px; border-bottom: 1px solid var(--td-component-stroke); }
.knowledge-name { min-width: 0; display: flex; align-items: center; gap: 7px; color: var(--td-text-color-primary); font-size: 13px; }
.knowledge-name span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.empty-hint { grid-column: 2; color: var(--td-text-color-placeholder); font-size: 12px; }
@media (max-width: 560px) {
  .knowledge-filter-row { grid-template-columns: 1fr; align-items: start; }
  .empty-hint { grid-column: 1; }
}
</style>
