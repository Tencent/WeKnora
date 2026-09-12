export interface EvaluationTaskFilters {
  status?: number
  datasetId?: string
  datasetVersionId?: string
  modelId?: string
  startedFrom?: string
  startedTo?: string
  labels?: string[]
  pageSize?: number
  cursor?: string
}

export function buildEvaluationTaskQuery(filters: EvaluationTaskFilters): URLSearchParams {
  const query = new URLSearchParams()
  if (filters.status !== undefined) query.set('status', String(filters.status))
  if (filters.datasetId?.trim()) query.set('dataset_id', filters.datasetId.trim())
  if (filters.datasetVersionId?.trim()) query.set('dataset_version_id', filters.datasetVersionId.trim())
  if (filters.modelId?.trim()) query.set('model_id', filters.modelId.trim())
  if (filters.startedFrom) query.set('started_from', filters.startedFrom)
  if (filters.startedTo) query.set('started_to', filters.startedTo)
  for (const label of filters.labels ?? []) {
    if (label.trim()) query.append('label', label.trim().toLocaleLowerCase())
  }
  if (filters.pageSize) query.set('page_size', String(filters.pageSize))
  if (filters.cursor) query.set('cursor', filters.cursor)
  return query
}

export function normalizeEvaluationComparisonSelection(taskIds: string[]): string[] {
  const seen = new Set<string>()
  const normalized: string[] = []
  for (const raw of taskIds) {
    const taskId = raw.trim()
    if (!taskId || seen.has(taskId)) continue
    seen.add(taskId)
    normalized.push(taskId)
  }
  if (normalized.length > 10) {
    throw new Error('Select at most 10 evaluation tasks')
  }
  return normalized
}
