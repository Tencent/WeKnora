import type { CreateEvaluationRequest } from '../../api/evaluation/datasets'

export interface EvaluationCreationDraft {
  datasetId: string
  versionId: string
  knowledgeBaseId: string
  chatId: string
  rerankId: string
  topK: string | number
  seed: string | number
}
export function buildEvaluationCreation(draft: EvaluationCreationDraft): CreateEvaluationRequest {
  if (!draft.datasetId || !draft.versionId || !draft.knowledgeBaseId || !draft.chatId) throw new Error('required')
  const request: CreateEvaluationRequest = {
    dataset_id: draft.datasetId, dataset_version_id: draft.versionId,
    knowledge_base_id: draft.knowledgeBaseId, chat_id: draft.chatId,
  }
  if (draft.rerankId) request.rerank_id = draft.rerankId
  // Vue's native number inputs emit numbers when populated and '' when cleared.
  const seed = String(draft.seed).trim(), topK = String(draft.topK).trim()
  if (seed) {
    if (!/^-?\d+$/.test(seed) || !Number.isSafeInteger(Number(seed))) throw new Error('seed')
    request.seed = Number(seed)
  }
  if (topK) {
    const k = Number(topK)
    if (!/^\d+$/.test(topK) || !Number.isSafeInteger(k) || k < 1 || k > 100) throw new Error('topK')
    request.configuration = { retrieval: { embedding_top_k: k } }
  }
  return request
}
