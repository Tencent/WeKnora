<template>
  <div class="read-skill-result">
    <p v-if="description" class="read-skill-desc">{{ description }}</p>

    <div v-if="files.length" class="read-skill-files">
      <div class="read-skill-heading">{{ $t('agentStream.skillFiles.heading') }}</div>
      <div class="results-list">
        <ResultRow
          v-for="(item, index) in files"
          :key="item.path || `${item.name}-${index}`"
          :index="index + 1"
          :title="item.path || item.name"
          :meta="item.isScript ? $t('agentStream.skillFiles.script') : ''"
          :popup-key="item.path || index"
          :show-popup="false"
        />
      </div>
    </div>

    <div v-if="body" class="read-skill-stream">
      <div class="read-skill-stream-label">{{ bodyLabel }}</div>
      <pre class="read-skill-stream-body">{{ body }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { skillFileListItems } from '@/utils/skillToolDisplay'
import type { ReadSkillData } from '@/types/tool-results'
import ResultRow from './ResultRow.vue'

const props = defineProps<{
  data: ReadSkillData | Record<string, unknown>
}>()

const { t } = useI18n()

const record = computed(() => (props.data || {}) as Record<string, unknown>)

const description = computed(() => {
  const value = record.value.description
  return typeof value === 'string' ? value.trim() : ''
})

const files = computed(() => skillFileListItems(record.value))

const filePath = computed(() => {
  const value = record.value.file_path
  return typeof value === 'string' ? value.trim() : ''
})

const body = computed(() => {
  const content = record.value.content
  if (typeof content === 'string' && content.trim()) return content
  if (filePath.value) return ''
  const instructions = record.value.instructions
  return typeof instructions === 'string' ? instructions : ''
})

const bodyLabel = computed(() =>
  filePath.value
    ? filePath.value
    : t('agentStream.skillFiles.instructions'),
)
</script>

<style lang="less" scoped>
.read-skill-result {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 0;
}

.read-skill-desc {
  margin: 0;
  font-size: 12px;
  line-height: 1.55;
  color: var(--td-text-color-secondary);
}

.read-skill-heading {
  margin-bottom: 4px;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.4;
  color: var(--td-text-color-secondary);
}

.read-skill-stream {
  min-width: 0;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  overflow: hidden;
  background: var(--td-bg-color-container);
}

.read-skill-stream-label {
  padding: 6px 12px;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.4;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);
  border-bottom: 1px solid var(--td-component-stroke);
}

.read-skill-stream-body {
  margin: 0;
  padding: 10px 12px;
  max-height: 280px;
  overflow: auto;
  font-family: var(--app-font-family-mono);
  font-size: 12px;
  line-height: 1.55;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  word-break: break-word;

  &::-webkit-scrollbar {
    width: 8px;
    height: 8px;
  }

  &::-webkit-scrollbar-thumb {
    background: var(--td-component-border);
    border-radius: 4px;
  }
}
</style>
