<template>
  <div class="headers-widget">
    <div v-for="(row, idx) in rows" :key="idx" class="headers-widget__row">
      <t-input
        v-model="row.key"
        class="headers-widget__key"
        :placeholder="t('model.editor.customHeadersKeyPlaceholder')"
        :disabled="disabled"
        autocomplete="off"
        spellcheck="false"
        @change="emitValue"
      />
      <t-input
        v-model="row.value"
        class="headers-widget__value"
        :placeholder="t('model.editor.customHeadersValuePlaceholder')"
        :disabled="disabled"
        autocomplete="off"
        spellcheck="false"
        @change="emitValue"
      />
      <t-button
        variant="text"
        shape="square"
        size="small"
        class="headers-widget__remove"
        :aria-label="t('common.delete')"
        :disabled="disabled"
        @click="remove(idx)"
      >
        <t-icon name="close" />
      </t-button>
    </div>
    <t-button variant="text" size="small" theme="primary" :disabled="disabled" @click="add">
      <template #icon><t-icon name="add" /></template>
      {{ t('model.editor.customHeadersAdd') }}
    </t-button>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { parseHeaders, serializeHeaders, type HeaderRow } from './headers'
import type { WidgetProps } from './types'

// Edits "Name: Value" lines (RSS auth headers) as key/value rows. Rows stay
// local so a half-typed row without a name survives; only named rows reach
// the stored value.
const props = defineProps<WidgetProps>()
const emit = defineEmits<{ 'update:modelValue': [value: unknown] }>()
const { t } = useI18n()

const rows = ref<HeaderRow[]>(parseHeaders(props.modelValue))

watch(
  () => props.modelValue,
  v => {
    if ((v ?? '') !== serializeHeaders(rows.value)) rows.value = parseHeaders(v)
  },
)

function emitValue() {
  const text = serializeHeaders(rows.value)
  emit('update:modelValue', text || undefined)
}

function add() {
  rows.value.push({ key: '', value: '' })
}

function remove(idx: number) {
  rows.value.splice(idx, 1)
  emitValue()
}
</script>

<style lang="less" scoped>
.headers-widget {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
}

.headers-widget__row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.headers-widget__key {
  flex: 0 0 38%;
}

.headers-widget__value {
  flex: 1;
}

.headers-widget__remove {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  padding: 0;
  color: var(--td-text-color-placeholder);
  border-radius: var(--app-radius-sm);
  transition: all var(--app-motion-fast) ease;

  &:hover {
    background: var(--td-error-color-light);
    color: var(--td-error-color);
  }
}
</style>
