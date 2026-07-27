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
  // Feedback-driven recall policy (#1248). All fields optional with
  // backend-supplied defaults so an older client still gets a complete
  // config back; saving omits zero-valued fields so the server-side
  // defaults remain the source of truth.
  feedback_ranking_enabled?: boolean
  feedback_boost_threshold?: number
  feedback_penalty_threshold?: number
  feedback_boost_factor?: number
  feedback_penalty_factor?: number
  feedback_min_samples?: number
  feedback_needs_optimization_threshold?: number
}

// Feedback policy defaults (#1248). Mirrors the server-side defaults so the
// UI's slider / input widgets initialise to the same values a fresh tenant
// receives; the backend is still the source of truth for the actual
// validation envelope.
export const FEEDBACK_POLICY_DEFAULTS = {
  feedback_ranking_enabled: false,
  feedback_boost_threshold: 0.8,
  feedback_penalty_threshold: 0.5,
  feedback_boost_factor: 1.2,
  feedback_penalty_factor: 0.8,
  feedback_min_samples: 3,
  feedback_needs_optimization_threshold: 0.2,
} as const;

// Get tenant retrieval config via KV API
export function getTenantRetrievalConfig() {
  return get('/api/v1/tenants/kv/retrieval-config')
}

// Update tenant retrieval config via KV API
export function updateTenantRetrievalConfig(config: RetrievalConfig) {
  return put('/api/v1/tenants/kv/retrieval-config', config)
}
