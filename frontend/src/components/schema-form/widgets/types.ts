import type { ConfigSchema } from '../schema'

/** Props every widget receives. Widgets emit update:modelValue. */
export interface WidgetProps {
  modelValue: unknown
  schema: ConfigSchema
  placeholder?: string
  disabled?: boolean
  required?: boolean
}
