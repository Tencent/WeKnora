<template>
  <div class="vlm-chain-selector">
    <div
      v-for="(modelId, index) in localModelIds"
      :key="`vlm-chain-${index}`"
      class="vlm-chain-row"
    >
      <div class="vlm-chain-row__label">
        <span class="vlm-chain-row__badge">{{ index + 1 }}</span>
        <span>{{ index === 0 ? t('knowledgeEditor.advanced.multimodal.vllmChainPrimaryLabel') : `${t('knowledgeEditor.advanced.multimodal.vllmChainFallbackLabel')} ${index}` }}</span>
        <span v-if="index === 0" class="required">*</span>
      </div>
      <div class="vlm-chain-row__selector">
        <ModelSelector
          model-type="VLLM"
          :selected-model-id="modelId"
          :all-models="allModels"
          :status="index === 0 ? status : 'default'"
          :placeholder="index === 0 ? t('knowledgeEditor.advanced.multimodal.vllmPlaceholder') : t('knowledgeEditor.advanced.multimodal.fallbackVllmPlaceholder')"
          @update:selected-model-id="(value: string) => updateModel(index, value)"
          @add-model="handleAddModel"
        />
      </div>
      <div class="vlm-chain-row__actions">
        <t-button
          size="small"
          variant="text"
          :disabled="index === 0"
          @click="moveModel(index, -1)"
        >
          {{ t('knowledgeEditor.advanced.multimodal.vllmChainMoveUp') }}
        </t-button>
        <t-button
          size="small"
          variant="text"
          :disabled="index === localModelIds.length - 1"
          @click="moveModel(index, 1)"
        >
          {{ t('knowledgeEditor.advanced.multimodal.vllmChainMoveDown') }}
        </t-button>
        <t-button
          v-if="index > 0"
          size="small"
          variant="text"
          theme="danger"
          @click="removeModel(index)"
        >
          {{ t('knowledgeEditor.advanced.multimodal.vllmChainRemove') }}
        </t-button>
      </div>
    </div>

    <div class="vlm-chain-footer">
      <t-button
        size="small"
        variant="outline"
        :disabled="localModelIds.length >= maxModels"
        @click="addFallback"
      >
        {{ t('knowledgeEditor.advanced.multimodal.vllmChainAdd') }}
      </t-button>
      <span class="vlm-chain-footer__hint">
        {{ t('knowledgeEditor.advanced.multimodal.vllmChainHint', { max: maxModels }) }}
      </span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import ModelSelector from '@/components/ModelSelector.vue'

const props = withDefaults(defineProps<{
  modelIds?: string[]
  allModels?: any[]
  maxModels?: number
  status?: 'default' | 'success' | 'warning' | 'error'
}>(), {
  modelIds: () => [''],
  allModels: () => [],
  maxModels: 5,
  status: 'default',
})

const emit = defineEmits<{
  'update:modelIds': [value: string[]]
  'add-model': []
}>()

const { t } = useI18n()

const localModelIds = computed(() => {
  const ids = Array.isArray(props.modelIds) && props.modelIds.length > 0
    ? props.modelIds
    : ['']
  return ids.slice(0, props.maxModels)
})

function normalize(ids: string[]) {
  const result: string[] = []
  const seen = new Set<string>()
  for (const raw of ids) {
    const id = (raw || '').trim()
    if (id && seen.has(id)) continue
    if (id) seen.add(id)
    result.push(id)
    if (result.length >= props.maxModels) break
  }
  return result.length > 0 ? result : ['']
}

function updateModel(index: number, value: string) {
  const next = [...localModelIds.value]
  const trimmed = (value || '').trim()
  if (trimmed && next.some((id, i) => i !== index && id === trimmed)) {
    MessagePlugin.warning(t('knowledgeEditor.advanced.multimodal.vllmChainDuplicate'))
    return
  }
  next[index] = trimmed
  emit('update:modelIds', normalize(next))
}

function addFallback() {
  if (localModelIds.value.length >= props.maxModels) return
  emit('update:modelIds', [...localModelIds.value, ''])
}

function handleAddModel() {
  emit('add-model')
}

function removeModel(index: number) {
  if (index <= 0) return
  const next = localModelIds.value.filter((_, i) => i !== index)
  emit('update:modelIds', normalize(next))
}

function moveModel(index: number, delta: number) {
  const nextIndex = index + delta
  if (nextIndex < 0 || nextIndex >= localModelIds.value.length) return
  const next = [...localModelIds.value]
  const current = next[index]
  next[index] = next[nextIndex]
  next[nextIndex] = current
  emit('update:modelIds', normalize(next))
}
</script>

<style scoped lang="less">
.vlm-chain-selector {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 100%;
}

.vlm-chain-row {
  display: grid;
  grid-template-columns: 140px minmax(0, 1fr) auto;
  gap: 12px;
  align-items: center;
}

.vlm-chain-row__label {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--td-text-color-primary);
  font-size: 13px;
  white-space: nowrap;
}

.vlm-chain-row__badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 999px;
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
  font-weight: 600;
}

.vlm-chain-row__selector {
  min-width: 0;
}

.vlm-chain-row__actions {
  display: flex;
  align-items: center;
  gap: 4px;
}

.vlm-chain-footer {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.vlm-chain-footer__hint {
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.required {
  color: var(--td-error-color);
}

@media (max-width: 768px) {
  .vlm-chain-row {
    grid-template-columns: 1fr;
  }

  .vlm-chain-row__actions {
    justify-content: flex-start;
  }
}
</style>
