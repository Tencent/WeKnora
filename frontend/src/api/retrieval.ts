import { get, put } from '@/utils/request'

// RetrievalConfig represents the global retrieval/search configuration for a tenant.
// Shared by knowledge search and message search.
export interface RetrievalConfig {
  embedding_top_k: number
  vector_threshold: number
  keyword_threshold: number
  rerank_top_k: number
  rerank_threshold: number
  rerank_model_id: string
  // Feedback-based ranking: chunk recall weights derived from answer
  // like/dislike feedback. Weights are always maintained server-side;
  // the switch only controls whether they are applied at retrieval time.
  feedback_ranking_enabled?: boolean
  feedback_boost_threshold?: number
  feedback_penalty_threshold?: number
  feedback_boost_factor?: number
  feedback_penalty_factor?: number
  feedback_min_samples?: number
  feedback_needs_optimization_threshold?: number
}

// Get tenant retrieval config via KV API
export function getTenantRetrievalConfig() {
  return get('/api/v1/tenants/kv/retrieval-config')
}

// Update tenant retrieval config via KV API
export function updateTenantRetrievalConfig(config: RetrievalConfig) {
  return put('/api/v1/tenants/kv/retrieval-config', config)
}
