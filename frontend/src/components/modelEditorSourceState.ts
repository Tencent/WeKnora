export type ModelEditorSource = 'local' | 'remote'

export type ModelEditorType = 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr'

export function shouldShowOllamaUnavailableTip(
  source: ModelEditorSource,
  modelType: ModelEditorType,
  ollamaServiceStatus: boolean | null,
): boolean {
  return source === 'local' && modelType !== 'rerank' && ollamaServiceStatus === false
}
