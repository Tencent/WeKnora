<template>
  <template v-for="field in fields" :key="field.key">
    <!-- Nested objects render their fields inline, in their own order. -->
    <SchemaFormFields
      v-if="field.schema.type === 'object'"
      :schema="field.schema"
      :value="childObject(field.key)"
      :path="join(field.key)"
      :secret-mode="secretMode"
      :errors="errors"
      :disabled="disabled"
      :context="context"
      @update:value="set(field.key, $event)"
    >
      <template #secret="slotProps"><slot name="secret" v-bind="slotProps" /></template>
    </SchemaFormFields>
    <div v-else class="form-item" :data-field="join(field.key)">
      <label class="form-label" :class="{ required: field.required }">{{ text(field.schema, 'title') || field.key }}</label>
      <slot
        v-if="field.schema['x-secret'] && secretMode === 'slot'"
        name="secret"
        :path="join(field.key)"
        :schema="field.schema"
        :label="text(field.schema, 'title') || field.key"
        :required="field.required"
      />
      <component
        :is="resolveWidget(field.schema)"
        v-else
        :model-value="value[field.key]"
        :path="join(field.key)"
        :schema="field.schema"
        :placeholder="text(field.schema, 'placeholder')"
        :disabled="disabled"
        :required="field.required"
        @update:model-value="set(field.key, $event)"
      />
      <p v-if="errorFor(field.key)" class="form-error">{{ errorFor(field.key) }}</p>
      <p v-else-if="text(field.schema, 'description')" class="form-desc">{{ text(field.schema, 'description') }}</p>
    </div>
  </template>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import {
  HIDDEN_WIDGET,
  inGroup,
  isPlainObject,
  isVisible,
  orderedFields,
  type ConfigSchema,
  type ConfigValue,
  type FieldError,
  type SecretSlotProps,
} from './schema'
import { useSchemaText } from './useSchemaText'
import { resolveWidget } from './widgets'

const props = defineProps<{
  schema: ConfigSchema
  value: ConfigValue
  /** Dotted path of this level; '' for the root. */
  path: string
  /** Only fields of this x-group are shown at the root level. */
  group?: string
  secretMode: 'input' | 'slot'
  errors?: FieldError[]
  disabled?: boolean
  /** Values "$"-prefixed x-visible-if keys read. */
  context?: ConfigValue
}>()

const emit = defineEmits<{ 'update:value': [value: ConfigValue] }>()

defineSlots<{ secret?: (props: SecretSlotProps) => unknown }>()

const { t, te } = useI18n()
const text = useSchemaText()

const fields = computed(() =>
  orderedFields(props.schema).filter(
    f =>
      f.schema['x-widget'] !== HIDDEN_WIDGET &&
      isVisible(f.schema, props.value, props.context) &&
      (props.path !== '' || inGroup(f.schema, props.group)),
  ),
)

function join(key: string) {
  return props.path ? `${props.path}.${key}` : key
}

function childObject(key: string): ConfigValue {
  const v = props.value[key]
  return isPlainObject(v) ? v : {}
}

function set(key: string, v: unknown) {
  const next: ConfigValue = { ...props.value }
  if (v === undefined) delete next[key]
  else next[key] = v
  emit('update:value', next)
}

function errorFor(key: string): string {
  const e = props.errors?.find(err => err.path === join(key))
  if (!e) return ''
  const k = `schemaForm.errors.${e.code}`
  return te(k) ? t(k) : e.message || e.code
}
</script>
