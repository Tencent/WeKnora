<template>
  <div class="schema-field">
    <label class="form-label" :class="{ required: field.required }">{{ fieldLabel }}</label>

    <t-select
      v-if="field.type === 'select'"
      :value="modelValue"
      :placeholder="field.description || undefined"
      @change="(v: unknown) => emit('update:modelValue', v)"
    >
      <t-option
        v-for="option in field.options || []"
        :key="option.value"
        :value="option.value"
        :label="optionText(option)"
      />
    </t-select>

    <t-input
      v-else-if="field.type === 'secret'"
      :value="modelValue"
      type="password"
      autocomplete="off"
      spellcheck="false"
      :placeholder="field.description || undefined"
      @change="(v: unknown) => emit('update:modelValue', v)"
    >
      <template #prefix-icon><t-icon name="lock-on" /></template>
    </t-input>

    <t-input-number
      v-else-if="field.type === 'number'"
      :value="modelValue"
      :placeholder="field.description || undefined"
      @change="(v: unknown) => emit('update:modelValue', v)"
    />

    <t-switch
      v-else-if="field.type === 'boolean'"
      :value="modelValue"
      @change="(v: unknown) => emit('update:modelValue', v)"
    />

    <t-input
      v-else-if="field.type === 'array'"
      :value="arrayText"
      :placeholder="field.description || undefined"
      @change="(v: unknown) => emit('update:modelValue', splitArray(v))"
    />

    <t-input
      v-else
      :value="modelValue"
      :placeholder="field.description || undefined"
      @change="(v: unknown) => emit('update:modelValue', v)"
    />

    <p v-if="field.description" class="form-desc">{{ fieldDescription }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

// SchemaField 是与后端 config_fields 对齐的通用字段描述，供各扩展点
// （model / search / parser / retriever）动态渲染插件配置表单。
export interface SchemaFieldOption {
  label: string
  label_key?: string
  value: string
}

export interface SchemaField {
  key: string
  label: string
  label_key?: string
  type: string // string / secret / number / boolean / array / select
  required?: boolean
  default?: string
  description?: string
  description_key?: string
  options?: SchemaFieldOption[]
}

const props = defineProps<{
  field: SchemaField
  modelValue: unknown
}>()

const emit = defineEmits<{
  'update:modelValue': [value: unknown]
}>()

const { t, te } = useI18n()

// 优先用 i18n key，缺失时回退到后端提供的原始文案。
const localized = (key: string | undefined, fallback: string): string => {
  if (key && te(key)) return t(key)
  return fallback
}

const fieldLabel = computed(() => localized(props.field.label_key, props.field.label))
const fieldDescription = computed(() => localized(props.field.description_key, props.field.description || ''))

const optionText = (option: SchemaFieldOption): string => localized(option.label_key, option.label)

// array 字段以逗号分隔文本编辑（与 datasource / web-search 约定一致）。
const arrayText = computed(() => {
  const value = props.modelValue
  return Array.isArray(value) ? value.join(', ') : String(value ?? '')
})

const splitArray = (value: unknown): string[] => {
  if (Array.isArray(value)) return value
  return String(value ?? '')
    .split(',')
    .map((item: string) => item.trim())
    .filter(Boolean)
}
</script>

<style lang="less" scoped>
.form-label {
  display: block;
  margin-bottom: 6px;
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  line-height: 1.4;

  &.required::before {
    content: '*';
    color: var(--td-error-color);
    margin-right: 4px;
    font-weight: 500;
    line-height: 1;
  }
}

.form-desc {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
}
</style>
