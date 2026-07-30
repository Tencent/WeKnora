import { get, post, put } from '@/utils/request';

// Feedback type constants
export type FeedbackType = 'like' | 'dislike' | 'unlike' | 'undislike';

// Dislike reason constants
export type DislikeReason = 'inaccurate' | 'incomplete' | 'irrelevant' | 'other';

export interface SubmitFeedbackRequest {
  session_id: string;
  message_id: string;
  feedback_type: FeedbackType;
  dislike_reason?: DislikeReason;
  dislike_reason_detail?: string;
}

export interface ChunkStats {
  like_count: number;
  dislike_count: number;
  like_rate: number;
  recall_weight: number;
  is_pending_optimization: boolean;
  related_session_count: number;
}

export interface ChunkStatsDetail extends ChunkStats {
  dislike_reason_stats: Record<string, number>;
}

export interface ChunkWeightLog {
  id: string;
  chunk_id: string;
  knowledge_base_id: string;
  trigger_type: 'user_feedback' | 'auto_adjust' | 'manual_reset' | 'batch_update';
  trigger_reason?: string;
  old_weight: number;
  new_weight: number;
  old_like_rate?: number;
  new_like_rate?: number;
  feedback_id?: string;
  operator_id?: string;
  created_at: string;
}

export interface ChunkFeedbackSummary {
  total_chunks: number;
  total_feedbacks: number;
  total_likes: number;
  total_dislikes: number;
  average_like_rate: number;
  pending_optimization_count: number;
}

export interface ListChunksByStatsParams {
  keyword?: string;
  min_like_rate?: number;
  max_like_rate?: number;
  pending_optimization?: boolean;
  sort_by?: 'like_count' | 'dislike_count' | 'like_rate' | 'recall_weight';
  sort_order?: 'asc' | 'desc';
  page?: number;
  page_size?: number;
}

export interface ListChunksResponse {
  items: any[];
  total: number;
  page: number;
  page_size: number;
}

export interface ChunkWeightLogsResponse {
  items: ChunkWeightLog[];
  total: number;
}

/**
 * Submit user feedback for a chat message
 */
export async function submitFeedback(data: SubmitFeedbackRequest): Promise<void> {
  await post('/api/v1/feedback/chunk', data);
}

/**
 * Get statistics for a specific chunk
 */
export async function getChunkStats(chunkId: string): Promise<ChunkStatsDetail> {
  return get(`/api/v1/chunks/${chunkId}/stats`);
}

/**
 * List chunks with statistics filtering
 */
export async function listChunksByStats(
  kbId: string,
  params: ListChunksByStatsParams = {}
): Promise<ListChunksResponse> {
  const query = new URLSearchParams();
  if (params.keyword) query.set('keyword', params.keyword);
  if (params.min_like_rate !== undefined) query.set('min_like_rate', String(params.min_like_rate));
  if (params.max_like_rate !== undefined) query.set('max_like_rate', String(params.max_like_rate));
  if (params.pending_optimization !== undefined) query.set('pending_optimization', String(params.pending_optimization));
  if (params.sort_by) query.set('sort_by', params.sort_by);
  if (params.sort_order) query.set('sort_order', params.sort_order);
  if (params.page) query.set('page', String(params.page));
  if (params.page_size) query.set('page_size', String(params.page_size));

  const qs = query.toString();
  return get(`/api/v1/knowledge-bases/${kbId}/chunks/stats${qs ? `?${qs}` : ''}`);
}

/**
 * Get weight change logs for a chunk
 */
export async function getChunkWeightLogs(
  chunkId: string,
  params: { limit?: number; offset?: number } = {}
): Promise<ChunkWeightLogsResponse> {
  const query = new URLSearchParams();
  if (params.limit) query.set('limit', String(params.limit));
  if (params.offset) query.set('offset', String(params.offset));

  const qs = query.toString();
  return get(`/api/v1/chunks/${chunkId}/weight-logs${qs ? `?${qs}` : ''}`);
}

/**
 * Reset feedback data for a chunk (admin only)
 */
export async function resetChunkFeedback(chunkId: string): Promise<void> {
  await post('/api/v1/chunks/feedback/reset', { chunk_id: chunkId });
}

/**
 * Get feedback summary for a knowledge base
 */
export async function getFeedbackSummary(kbId: string): Promise<ChunkFeedbackSummary> {
  return get(`/api/v1/knowledge-bases/${kbId}/feedback-summary`);
}

/**
 * Batch adjust weights for all chunks in a knowledge base (admin only)
 */
export async function batchAdjustWeights(kbId: string): Promise<void> {
  await post(`/api/v1/knowledge-bases/${kbId}/chunks/batch-adjust-weights`, {});
}
