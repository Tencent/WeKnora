<template>
  <t-switch :model-value="checked" :disabled="disabled" @update:model-value="onChange" />
</template>

<script setup lang="ts">
import { computed } from 'vue'

import type { WidgetProps } from './types'

const props = defineProps<WidgetProps>()
const emit = defineEmits<{ 'update:modelValue': [value: unknown] }>()

// String-typed booleans ("true" / "false") are stored as strings.
const checked = computed(() => props.modelValue === true || props.modelValue === 'true')

function onChange(v: unknown) {
  const on = v === true
  emit('update:modelValue', props.schema.type === 'string' ? String(on) : on)
}
</script>
