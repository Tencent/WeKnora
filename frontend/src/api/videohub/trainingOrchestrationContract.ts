export const TRAINING_ORCHESTRATION_SCHEMA_VERSION = 'training-orchestration/v1' as const

export function assertSupportedTrainingProjection(value: unknown): asserts value is { schema_version: typeof TRAINING_ORCHESTRATION_SCHEMA_VERSION } {
  if (!value || typeof value !== 'object' || !('schema_version' in value)) {
    throw new Error('培训学习路径结果缺少版本信息')
  }
  if (value.schema_version !== TRAINING_ORCHESTRATION_SCHEMA_VERSION) {
    throw new Error('当前培训学习路径版本暂不受支持，请刷新后重试')
  }
}
