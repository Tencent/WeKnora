export const TRAINING_ORCHESTRATION_SCHEMA_VERSION = 'training-orchestration/v1' as const

export function assertSupportedTrainingProjection(value: unknown): asserts value is { schema_version: typeof TRAINING_ORCHESTRATION_SCHEMA_VERSION } {
  if (!value || typeof value !== 'object' || !('schema_version' in value)) {
    throw new Error('培训学习路径结果缺少版本信息')
  }
  if (value.schema_version !== TRAINING_ORCHESTRATION_SCHEMA_VERSION) {
    throw new Error('当前培训学习路径版本暂不受支持，请刷新后重试')
  }
  const projection = value as Record<string, unknown>
  const clusters = projection.topic_clusters
  if (clusters === undefined) return
  if (!Array.isArray(clusters)) throw new Error('培训学习路径结果缺少主题簇')
  for (const cluster of clusters) {
    const gap = cluster && typeof cluster === 'object' ? (cluster as Record<string, unknown>).gap_analysis : null
    if (!gap || typeof gap !== 'object') throw new Error('培训学习路径结果缺少缺口分析')
    const item = gap as Record<string, unknown>
    if (item.status !== 'partial' && item.status !== 'sufficient') throw new Error('培训学习路径缺口分析状态无效')
    if (typeof item.current_depth_summary !== 'string' || typeof item.supplement_direction_summary !== 'string') throw new Error('培训学习路径缺口分析摘要无效')
    const dimensions = item.dimensions
    if (!dimensions || typeof dimensions !== 'object') throw new Error('培训学习路径缺口分析维度缺失')
    for (const key of ['knowledge_coverage', 'workplace_application', 'independent_task_completion']) {
      const dimension = (dimensions as Record<string, unknown>)[key]
      if (!dimension || typeof dimension !== 'object' || typeof (dimension as Record<string, unknown>).gap !== 'string' || typeof (dimension as Record<string, unknown>).recommendation !== 'string') {
        throw new Error('培训学习路径缺口分析维度无效')
      }
    }
  }
}
