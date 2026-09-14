<template>
  <time v-if="label" class="conversation-time" :datetime="datetime">{{ label }}</time>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { getConversationTimestampModel } from '@/utils/messageTimestamp'

const props = defineProps<{
  value?: unknown
}>()

const { t } = useI18n()

const model = computed(() => getConversationTimestampModel(props.value))
const datetime = computed(() => model.value?.datetime ?? '')
const label = computed(() => {
  const next = model.value
  if (!next) return ''
  if (next.kind === 'today') return t('chat.conversationTime.today', { time: next.time })
  if (next.kind === 'yesterday') return t('chat.conversationTime.yesterday', { time: next.time })
  if (next.kind === 'thisYear') {
    return t('chat.conversationTime.thisYear', { month: next.month, day: next.day, time: next.time })
  }
  return t('chat.conversationTime.otherYear', {
    year: next.year,
    month: next.month,
    day: next.day,
    time: next.time,
  })
})
</script>

<style scoped lang="less">
.conversation-time {
  display: block;
  width: 100%;
  padding: 4px 0 8px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  line-height: 20px;
  text-align: center;
  user-select: none;
}
</style>
