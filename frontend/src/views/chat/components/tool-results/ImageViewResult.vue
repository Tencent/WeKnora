<template>
  <div class="image-view-result">
    <p class="image-view-summary">{{ summary }}</p>

    <div v-if="described.length" class="image-view-descriptions">
      <div class="image-view-heading">{{ $t('agentStream.imageView.descriptionsHeading') }}</div>
      <div v-for="(item, index) in described" :key="index" class="image-view-item">
        <div v-if="item.number" class="image-view-label">{{ $t('agentStream.imageView.imageLabel', { n: item.number }) }}</div>
        <div v-if="item.text" class="image-view-text">{{ item.text }}</div>
        <div v-else class="image-view-missing">{{ $t('agentStream.imageView.noDescription') }}</div>
      </div>
    </div>

    <ul v-if="errors.length" class="image-view-errors">
      <li v-for="(error, index) in errors" :key="index">{{ error }}</li>
    </ul>

    <p v-if="remaining > 0" class="image-view-remaining">
      {{ $t('agentStream.imageView.remaining', { count: remaining }) }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ImageViewData } from '@/types/tool-results'

const props = defineProps<{
  data: ImageViewData | Record<string, unknown>
}>()

const { t } = useI18n()

const record = computed(() => (props.data || {}) as ImageViewData)

// Numbered images belong to a document (attachment://…/images/N); a plain
// image file has none.
const numbers = computed(() =>
  Array.isArray(record.value.image_numbers) ? record.value.image_numbers.map(Number) : [],
)

const total = computed(() => Number(record.value.total_images) || 0)

const numbersLabel = computed(() => {
  const list = numbers.value
  if (list.length === 0) return ''
  if (list.length === 1) return String(list[0])
  const contiguous = list.every((n, i) => i === 0 || n === list[i - 1] + 1)
  return contiguous ? `${list[0]}–${list[list.length - 1]}` : list.join(', ')
})

const summary = computed(() => {
  const file = record.value.file_name || record.value.path || ''
  if (!numbers.value.length) return t('agentStream.imageView.single', { file })
  return t('agentStream.imageView.summary', { file, numbers: numbersLabel.value, total: total.value })
})

const described = computed(() => {
  const descriptions = record.value.image_descriptions
  if (!Array.isArray(descriptions)) return []
  if (numbers.value.length && descriptions.length !== numbers.value.length) return []
  return descriptions.map((text, i) => ({ number: numbers.value[i], text: String(text || '').trim() }))
})

const errors = computed(() =>
  Array.isArray(record.value.image_errors) ? record.value.image_errors.map(String) : [],
)

const remaining = computed(() => {
  const match = /\/images\/(\d+)$/.exec(String(record.value.next_path || ''))
  if (!match) return 0
  return Math.max(total.value - Number(match[1]) + 1, 0)
})
</script>

<style lang="less" scoped>
.image-view-result {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 0;
  font-size: var(--app-text-sm);
  line-height: 1.55;
}

.image-view-summary,
.image-view-remaining {
  margin: 0;
  color: var(--td-text-color-secondary);
}

.image-view-heading {
  margin-bottom: 4px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
}

.image-view-descriptions {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 320px;
  overflow: auto;
}

.image-view-item {
  padding: 8px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
}

.image-view-label {
  margin-bottom: 2px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
}

.image-view-text {
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  word-break: break-word;
}

.image-view-missing {
  color: var(--td-text-color-placeholder);
}

.image-view-errors {
  margin: 0;
  padding-left: 18px;
  color: var(--td-warning-color);
}
</style>
