import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { resolveText, type ConfigSchema, type TextSlot, type Translator } from './schema'

/** Binds resolveText to the active vue-i18n locale. */
export function useSchemaText() {
  const { t, te, locale } = useI18n()
  const tr = computed<Translator>(() => ({
    t: key => t(key),
    te: key => te(key),
    locale: locale.value,
  }))
  return (schema: ConfigSchema, slot: TextSlot) => resolveText(schema, slot, tr.value)
}
