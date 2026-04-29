<template>
  <div class="kb-multimodal-settings">
    <div class="section-header">
      <h2>{{ $t('knowledgeEditor.indexing.title') }}</h2>
      <p class="section-description">{{ $t('knowledgeEditor.indexing.description') }}</p>
    </div>

    <div class="settings-group">
      <!-- Hybrid Search (vector + keyword combined) -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.indexing.searchTitle') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.indexing.searchDesc') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            :model-value="searchEnabled"
            @change="handleSearchToggle"
            size="medium"
          />
        </div>
      </div>

      <!-- Wiki -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.indexing.wikiTitle') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.indexing.wikiDesc') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            :model-value="modelValue.wikiEnabled"
            @change="(val: boolean) => update('wikiEnabled', val)"
            size="medium"
          />
        </div>
      </div>

      <!-- Wiki sub-settings (inline when enabled) -->
      <template v-if="modelValue.wikiEnabled">
        <slot name="wiki-settings" />
      </template>

      <!-- Knowledge Graph -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.indexing.graphTitle') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.indexing.graphDesc') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            :model-value="modelValue.graphEnabled"
            @change="(val: boolean) => update('graphEnabled', val)"
            size="medium"
          />
        </div>
      </div>

      <!-- Graph sub-settings (inline when enabled) -->
      <template v-if="modelValue.graphEnabled">
        <slot name="graph-settings" />
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

export interface IndexingStrategy {
  vectorEnabled: boolean
  keywordEnabled: boolean
  wikiEnabled: boolean
  graphEnabled: boolean
}

const props = defineProps<{
  modelValue: IndexingStrategy
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: IndexingStrategy): void
}>()

// Search = vector + keyword combined (the system always uses hybrid search internally)
const searchEnabled = computed(() => props.modelValue.vectorEnabled || props.modelValue.keywordEnabled)

const handleSearchToggle = (val: boolean) => {
  emit('update:modelValue', {
    ...props.modelValue,
    vectorEnabled: val,
    keywordEnabled: val,
  })
}

const update = (field: keyof IndexingStrategy, value: boolean) => {
  emit('update:modelValue', {
    ...props.modelValue,
    [field]: value,
  })
}
</script>

<style lang="less">
/* NOT scoped — these classes must match the parent modal's scoped styles.
   Since slot content is rendered in the parent scope, only the wrapper
   elements defined HERE need their own styles. We replicate the same
   design tokens used by .kb-multimodal-settings in the parent. */
</style>
