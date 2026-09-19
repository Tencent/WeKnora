// 插件 config field 值的序列化 / 反序列化工具。
// ExtraConfig 在后端以 map[string]string 持久化，但表单绑定 typed 值
// （boolean / number / array），保存时序列化为字符串，加载时反序列化。
// 供 model / search / parser / retriever 各扩展点复用。

export interface ConfigFieldLike {
  type: string
}

// deserializeConfigFieldValue 把后端持久化的字符串值还原为表单 typed 值。
export function deserializeConfigFieldValue(field: ConfigFieldLike | undefined, raw: unknown): any {
  if (raw === undefined || raw === null) return ''
  if (!field) return raw
  if (field.type === 'boolean') return raw === true || raw === 'true'
  if (field.type === 'number') {
    const parsed = Number(raw)
    return Number.isNaN(parsed) ? raw : parsed
  }
  if (field.type === 'array') {
    return Array.isArray(raw)
      ? raw
      : String(raw).split(',').map((s: string) => s.trim()).filter(Boolean)
  }
  return raw
}

// serializeConfigFieldValue 把表单 typed 值序列化为后端持久化的字符串。
export function serializeConfigFieldValue(value: unknown): string {
  return String(value ?? '')
}

// isConfigFieldValueEmpty 判断必填字段是否为空。正确处理 boolean=false 与
// number=0 的 falsy 误判。
export function isConfigFieldValueEmpty(field: ConfigFieldLike, value: unknown): boolean {
  if (field.type === 'boolean') return false
  if (field.type === 'number') {
    return value === undefined || value === null || value === '' || Number.isNaN(Number(value))
  }
  if (field.type === 'array') {
    return Array.isArray(value) ? value.length === 0 : String(value ?? '').trim() === ''
  }
  return value === undefined || value === null || String(value).trim() === ''
}
