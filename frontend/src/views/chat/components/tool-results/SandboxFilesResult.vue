<template>
  <div class="sandbox-files-result">
    <div v-if="summary" class="results-summary-text">{{ summary }}</div>
    <div v-if="items.length" class="results-list">
      <ResultRow
        v-for="(item, index) in items"
        :key="item.path || `${item.name}-${index}`"
        :index="index + 1"
        :title="item.name || item.path"
        :meta="metaFor(item)"
        :popup-key="item.path || index"
        :show-popup="false"
      />
    </div>
    <div v-else class="empty-state">{{ $t('agentStream.sandboxFiles.empty') }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatFileSize } from '@/utils/files'
import {
  formatSandboxModifiedAt,
  sandboxFileListItems,
  type SkillFileListItem,
} from '@/utils/skillToolDisplay'
import type { ListSandboxFilesData } from '@/types/tool-results'
import ResultRow from './ResultRow.vue'

const props = defineProps<{
  data: ListSandboxFilesData | Record<string, unknown>
}>()

const { t } = useI18n()

const items = computed(() => sandboxFileListItems(props.data))

const summary = computed(() => {
  if (!items.value.length) return ''
  const record = (props.data || {}) as Record<string, unknown>
  const count = typeof record.count === 'number' ? record.count : items.value.length
  const parts = [t('agentStream.sandboxFiles.found', { count })]
  if (record.truncated) {
    parts.push(t('agentStream.sandboxFiles.truncated'))
  }
  return parts.join(' · ')
})

function sizeLabel(size?: number): string {
  if (size == null || !Number.isFinite(size) || size < 0) return ''
  if (size === 0) return '0 B'
  return formatFileSize(size)
}

function metaFor(item: SkillFileListItem): string {
  const parts = [sizeLabel(item.size), item.modifiedAt ? formatSandboxModifiedAt(item.modifiedAt) : '']
  return parts.filter(Boolean).join(' · ')
}
</script>

<style lang="less" scoped>
@import './tool-results.less';

.sandbox-files-result {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.results-summary-text {
  font-size: var(--agent-step-summary-size, 12px);
  font-weight: 400;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
}

.results-list {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.empty-state {
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}
</style>
