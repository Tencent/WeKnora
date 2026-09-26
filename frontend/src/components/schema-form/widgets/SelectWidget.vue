<template>
  <t-select
    :model-value="modelValue as string | number | undefined"
    :placeholder="placeholder"
    :disabled="disabled"
    :clearable="!required"
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <t-option
      v-for="choice in choices"
      :key="String(choice.value)"
      :value="choice.value as string | number"
      :label="choice.label"
    />
  </t-select>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import { choicesOf } from '../schema'
import { useSchemaText } from '../useSchemaText'
import type { WidgetProps } from './types'

const props = defineProps<WidgetProps>()
defineEmits<{ 'update:modelValue': [value: unknown] }>()

const text = useSchemaText()

const choices = computed(() =>
  choicesOf(props.schema).map(c => ({
    value: c.value,
    label: text(c.schema, 'title') || String(c.value),
  })),
)
</script>
