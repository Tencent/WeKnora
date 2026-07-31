import { get, post, put, del } from '../../utils/request';

export type ChunkFeedbackRating = 'like' | 'dislike';

// 对某条助手回复提交点赞/点踩（重复提交会更新原因，切换评价会自动换绑）
export async function submitAnswerFeedback(
  session_id: string,
  message_id: string,
  rating: ChunkFeedbackRating,
  reason = '',
) {
  return post(`/api/v1/messages/${session_id}/${message_id}/feedback`, {
    rating,
    reason,
  });
}

// 取消对某条助手回复的评价
export async function cancelAnswerFeedback(session_id: string, message_id: string) {
  return del(`/api/v1/messages/${session_id}/${message_id}/feedback`);
}

// ---------- 后台：片段反馈统计 / 权重管理 ----------

export interface ChunkFeedbackStat {
  chunk_id: string;
  chunk_seq_id: number;
  knowledge_id: string;
  knowledge_base_id: string;
  knowledge_title: string;
  knowledge_filename: string;
  chunk_index: number;
  content_preview: string;
  chunk_type: string;
  like_count: number;
  dislike_count: number;
  approval_rate: number;
  recall_weight: number;
  needs_optimization: boolean;
  session_count: number;
  feedback_count: number;
  updated_at: string;
}

export interface DislikeReasonStat {
  reason: string;
  count: number;
}

export interface RelatedSessionStat {
  session_id: string;
  title: string;
  message_count: number;
  last_active_at?: string;
}

export interface ChunkFeedbackDetail extends ChunkFeedbackStat {
  dislike_reasons: DislikeReasonStat[];
  related_sessions: RelatedSessionStat[];
}

export interface ChunkWeightLog {
  id: string;
  tenant_id: number;
  chunk_id: string;
  knowledge_base_id: string;
  old_weight: number;
  new_weight: number;
  source: 'feedback' | 'manual_reset' | 'manual_adjust';
  message_id: string;
  user_id: string;
  reason: string;
  created_at: string;
}

export interface ChunkFeedbackConfig {
  tenant_id?: number;
  boost_threshold: number;
  degrade_threshold: number;
  optimize_threshold: number;
  min_votes: number;
  weight_step: number;
  max_weight: number;
  min_weight: number;
  updated_at?: string;
}

export interface FeedbackStatsQuery {
  knowledge_base_id?: string;
  knowledge_id?: string;
  min_approval_rate?: number;
  max_approval_rate?: number;
  needs_optimization?: boolean;
  keyword?: string;
  sort_by?: string;
  sort_order?: 'asc' | 'desc';
  page?: number;
  page_size?: number;
}

export async function getChunkFeedbackStats(params: FeedbackStatsQuery) {
  const query = new URLSearchParams();
  if (params.knowledge_base_id) query.set('knowledge_base_id', params.knowledge_base_id);
  if (params.knowledge_id) query.set('knowledge_id', params.knowledge_id);
  if (params.min_approval_rate !== undefined) query.set('min_approval_rate', String(params.min_approval_rate));
  if (params.max_approval_rate !== undefined) query.set('max_approval_rate', String(params.max_approval_rate));
  if (params.needs_optimization !== undefined) query.set('needs_optimization', String(params.needs_optimization));
  if (params.keyword) query.set('keyword', params.keyword);
  if (params.sort_by) query.set('sort_by', params.sort_by);
  if (params.sort_order) query.set('sort_order', params.sort_order);
  query.set('page', String(params.page || 1));
  query.set('page_size', String(params.page_size || 20));
  return get(`/api/v1/knowledge-bases/chunk-feedback/stats?${query.toString()}`);
}

export async function getChunkFeedbackDetail(chunk_id: string) {
  return get(`/api/v1/knowledge-bases/chunk-feedback/stats/${chunk_id}`);
}

export async function getChunkWeightLogs(params: {
  chunk_id?: string;
  source?: string;
  page?: number;
  page_size?: number;
}) {
  const query = new URLSearchParams();
  if (params.chunk_id) query.set('chunk_id', params.chunk_id);
  if (params.source) query.set('source', params.source);
  query.set('page', String(params.page || 1));
  query.set('page_size', String(params.page_size || 20));
  return get(`/api/v1/knowledge-bases/chunk-feedback/weight-logs?${query.toString()}`);
}

export async function resetChunkFeedback(chunk_ids: string[]) {
  return post('/api/v1/knowledge-bases/chunk-feedback/reset', { chunk_ids });
}

export async function getChunkFeedbackConfig() {
  return get('/api/v1/knowledge-bases/chunk-feedback/config');
}

export async function updateChunkFeedbackConfig(cfg: Partial<ChunkFeedbackConfig>) {
  return put('/api/v1/knowledge-bases/chunk-feedback/config', cfg);
}