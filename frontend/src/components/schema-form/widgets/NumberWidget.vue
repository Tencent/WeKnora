<template>
  <t-input-number
    :model-value="numeric"
    :placeholder="placeholder"
    :disabled="disabled"
    :min="schema.minimum"
    :max="schema.maximum"
    :decimal-places="schema.type === 'integer' ? 0 : undefined"
    theme="normal"
    @update:model-value="onChange"
  />
</template>

<script setup lang="ts">
import { computed } from 'vue'

import type { WidgetProps } from './types'

const props = defineProps<WidgetProps>()
const emit = defineEmits<{ 'update:modelValue': [value: unknown] }>()

// A string-typed field (model vendor extra_config is a map of strings) keeps
// storing strings; the widget only changes how it is edited.
const numeric = computed(() => {
  const v = props.modelValue
  if (v === undefined || v === null || v === '') return undefined
  const n = Number(v)
  return Number.isNaN(n) ? undefined : n
})

function onChange(v: number | string | undefined) {
  if (v === undefined || v === null || v === '') {
    emit('update:modelValue', undefined)
    return
  }
  emit('update:modelValue', props.schema.type === 'string' ? String(v) : Number(v))
}
</script>
