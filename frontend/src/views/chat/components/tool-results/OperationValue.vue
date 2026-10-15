<template>
  <span v-if="value == null" class="empty-value">—</span>
  <template v-else-if="Array.isArray(value)">
    <span v-if="!value.length" class="empty-value">{{ $t('agentStream.assistantTools.empty') }}</span>
    <ol v-else class="value-list">
      <li v-for="(item, index) in value" :key="index">
        <details v-if="itemTitle(item)" class="value-record">
          <summary>
            <span class="value-index">#{{ index + 1 }}</span>
            <span class="value-title">{{ itemTitle(item) }}</span>
            <span v-if="itemStatus(item)" class="value-status" :class="{ 'is-error': toolRecord(item).parse_status === 'failed' || toolRecord(item).status === 'failed' }">{{ itemStatus(item) }}</span>
            <span class="value-chevron" aria-hidden="true">›</span>
          </summary>
          <div class="value-detail"><OperationValue :value="item" /></div>
        </details>
        <OperationValue v-else :value="item" />
      </li>
    </ol>
  </template>
  <template v-else-if="typeof value === 'object'">
    <span v-if="!Object.keys(value).length" class="empty-value">{{ $t('agentStream.assistantTools.empty') }}</span>
    <dl v-else class="value-fields">
      <div v-for="(item, key) in value" :key="key">
        <dt>{{ key }}</dt><dd><OperationValue :value="item" /></dd>
      </div>
    </dl>
  </template>
  <span v-else class="scalar-value">{{ value }}</span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { toolRecord, toolText } from '@/utils/assistantToolDisplay'
defineProps<{ value: unknown }>()
const { t } = useI18n()
const itemTitle = (item: unknown) => {
  const row = toolRecord(item)
  return toolText(row.title) || toolText(row.name) || toolText(row.standard_question)
}
const itemStatus = (item: unknown) => {
  const row = toolRecord(item)
  const status = toolText(row.parse_status) || toolText(row.status)
  if (status === 'completed') return t('agentStream.assistantTools.completed')
  if (status === 'failed') return t('common.failed')
  return status
}
</script>

<style scoped lang="less">
.empty-value { color: var(--td-text-color-placeholder); }
.scalar-value { white-space: pre-wrap; overflow-wrap: anywhere; }
.value-list { margin: 0; padding: 0; list-style: none; }
.value-list > li + li { margin-top: 4px; }
.value-record > summary { display: flex; align-items: center; gap: 8px; padding: 4px 0; cursor: pointer; list-style: none; }
.value-record > summary::-webkit-details-marker { display: none; }
.value-record > summary:hover .value-title { color: var(--td-brand-color); }
.value-index { min-width: 22px; font-size: var(--app-text-xs); color: var(--td-text-color-placeholder); }
.value-title { flex: 1; min-width: 0; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; color: var(--td-text-color-primary); }
.value-status { font-size: var(--app-text-xs); color: var(--td-text-color-placeholder); }
.value-status.is-error { color: var(--td-error-color); }
.value-chevron { color: var(--td-text-color-placeholder); transition: transform var(--app-motion-fast); }
.value-record[open] > summary .value-chevron { transform: rotate(90deg); }
.value-detail { margin: 2px 0 8px 30px; }
.value-fields { margin: 0; }
.value-fields > div { display: grid; grid-template-columns: minmax(72px, 25%) minmax(0, 1fr); gap: 10px; padding: 2px 0; }
dt { color: var(--td-text-color-placeholder); overflow-wrap: anywhere; font-size: var(--app-text-xs); }
dd { margin: 0; min-width: 0; }
</style>
