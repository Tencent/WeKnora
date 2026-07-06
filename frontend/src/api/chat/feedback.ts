import { get, post, put } from "../../utils/request";

// Feedback type matches the backend types.FeedbackType
export type FeedbackType = "like" | "dislike" | "none";

export interface FeedbackRequest {
  session_id: string;
  message_id: string;
  feedback_type: FeedbackType;
  reason?: string;
  reason_detail?: string;
}

export interface MessageFeedback {
  id: string;
  tenant_id: number;
  user_id: string;
  session_id: string;
  message_id: string;
  feedback_type: FeedbackType;
  reason?: string;
  reason_detail?: string;
  created_at: string;
  updated_at: string;
}

export interface ChunkFeedbackStats {
  chunk_id: string;
  knowledge_id: string;
  knowledge_base_id: string;
  chunk_index: number;
  chunk_type: string;
  content_preview: string;
  like_count: number;
  dislike_count: number;
  approval_rate: number;
  recall_weight: number;
  needs_optimization: boolean;
  session_count: number;
  feedback_count: number;
  dislike_reasons?: { reason: string; count: number }[];
}

export interface ChunkWeightLog {
  id: string;
  tenant_id: number;
  chunk_id: string;
  old_weight: number;
  new_weight: number;
  old_approval_rate: number;
  new_approval_rate: number;
  old_like_count: number;
  new_like_count: number;
  old_dislike_count: number;
  new_dislike_count: number;
  trigger_type: string;
  trigger_detail?: string;
  created_at: string;
}

export interface FeedbackThresholds {
  boost_threshold: number;
  reduce_threshold: number;
  optimize_threshold: number;
  boost_weight: number;
  reduce_weight: number;
  min_feedback_count: number;
}

// Submit like/dislike/cancel feedback on a chat answer.
export async function submitFeedback(data: FeedbackRequest) {
  return post("/api/v1/feedback", data);
}

// Get the current user's feedback on a specific message.
export async function getFeedback(messageId: string) {
  return get(`/api/v1/feedback/${messageId}`);
}

// List chunk feedback statistics (admin).
export async function listChunkFeedbackStats(params: {
  knowledge_base_id?: string;
  page?: number;
  page_size?: number;
  min_approval?: number;
  max_approval?: number;
  needs_optimization?: boolean;
}) {
  const searchParams = new URLSearchParams();
  if (params.knowledge_base_id) searchParams.set("knowledge_base_id", params.knowledge_base_id);
  if (params.page) searchParams.set("page", String(params.page));
  if (params.page_size) searchParams.set("page_size", String(params.page_size));
  if (params.min_approval !== undefined) searchParams.set("min_approval", String(params.min_approval));
  if (params.max_approval !== undefined) searchParams.set("max_approval", String(params.max_approval));
  if (params.needs_optimization) searchParams.set("needs_optimization", "true");
  return get(`/api/v1/feedback/chunks/stats?${searchParams.toString()}`);
}

// Get feedback stats for a single chunk (admin).
export async function getChunkFeedbackStats(chunkId: string) {
  return get(`/api/v1/feedback/chunks/${chunkId}/stats`);
}

// List weight change logs (admin).
export async function listWeightLogs(params: { chunk_id?: string; page?: number; page_size?: number }) {
  const searchParams = new URLSearchParams();
  if (params.chunk_id) searchParams.set("chunk_id", params.chunk_id);
  if (params.page) searchParams.set("page", String(params.page));
  if (params.page_size) searchParams.set("page_size", String(params.page_size));
  return get(`/api/v1/feedback/weight-logs?${searchParams.toString()}`);
}

// Admin: reset a chunk's feedback data and weight to defaults.
export async function adminResetChunkFeedback(chunkId: string) {
  return post(`/api/v1/feedback/chunks/${chunkId}/reset`, {});
}

// Admin: manually set a chunk's recall weight.
export async function adminSetChunkWeight(chunkId: string, weight: number) {
  return put(`/api/v1/feedback/chunks/${chunkId}/weight`, { weight });
}

// Get the feedback weight-adjustment threshold config.
export async function getFeedbackThresholds() {
  return get("/api/v1/feedback/thresholds");
}
