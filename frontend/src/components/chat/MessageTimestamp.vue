<template>
  <time v-if="label" class="message-time" :class="alignClass" :datetime="datetime">{{ label }}</time>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { formatMessageTimestamp, normalizeMessageCreatedAt } from '@/utils/messageTimestamp'

const props = defineProps<{
  value?: unknown
  align?: 'start' | 'end'
}>()

const datetime = computed(() => normalizeMessageCreatedAt(props.value))
const label = computed(() => formatMessageTimestamp(props.value))
const alignClass = computed(() =>
  props.align === 'end' ? 'message-time--end' : 'message-time--start',
)
</script>

<style scoped lang="less">
.message-time {
  margin-top: 4px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  line-height: 20px;
}

.message-time--start {
  align-self: flex-start;
}

.message-time--end {
  align-self: flex-end;
}
</style>
