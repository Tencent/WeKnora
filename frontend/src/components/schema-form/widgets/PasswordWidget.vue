<template>
  <t-input
    :model-value="secret.text.value"
    type="password"
    autocomplete="new-password"
    :placeholder="secret.placeholder.value"
    :disabled="disabled"
    @update:model-value="(v: string) => secret.update(v)"
  >
    <template #prefix-icon><t-icon name="lock-on" /></template>
  </t-input>
</template>

<script setup lang="ts">
import { useSecretField } from './secretField'
import type { WidgetProps } from './types'

const props = defineProps<WidgetProps>()
const emit = defineEmits<{ 'update:modelValue': [value: unknown] }>()
// A stored secret shows as an empty field: typing replaces it.
const secret = useSecretField(props, (v) => emit('update:modelValue', v))
</script>
