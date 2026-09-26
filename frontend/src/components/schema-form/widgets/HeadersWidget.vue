<template>
  <div class="headers-widget">
    <p v-if="stored" class="headers-widget__stored">{{ t('schemaForm.secretStored') }}</p>
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
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { isStoredSecret, secretInputText, secretInputValue } from '../schema'
import { parseHeaders, serializeHeaders, type HeaderRow } from './headers'
import type { WidgetProps } from './types'

// Edits "Name: Value" lines (RSS auth headers) as key/value rows. Rows stay
// local so a half-typed row without a name survives; only named rows reach
// the stored value.
const props = defineProps<WidgetProps>()
const emit = defineEmits<{ 'update:modelValue': [value: unknown] }>()
const { t } = useI18n()

// Stored secret headers ("***") start with no rows: new rows replace them,
// and removing every row again keeps them.
const stored = computed(() => isStoredSecret(props.schema, props.modelValue))
const hadStored = ref(stored.value)
const rows = ref<HeaderRow[]>(parseHeaders(secretInputText(props.schema, props.modelValue)))

watch(
  () => props.modelValue,
  v => {
    if (stored.value) hadStored.value = true
    const text = secretInputText(props.schema, v)
    if (text !== serializeHeaders(rows.value)) rows.value = parseHeaders(text)
  },
)

function emitValue() {
  const text = secretInputValue(serializeHeaders(rows.value), hadStored.value)
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

.headers-widget__stored {
  margin: 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
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
