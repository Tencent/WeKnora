import type { ConfigSchema } from '../schema'

/** Props every widget receives. Widgets emit update:modelValue. */
export interface WidgetProps {
  modelValue: unknown
  /** Dotted path of the field in its form. */
  path?: string
  schema: ConfigSchema
  placeholder?: string
  disabled?: boolean
  required?: boolean
}
