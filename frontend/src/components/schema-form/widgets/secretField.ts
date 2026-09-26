import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { isStoredSecret, secretInputText, secretInputValue } from '../schema'
import type { WidgetProps } from './types'

/**
 * A text-like widget's value, text and placeholder for a field that may hold
 * a stored secret ("***"): the field shows empty with a "saved" hint, typing
 * replaces the secret, and emptying the field again keeps it.
 */
export function useSecretField(props: WidgetProps, emit: (value: string) => void) {
  const { t } = useI18n()
  const stored = computed(() => isStoredSecret(props.schema, props.modelValue))
  // Once the field held a stored secret, emptying it keeps that secret.
  const hadStored = ref(false)
  watch(stored, (v) => {
    if (v) hadStored.value = true
  }, { immediate: true })
  return {
    text: computed(() => secretInputText(props.schema, props.modelValue)),
    placeholder: computed(() => (stored.value ? t('schemaForm.secretStored') : props.placeholder)),
    update: (text: string) => emit(secretInputValue(text, hadStored.value)),
  }
}
