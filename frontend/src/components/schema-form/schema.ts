// Config schemas as the backend serves them (internal/plugin/configschema):
// a JSON Schema subset plus x- keywords for the form. This module is the
// pure part of <SchemaForm>: ordering, visibility, text lookup, defaults and
// validation, kept free of Vue so it runs under node:test.

export type SchemaType = 'object' | 'string' | 'number' | 'integer' | 'boolean' | 'array'

/** Text slots a field can localize. */
export type TextSlot = 'title' | 'description' | 'placeholder'

export interface ConfigSchema {
  type?: SchemaType
  title?: string
  description?: string
  properties?: Record<string, ConfigSchema>
  required?: string[]
  items?: ConfigSchema
  default?: unknown
  const?: unknown
  enum?: unknown[]
  oneOf?: ConfigSchema[]
  minLength?: number
  maxLength?: number
  pattern?: string
  minimum?: number
  maximum?: number
  format?: string
  'x-secret'?: boolean
  'x-widget'?: string
  'x-placeholder'?: string
  'x-i18n'?: Partial<Record<TextSlot, Record<string, string>>>
  'x-i18n-keys'?: Partial<Record<TextSlot, string>>
  'x-visible-if'?: Record<string, unknown>
  'x-group'?: string
  'x-order'?: number
  'x-model-types'?: string[]
  'x-options'?: OptionsSource
  'x-oauth'?: OAuthSpec
}

/** x-options: the field's choices come from the plugin. */
export interface OptionsSource {
  /** The plugin's options endpoint. */
  name: string
  /** Dotted paths of fields the choices depend on; they reload when those change. */
  dependsOn?: string[]
  /** Ask the plugin with what the user types. */
  search?: boolean
}

/** x-oauth: the field holds a connection WeKnora authorized. */
export interface OAuthSpec {
  authorizeUrl: string
  tokenUrl?: string
  scopes?: string[]
}

/** Prefix of an x-oauth field's value (same as configschema.OAuthRefPrefix). */
export const OAUTH_REF_PREFIX = 'oauth:'

export function isOAuthRef(v: unknown): v is string {
  return typeof v === 'string' && v.length > OAUTH_REF_PREFIX.length && v.startsWith(OAUTH_REF_PREFIX)
}

export type ConfigValue = Record<string, unknown>

/** Same codes as configschema.FieldError on the backend. */
export type FieldErrorCode =
  | 'required' | 'type' | 'enum' | 'min_length' | 'max_length'
  | 'pattern' | 'minimum' | 'maximum' | 'format'

export interface FieldError {
  path: string
  code: FieldErrorCode
  message?: string
}

/** x-widget value that keeps a declared field out of the form. */
export const HIDDEN_WIDGET = 'hidden'

/** Same value the backend sends for a set secret. */
export const REDACTED_SECRET = '***'

/** Props of the `secret` slot of <SchemaForm> in secretMode "slot". */
export interface SecretSlotProps {
  path: string
  schema: ConfigSchema
  label: string
  required: boolean
}

export interface SchemaField {
  key: string
  schema: ConfigSchema
  required: boolean
}

/** Properties in form order: x-order, then key. */
export function orderedFields(schema: ConfigSchema | undefined): SchemaField[] {
  const props = schema?.properties ?? {}
  const required = new Set(schema?.required ?? [])
  return Object.keys(props)
    .sort((a, b) => {
      const oa = props[a]['x-order'] ?? 0
      const ob = props[b]['x-order'] ?? 0
      return oa !== ob ? oa - ob : a < b ? -1 : a > b ? 1 : 0
    })
    .map(key => ({ key, schema: props[key], required: required.has(key) }))
}

/** The schema node at a dotted path ("auth.token"), or undefined. */
export function schemaAt(schema: ConfigSchema, path: string): ConfigSchema | undefined {
  let node: ConfigSchema | undefined = schema
  for (const key of path.split('.')) {
    node = node?.properties?.[key]
    if (!node) return undefined
  }
  return node
}

/**
 * x-visible-if: every listed sibling must equal the given value. Keys that
 * start with "$" ("$mode") are read from context instead: values of the object
 * the configuration lives in, such as an IM channel's mode.
 */
export function isVisible(
  schema: ConfigSchema,
  siblings: ConfigValue | undefined,
  context?: ConfigValue,
): boolean {
  const cond = schema['x-visible-if']
  if (!cond) return true
  return Object.entries(cond).every(([key, want]) => {
    const got = key.startsWith('$') ? context?.[key.slice(1)] : siblings?.[key]
    return looselyEqual(got, want)
  })
}

/** Whether a field belongs to a group filter; no filter shows everything. */
export function inGroup(schema: ConfigSchema, group: string | undefined): boolean {
  return group === undefined || (schema['x-group'] ?? '') === group
}

/** The choices a select can offer: oneOf entries, else enum values. */
export function choicesOf(schema: ConfigSchema): Array<{ value: unknown; schema: ConfigSchema }> {
  if (schema.oneOf?.length) return schema.oneOf.map(c => ({ value: c.const, schema: c }))
  return (schema.enum ?? []).map(v => ({ value: v, schema: { title: String(v) } }))
}

export interface Translator {
  t: (key: string) => string
  te: (key: string) => boolean
  locale: string
}

/**
 * Resolves a text slot: a frontend locale key when it exists, then the
 * schema's own translation for the locale (or its language), then the plain
 * text. Builtins lean on keys; plugins ship their text in x-i18n.
 */
