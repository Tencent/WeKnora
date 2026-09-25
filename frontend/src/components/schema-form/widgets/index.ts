import type { Component } from 'vue'

import type { ConfigSchema } from '../schema'
import HeadersWidget from './HeadersWidget.vue'
import NumberWidget from './NumberWidget.vue'
import PasswordWidget from './PasswordWidget.vue'
import SelectWidget from './SelectWidget.vue'
import SwitchWidget from './SwitchWidget.vue'
import TextWidget from './TextWidget.vue'
import TextareaWidget from './TextareaWidget.vue'

/**
 * Form controls by x-widget name. Only the frontend registers widgets;
 * plugins pick one by name, and anything richer belongs in a plugin page.
 */
const widgets = new Map<string, Component>([
  ['text', TextWidget],
  ['password', PasswordWidget],
  ['textarea', TextareaWidget],
  ['number', NumberWidget],
  ['select', SelectWidget],
  ['switch', SwitchWidget],
  // "Name: Value" lines edited as rows (RSS auth headers).
  ['headers', HeadersWidget],
])

export function registerWidget(name: string, component: Component) {
  widgets.set(name, component)
}

/** The widget name for a field: x-widget when known, else inferred. */
export function widgetName(schema: ConfigSchema): string {
  const explicit = schema['x-widget']
  if (explicit && widgets.has(explicit)) return explicit
  if (schema['x-secret']) return 'password'
  if (schema.oneOf?.length || schema.enum?.length) return 'select'
  if (schema.type === 'boolean') return 'switch'
  if (schema.type === 'number' || schema.type === 'integer') return 'number'
  return 'text'
}

export function resolveWidget(schema: ConfigSchema): Component {
  return widgets.get(widgetName(schema)) ?? TextWidget
}
