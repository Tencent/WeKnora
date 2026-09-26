<template>
  <t-input
    :model-value="(modelValue as string | undefined) ?? ''"
    type="password"
    autocomplete="new-password"
    :placeholder="placeholder"
    :disabled="disabled"
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <template #prefix-icon><t-icon name="lock-on" /></template>
  </t-input>
</template>

<script setup lang="ts">
import type { WidgetProps } from './types'

defineProps<WidgetProps>()
defineEmits<{ 'update:modelValue': [value: unknown] }>()
</script>