export function resolveText(schema: ConfigSchema, slot: TextSlot, tr: Translator): string {
  const key = schema['x-i18n-keys']?.[slot]
  if (key && tr.te(key)) return tr.t(key)
  const table = schema['x-i18n']?.[slot]
  if (table) {
    if (table[tr.locale]) return table[tr.locale]
    const lang = tr.locale.split('-')[0]
    const match = Object.keys(table).sort().find(k => k.split('-')[0] === lang && table[k])
    if (match) return table[match]
  }
  if (slot === 'placeholder') return schema['x-placeholder'] ?? ''
  return schema[slot] ?? ''
}

/**
 * Fills defaults into a copy of value: missing scalars take their default and
 * nested objects are filled recursively. Existing values are never replaced.
 */
export function applyDefaults(schema: ConfigSchema, value: ConfigValue | undefined): ConfigValue {
  const out: ConfigValue = { ...(value ?? {}) }
  for (const { key, schema: prop } of orderedFields(schema)) {
    if (prop.type === 'object') {
      const child = out[key]
      out[key] = applyDefaults(prop, isPlainObject(child) ? child : undefined)
      continue
    }
    if ((out[key] === undefined || out[key] === '') && prop.default !== undefined) {
      out[key] = prop.default
    }
  }
  return out
}

/** Dotted paths of every secret field. */
export function secretPaths(schema: ConfigSchema, prefix = ''): string[] {
  const out: string[] = []
  for (const { key, schema: prop } of orderedFields(schema)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (prop['x-secret']) out.push(path)
    if (prop.type === 'object') out.push(...secretPaths(prop, path))
  }
  return out
}

export interface ValidateOptions {
  /** Skip secret fields entirely (they are edited elsewhere, e.g. /credentials). */
  skipSecrets?: boolean
  /** Values "$"-prefixed x-visible-if keys read (see isVisible). */
  context?: ConfigValue
}

/**
 * Mirrors configschema.Validate: required fields hidden by x-visible-if are
 * not required, undeclared keys are allowed, and a redacted secret counts as
 * present.
 */
export function validateConfig(
  schema: ConfigSchema,
  value: ConfigValue | undefined,
  opts: ValidateOptions = {},
): FieldError[] {
  const errors: FieldError[] = []
  validateObject(schema, value ?? {}, '', errors, opts)
  return errors
}

function validateObject(
  schema: ConfigSchema,
  value: ConfigValue,
  path: string,
  errors: FieldError[],
  opts: ValidateOptions,
) {
  for (const { key, schema: prop, required } of orderedFields(schema)) {
    if (!isVisible(prop, value, opts.context)) continue
    if (opts.skipSecrets && prop['x-secret']) continue
    const fieldPath = path ? `${path}.${key}` : key
    const v = value[key]
    if (v === undefined || v === null || v === '') {
      if (required) errors.push({ path: fieldPath, code: 'required' })
      continue
    }
    validateValue(prop, v, fieldPath, errors, opts)
  }
}

function validateValue(
  schema: ConfigSchema,
  v: unknown,
  path: string,
  errors: FieldError[],
  opts: ValidateOptions,
) {
  const add = (code: FieldErrorCode) => errors.push({ path, code })
  switch (schema.type) {
    case 'object':
      if (!isPlainObject(v)) return add('type')
      return validateObject(schema, v, path, errors, opts)
    case 'array':
      if (!Array.isArray(v)) return add('type')
      v.forEach((item, i) => schema.items && validateValue(schema.items, item, `${path}[${i}]`, errors, opts))
      return
    case 'string': {
      if (typeof v !== 'string') return add('type')
      if (schema['x-secret'] && v === REDACTED_SECRET) return
      if (schema['x-oauth']) {
        if (!isOAuthRef(v)) add('format')
        return
      }
      const n = [...v].length
      if (schema.minLength !== undefined && n < schema.minLength) add('min_length')
      if (schema.maxLength !== undefined && n > schema.maxLength) add('max_length')
      if (schema.pattern && !safeRegExp(schema.pattern)?.test(v)) add('pattern')
      if (schema.format === 'uri' && !isAbsoluteURL(v)) add('format')
      if (schema.format === 'email' && !/^[^\s@]+@[^\s@]+$/.test(v)) add('format')
      break
    }
    case 'number':
    case 'integer': {
      if (typeof v !== 'number' || Number.isNaN(v)) return add('type')
      if (schema.type === 'integer' && !Number.isInteger(v)) return add('type')
      if (schema.minimum !== undefined && v < schema.minimum) add('minimum')
      if (schema.maximum !== undefined && v > schema.maximum) add('maximum')
      break
    }
    case 'boolean':
      if (typeof v !== 'boolean') return add('type')
      break
  }
  // x-options choices are the plugin's to check.
  const choices = schema['x-options'] ? [] : choicesOf(schema)
  if (choices.length && !choices.some(c => looselyEqual(c.value, v))) add('enum')
}

function isAbsoluteURL(v: string): boolean {
  try {
    const u = new URL(v)
    return !!u.protocol && !!u.host
  } catch {
    return false
  }
}

function safeRegExp(pattern: string): RegExp | null {
  try {
    return new RegExp(pattern)
  } catch {
    return null
  }
}

export function isPlainObject(v: unknown): v is ConfigValue {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function looselyEqual(a: unknown, b: unknown): boolean {
  if (typeof a === 'number' || typeof b === 'number') return Number(a) === Number(b) && a !== '' && b !== ''
  if (isPlainObject(a) || Array.isArray(a)) return JSON.stringify(a) === JSON.stringify(b)
  return a === b
}
