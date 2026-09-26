<template>
  <t-textarea
    :model-value="(modelValue as string | undefined) ?? ''"
    :placeholder="placeholder"
    :disabled="disabled"
    :maxlength="schema.maxLength"
    :autosize="{ minRows: 3, maxRows: 10 }"
    @update:model-value="$emit('update:modelValue', $event)"
  />
</template>

<script setup lang="ts">
import type { WidgetProps } from './types'

defineProps<WidgetProps>()
defineEmits<{ 'update:modelValue': [value: unknown] }>()
</script>
