<template>
  <div class="sandbox-files-result">
    <ResultRow
      :index="1"
      :title="title"
      :meta="meta"
      :popup-key="path"
      :show-popup="false"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatFileSize } from '@/utils/files'
import type { WriteSandboxFileData } from '@/types/tool-results'
import ResultRow from './ResultRow.vue'

const props = defineProps<{
  data: WriteSandboxFileData | Record<string, unknown>
}>()

const { t } = useI18n()

const record = computed(() => (props.data || {}) as Record<string, unknown>)

const path = computed(() => String(record.value.path || ''))

const title = computed(() => {
  const name = String(record.value.name || '').trim()
  if (name) return name
  const parts = path.value.split('/').filter(Boolean)
  const isEdit = record.value.display_type === 'edit_sandbox_file'
  return parts[parts.length - 1] || path.value || t(isEdit ? 'agentStream.sandboxFiles.edited' : 'agentStream.sandboxFiles.wrote')
})

const meta = computed(() => {
  const size = record.value.size
  const replacements = record.value.replacements
  const isEdit = record.value.display_type === 'edit_sandbox_file' || typeof replacements === 'number'
  const parts = [t(isEdit ? 'agentStream.sandboxFiles.edited' : 'agentStream.sandboxFiles.wrote')]
  if (isEdit && typeof replacements === 'number' && Number.isFinite(replacements)) {
    parts.push(t('agentStream.sandboxFiles.replacements', { count: replacements }))
  }
  if (typeof size === 'number' && Number.isFinite(size) && size >= 0) {
    parts.push(size === 0 ? '0 B' : formatFileSize(size))
  }
  if (path.value) parts.push(path.value)
  return parts.join(' · ')
})
</script>

<style lang="less" scoped>
@import './tool-results.less';

.sandbox-files-result {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
</style>
