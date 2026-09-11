const trainingJobErrorMessages: Record<string, string> = {
  source_not_stable: '视频仍在处理中，请等待内容稳定后重试。',
  source_changed: '生成期间视频内容发生变化，请等待处理完成后重试。',
  model_output_truncated: 'AI 返回内容不完整，系统未发布结果，请重试。',
  model_output_invalid: 'AI 返回格式异常，系统未发布结果，请重试。',
  timeout: '本次生成耗时过长，系统未发布结果，请重试。',
  evidence_retrieval_failed: '部分学习内容暂时无法召回，系统未发布结果，请稍后重试。',
  invalid_reference: 'AI 引用了当前视频范围外的内容，系统未发布结果。',
}

export function trainingJobErrorMessage(job: { error_code?: string }): string {
  return trainingJobErrorMessages[job.error_code || ''] || '学习路径生成失败，当前结果未受影响，请重试。'
}

export function trainingJobWarningMessage(job: { warning_code?: string; warning_message?: string }): string {
  if (job.warning_code === 'evidence_retrieval_degraded') {
    return job.warning_message || '证据召回降级，已使用规划证据完成生成。'
  }
  return ''
}
