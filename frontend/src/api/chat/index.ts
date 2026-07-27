import { get, post, put, del, postChat } from "../../utils/request";



export async function createSessions(data = {}) {
  return post("/api/v1/sessions", data);
}

export async function getSessionsList(page: number, page_size: number, source?: string) {
  const params = new URLSearchParams({ page: String(page), page_size: String(page_size) });
  if (source) {
    params.set("source", source);
  }
  return get(`/api/v1/sessions?${params.toString()}`);
}

export async function pinSession(session_id: string) {
  return post(`/api/v1/sessions/${session_id}/pin`, {});
}

export async function unpinSession(session_id: string) {
  return del(`/api/v1/sessions/${session_id}/pin`);
}

export async function generateSessionsTitle(session_id: string, data: any) {
  return post(`/api/v1/sessions/${session_id}/generate_title`, data);
}

export async function updateSession(session_id: string, data: { title: string; description?: string }) {
  return put(`/api/v1/sessions/${session_id}`, data);
}

export async function knowledgeChat(data: { session_id: string; query: string; }) {
  return postChat(`/api/v1/knowledge-chat/${data.session_id}`, { query: data.query, channel: "web" });
}

// Agent chat with streaming support
export async function agentChat(data: { 
  session_id: string; 
  query: string;
  knowledge_base_ids?: string[];
  agent_enabled: boolean;
}) {
  return postChat(`/api/v1/agent-chat/${data.session_id}`, { 
    query: data.query,
    knowledge_base_ids: data.knowledge_base_ids,
    agent_enabled: data.agent_enabled,
    channel: "web"
  });
}

export async function getMessageList(data: { session_id: string; limit: number, created_at: string }) {
  if (data.created_at) {
    return get(`/api/v1/messages/${data.session_id}/load?before_time=${encodeURIComponent(data.created_at)}&limit=${data.limit}`);
  } else {
    return get(`/api/v1/messages/${data.session_id}/load?limit=${data.limit}`);
  }
}

export async function delSession(session_id: string) {
  return del(`/api/v1/sessions/${session_id}`);
}

export async function batchDelSessions(ids: string[]) {
  return del(`/api/v1/sessions/batch`, { ids });
}

export async function deleteAllSessions() {
  return del(`/api/v1/sessions/batch`, { delete_all: true });
}

export async function getSession(session_id: string) {
  return get(`/api/v1/sessions/${session_id}`);
}

export async function stopSession(session_id: string, message_id: string) {
  return post(`/api/v1/sessions/${session_id}/stop`, { message_id });
}

export async function clearSessionMessages(session_id: string) {
  return del(`/api/v1/sessions/${session_id}/messages`);
}

/**
 * Issue #1248: like / dislike / cancel an assistant message.
 * `rating="none"` cancels any prior rating. Reasons are an optional
 * preset-key list, only meaningful when rating="dislike".
 */
export async function setMessageFeedback(payload: {
  session_id: string;
  message_id: string;
  rating: "like" | "dislike" | "none";
  reasons?: string[];
  comment?: string;
}) {
  const { session_id, message_id, ...body } = payload;
  return put(`/api/v1/sessions/${session_id}/messages/${message_id}/feedback`, body);
}

export async function getChunkFeedbackStats(params: {
  kb_id: string;
  page?: number;
  page_size?: number;
  sort_by?: string;
  low_quality?: boolean;
  keyword?: string;
  knowledge_id?: string;
}) {
  const search = new URLSearchParams();
  if (params.page) search.set("page", String(params.page));
  if (params.page_size) search.set("page_size", String(params.page_size));
  if (params.sort_by) search.set("sort_by", params.sort_by);
  if (params.low_quality) search.set("low_quality", "true");
  if (params.keyword) search.set("keyword", params.keyword);
  if (params.knowledge_id) search.set("knowledge_id", params.knowledge_id);
  return get(`/api/v1/knowledge-bases/${params.kb_id}/feedback/chunk-stats?${search.toString()}`);
}

export async function getChunkWeightLogs(params: {
  kb_id: string;
  chunk_id?: string;
  page?: number;
  page_size?: number;
}) {
  const search = new URLSearchParams();
  if (params.chunk_id) search.set("chunk_id", params.chunk_id);
  if (params.page) search.set("page", String(params.page));
  if (params.page_size) search.set("page_size", String(params.page_size));
  return get(`/api/v1/knowledge-bases/${params.kb_id}/feedback/weight-logs?${search.toString()}`);
}

export async function resetKnowledgeBaseFeedback(kb_id: string) {
  return post(`/api/v1/knowledge-bases/${kb_id}/feedback/reset`, {});
}
