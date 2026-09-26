<template>
  <div class="schema-form">
    <SchemaFormFields
      :schema="schema"
      :value="modelValue ?? {}"
      path=""
      :group="group"
      :secret-mode="secretMode"
      :errors="errors"
      :disabled="disabled"
      :context="context"
      @update:value="$emit('update:modelValue', $event)"
    >
      <template #secret="slotProps"><slot name="secret" v-bind="slotProps" /></template>
    </SchemaFormFields>
  </div>
</template>

<script setup lang="ts">
import type { ConfigSchema, ConfigValue, FieldError, SecretSlotProps } from './schema'
import SchemaFormFields from './SchemaFormFields.vue'

/**
 * Renders a config schema (internal/plugin/configschema) as form fields.
 *
 * The root uses display: contents, so each field is a direct child of the
 * surrounding drawer section and picks up its spacing. Pass `group` to render
 * one x-group per section. With secretMode "slot", secret fields render the
 * `secret` slot instead of an input, for forms whose secrets are edited
 * through a /credentials subresource.
 */
withDefaults(
  defineProps<{
    schema: ConfigSchema
    modelValue: ConfigValue | undefined
    group?: string
    secretMode?: 'input' | 'slot'
    errors?: FieldError[]
    disabled?: boolean
    /** Values "$"-prefixed x-visible-if keys read, e.g. { mode } for IM credentials. */
    context?: ConfigValue
  }>(),
  { group: undefined, secretMode: 'input', errors: () => [], disabled: false, context: undefined },
)

defineEmits<{ 'update:modelValue': [value: ConfigValue] }>()

defineSlots<{ secret?: (props: SecretSlotProps) => unknown }>()
</script>

<style lang="less" scoped>
.schema-form {
  display: contents;
}

// Same drawer form conventions as ModelEditorDialog / WebSearchSettings.
:deep(.form-item) {
  margin-bottom: 0;
}

:deep(.form-label) {
  display: block;
  margin-bottom: 6px;
  font-size: var(--app-text-md);
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

:deep(.form-desc),
:deep(.form-error) {
  margin: 4px 0 0;
  font-size: var(--app-text-sm);
  line-height: 1.5;
}

:deep(.form-desc) {
  color: var(--td-text-color-placeholder);
}

:deep(.form-error) {
  color: var(--td-error-color);
}

:deep(.t-input),
:deep(.t-select),
:deep(.t-textarea),
:deep(.t-input-number) {
  width: 100%;
  font-size: var(--app-text-md);
}
</style>
