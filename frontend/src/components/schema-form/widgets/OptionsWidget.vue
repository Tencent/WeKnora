<template>
  <div class="options-widget">
    <t-select
      v-if="source"
      :model-value="selected"
      :options="selectOptions"
      :placeholder="placeholder"
      :disabled="disabled"
      :clearable="!required"
      :multiple="multiple"
      :loading="loading"
      filterable
      :on-search="spec?.search ? onSearch : undefined"
      @popup-visible-change="onPopup"
      @update:model-value="onChange"
    >
      <template #empty>
        <div class="options-widget__empty">{{ loading ? t('schemaForm.options.loading') : t('schemaForm.options.empty') }}</div>
      </template>
    </t-select>
    <t-input
      v-else
      :model-value="Array.isArray(modelValue) ? modelValue.join(', ') : ((modelValue as string | undefined) ?? '')"
      :placeholder="placeholder"
      :disabled="disabled"
      @update:model-value="onText"
    />
    <p v-if="error" class="options-widget__error">
      {{ error }}
      <t-link theme="primary" size="small" @click="load(query)">{{ t('schemaForm.options.retry') }}</t-link>
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { dependencyKey, useSchemaFormSource, type FieldOption } from '../source'
import type { WidgetProps } from './types'

// Choices the plugin lists for this field (x-options), reloaded when the
// fields they depend on change. Values the plugin no longer lists stay
// selectable so opening a form never drops a saved choice.
const props = defineProps<WidgetProps>()
const emit = defineEmits<{ 'update:modelValue': [value: unknown] }>()
const { t } = useI18n()

const source = useSchemaFormSource()
const spec = computed(() => props.schema['x-options'])
const multiple = computed(() => props.schema.type === 'array')

const options = ref<FieldOption[]>([])
const loading = ref(false)
const error = ref('')
const query = ref('')
let loadedKey: string | null = null
let seq = 0

const selected = computed(() => {
  const v = props.modelValue
  if (multiple.value) return Array.isArray(v) ? v : []
  return typeof v === 'string' ? v : undefined
})

const selectOptions = computed(() => {
  const out = options.value.map(o => ({ value: o.value, label: o.label || o.value, title: o.description }))
  const known = new Set(out.map(o => o.value))
  const current = Array.isArray(selected.value) ? selected.value : selected.value ? [selected.value] : []
  for (const v of current) {
    if (typeof v === 'string' && !known.has(v)) out.push({ value: v, label: v, title: undefined })
  }
  return out
})

async function load(q = '') {
  if (!source || !spec.value) return
  const mine = ++seq
  loading.value = true
  error.value = ''
  try {
    const got = await source.options(props.path ?? '', spec.value, q)
    if (mine === seq) options.value = got
  } catch (e) {
    if (mine === seq) {
      options.value = []
      error.value = (e as { message?: string })?.message || t('schemaForm.options.failed')
    }
  } finally {
    if (mine === seq) loading.value = false
  }
}

const depKey = computed(() => dependencyKey(source, spec.value))

onMounted(() => {
  loadedKey = depKey.value
  void load()
})

// Typing into a field the choices depend on reloads them once it settles.
let timer: ReturnType<typeof setTimeout> | undefined
watch(depKey, key => {
  clearTimeout(timer)
  timer = setTimeout(() => {
    if (key === loadedKey) return
    loadedKey = key
    void load(query.value)
  }, 400)
})

function onPopup(visible: boolean) {
  if (visible && error.value && !loading.value) void load(query.value)
}

let searchTimer: ReturnType<typeof setTimeout> | undefined
function onSearch(q: string) {
  query.value = q
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => void load(q), 300)
}

function onChange(v: unknown) {
  if (multiple.value) {
    emit('update:modelValue', Array.isArray(v) && v.length ? v : undefined)
    return
  }
  emit('update:modelValue', v === '' || v === null ? undefined : v)
}

function onText(v: string) {
  if (!multiple.value) {
    emit('update:modelValue', v || undefined)
    return
  }
  const list = v.split(',').map(s => s.trim()).filter(Boolean)
  emit('update:modelValue', list.length ? list : undefined)
}
</script>

<style lang="less" scoped>
.options-widget {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.options-widget__empty {
  padding: 8px 12px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
}

.options-widget__error {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  color: var(--td-error-color);
  font-size: var(--app-text-sm);
  line-height: 1.5;
}
</style>
