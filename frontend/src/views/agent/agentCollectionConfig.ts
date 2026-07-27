import type { AgentCollectionField, CustomAgentConfig } from '@/api/agent'

const KEY_PATTERN = /^[a-z][a-z0-9_]{0,63}$/
const SENSITIVE_PATTERN = /(password|passwd|secret|token|api[_-]?key|credential|密码|密钥|令牌)/i
const CHOICE_TYPES = new Set(['single_choice', 'multiple_choice'])

export function ensureCollectionDefaults(config: CustomAgentConfig): CustomAgentConfig {
  config.collection_enabled ??= false
  config.collection_goal ??= ''
  config.collection_schema_version ??= 1
  config.collection_extract_from_messages ??= true
  config.collection_extraction_threshold ??= 0.85
  config.collection_collect_optional_during_intake ??= false
  config.collection_fields ??= []
  normalizeCollectionFields(config.collection_fields)
  return config
}

export function normalizeCollectionFields(fields: AgentCollectionField[]): AgentCollectionField[] {
  fields.forEach((field, index) => {
    field.order = index
    field.enabled ??= true
    field.required ??= false
    field.validation ??= {}
    if (CHOICE_TYPES.has(field.type)) field.options ??= []
  })
  return fields
}

export function nextCollectionFieldKey(fields: AgentCollectionField[]): string {
  const used = new Set(fields.map((field) => field.key))
  let index = fields.length + 1
  while (used.has(`field_${index}`)) index += 1
  return `field_${index}`
}

export function createCollectionField(
  fields: AgentCollectionField[],
  order: number,
): AgentCollectionField {
  return {
    key: nextCollectionFieldKey(fields),
    label: '',
    type: 'short_text',
    required: false,
    enabled: true,
    order,
    validation: {},
  }
}

export function validateCollectionConfig(config: CustomAgentConfig): string[] {
  if (!config.collection_enabled) return []
  const errors: string[] = []
  const fields = config.collection_fields ?? []
  if (!config.collection_goal?.trim()) errors.push('请填写采集目标')
  if (fields.filter((field) => field.enabled).length > 100) errors.push('启用字段不能超过 100 个')
  const seen = new Set<string>()
  fields.forEach((field, index) => validateField(field, index, seen, errors))
  return errors
}

function validateField(
  field: AgentCollectionField,
  index: number,
  seen: Set<string>,
  errors: string[],
) {
  const name = field.label?.trim() || `第 ${index + 1} 个字段`
  if (!field.label?.trim()) errors.push(`${name}缺少名称`)
  if (!KEY_PATTERN.test(field.key || '')) errors.push(`${name}的字段标识格式不正确`)
  if (SENSITIVE_PATTERN.test(`${field.key} ${field.label} ${field.description ?? ''}`)) errors.push(`${name}不能用于采集密码、令牌或密钥`)
  if (seen.has(field.key)) errors.push(`${name}的字段标识重复`)
  if (CHOICE_TYPES.has(field.type)) validateOptions(field, name, errors)
  validateBounds(field, name, errors)
  const condition = field.visible_when
  if (condition && !seen.has(condition.field)) errors.push(`${name}的显示条件只能引用前面的字段`)
  seen.add(field.key)
}

function validateOptions(field: AgentCollectionField, name: string, errors: string[]) {
  const options = field.options ?? []
  if (options.length < 2 || options.length > 50) errors.push(`${name}需要 2 到 50 个选项`)
  const ids = new Set<string>()
  options.forEach((option) => {
    if (!KEY_PATTERN.test(option.id) || !option.label.trim()) errors.push(`${name}包含无效选项`)
    if (ids.has(option.id)) errors.push(`${name}的选项标识重复`)
    ids.add(option.id)
  })
}

function validateBounds(field: AgentCollectionField, name: string, errors: string[]) {
  const value = field.validation ?? {}
  if (value.min_length != null && value.max_length != null && value.min_length > value.max_length) {
    errors.push(`${name}的最小长度不能大于最大长度`)
  }
  if (value.min_number != null && value.max_number != null && value.min_number > value.max_number) {
    errors.push(`${name}的最小值不能大于最大值`)
  }
  if (value.min_date && value.max_date && value.min_date > value.max_date) {
    errors.push(`${name}的最早日期不能晚于最晚日期`)
  }
}
