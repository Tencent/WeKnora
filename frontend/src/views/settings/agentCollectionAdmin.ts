import type { AgentCollectionField } from '@/api/agent'
import type { CollectionFilter } from '@/api/system/agent-collection'

export function cleanCollectionFilter(filter: CollectionFilter): CollectionFilter {
  return Object.fromEntries(Object.entries(filter).filter(([, value]) =>
    value !== '' && value !== undefined && value !== null,
  )) as CollectionFilter
}

export function collectionDateRange(from: string, to: string) {
  return {
    updated_from: from ? `${from}T00:00:00Z` : undefined,
    updated_to: to ? `${to}T23:59:59Z` : undefined,
  }
}

export function orderedCollectionFields(fields: AgentCollectionField[]) {
  return [...fields].sort((left, right) => left.order - right.order)
}

export function displayCollectionValue(value: unknown): string {
  if (value == null || value === '') return '未填写'
  if (Array.isArray(value)) return value.join('、')
  return typeof value === 'object' ? JSON.stringify(value) : String(value)
}

export function collectionCompletionLabel(complete: boolean) {
  return complete ? '已完成' : '待补充'
}

export function collectionExportFilename(filename: string | undefined, format: 'csv' | 'xlsx', date = new Date()) {
  return filename?.trim() || `agent-collection-${date.toISOString().slice(0, 10)}.${format}`
}
