<template>
  <t-textarea
    :model-value="secret.text.value"
    :placeholder="secret.placeholder.value"
    :disabled="disabled"
    :maxlength="schema.maxLength"
    :autosize="{ minRows: 3, maxRows: 10 }"
    @update:model-value="(v: string) => secret.update(v)"
  />
</template>

<script setup lang="ts">
import { useSecretField } from './secretField'
import type { WidgetProps } from './types'

const props = defineProps<WidgetProps>()
const emit = defineEmits<{ 'update:modelValue': [value: unknown] }>()
// A secret shown as text (x-widget: textarea) masks a stored value the same way.
const secret = useSecretField(props, (v) => emit('update:modelValue', v))
</script>
