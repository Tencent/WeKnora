<template>
  <div class="range-selector">
    <t-select :model-value="modelValue" :options="options" class="range-selector__select" @change="changeRange" />
    <t-date-range-picker
      v-if="modelValue === 'custom'"
      :model-value="custom ?? []"
      value-type="YYYY-MM-DD"
      :disable-date="disableDate"
      :popup-props="popupProps"
      :placeholder="['开始日期', '结束日期']"
      clearable
      @change="changeCustom"
    />
  </div>
</template>

<script setup lang="ts">
import type { DashboardRange } from '@/types/videohub'

defineProps<{ modelValue: DashboardRange; custom: [string, string] | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: DashboardRange]; 'update:custom': [value: [string, string] | null]; invalid: [message: string] }>()
const options = [
  { label: '近 7 天', value: '7d' }, { label: '近 30 天', value: '30d' },
  { label: '近 90 天', value: '90d' }, { label: '自定义', value: 'custom' },
]
const popupProps = { overlayInnerClassName: 'dashboard-range-popup' }
function calendarDay(value: string) { const [year, month, day] = value.split('-').map(Number); return Date.UTC(year, month - 1, day) }
function inclusiveDays(range: [string, string]) { return Math.round((calendarDay(range[1]) - calendarDay(range[0])) / 86400000) + 1 }
function changeRange(value: string | number) { emit('update:modelValue', value as DashboardRange); if (value !== 'custom') emit('update:custom', null) }
function changeCustom(value: Array<string | number | Date>) {
  if (value.length !== 2) { emit('update:custom', null); return }
  const range = value.map(String) as [string, string]
  if (inclusiveDays(range) > 90) { emit('invalid', '自定义时间范围最长 90 天'); return }
  emit('update:custom', range)
}
function disableDate(args: { date: Date }) { return args.date.getTime() > Date.now() }
</script>

<style scoped>
.range-selector { display: flex; align-items: center; justify-content: flex-end; gap: var(--td-comp-margin-s); }
.range-selector__select { width: 132px; }
@media (max-width: 720px) { .range-selector { align-items: stretch; flex-direction: column; width: 100%; } .range-selector__select { width: 100%; } }
</style>

<style>
.dashboard-range-popup { border: var(--border-width-hairline, .5px) solid var(--color-stroke, var(--td-component-stroke)); border-radius: var(--rounded-popup, 10px); background: var(--color-bg-popup, var(--td-bg-color-container)); box-shadow: var(--shadow-popup); backdrop-filter: blur(20px) saturate(180%); }
</style>
