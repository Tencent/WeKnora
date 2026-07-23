export interface StreamFolderScope {
  knowledge_base_id: string
  folder_ids: string[]
}

export interface StartStreamParams {
  session_id: any
  query: any
  knowledge_base_ids?: string[]
  knowledge_ids?: string[]
  folder_scopes?: StreamFolderScope[]
  tag_ids?: string[]
  agent_enabled?: boolean
  agent_id?: string
  web_search_enabled?: boolean
  summary_model_id?: string
  mcp_service_ids?: string[]
  skill_names?: string[]
  mentioned_items?: Array<{id: string; name: string; type: string; kb_type?: string; kb_id?: string; kb_name?: string; service_id?: string; skill_name?: string}>
  images?: Array<{data: string}>
  attachment_uploads?: Array<{data: string; file_name: string; file_size: number}>
  attachment_ids?: string[]
  suggestion_attribution?: { suggestion_set_id: string; question_id: string }
  method: string
  url: string
  embed_token?: string
  embed_session_sig?: string
  embed_visitor_id?: string
}

export function buildStreamPostBody(params: StartStreamParams): Record<string, any> {
  const postBody: Record<string, any> = {
    query: params.query,
    agent_enabled: params.agent_enabled !== undefined ? params.agent_enabled : true,
  }
  if (params.knowledge_base_ids !== undefined && params.knowledge_base_ids.length > 0) {
    postBody.knowledge_base_ids = params.knowledge_base_ids
  }
  if (params.knowledge_ids !== undefined && params.knowledge_ids.length > 0) {
    postBody.knowledge_ids = params.knowledge_ids
  }
  if (params.folder_scopes !== undefined && params.folder_scopes.length > 0) {
    postBody.folder_scopes = params.folder_scopes
  }
  if (params.agent_id) {
    postBody.agent_id = params.agent_id
  }
  if (params.web_search_enabled !== undefined) {
    postBody.web_search_enabled = params.web_search_enabled
  }
  if (params.summary_model_id) {
    postBody.summary_model_id = params.summary_model_id
  }
  if (params.mcp_service_ids !== undefined && params.mcp_service_ids.length > 0) {
    postBody.mcp_service_ids = params.mcp_service_ids
  }
  if (params.skill_names !== undefined && params.skill_names.length > 0) {
    postBody.skill_names = params.skill_names
  }
  if (params.tag_ids !== undefined && params.tag_ids.length > 0) {
    postBody.tag_ids = params.tag_ids
  }
  if (params.mentioned_items !== undefined && params.mentioned_items.length > 0) {
    postBody.mentioned_items = params.mentioned_items
  }
  if (params.images !== undefined && params.images.length > 0) {
    postBody.images = params.images
  }
  if (params.attachment_uploads !== undefined && params.attachment_uploads.length > 0) {
    postBody.attachment_uploads = params.attachment_uploads
  }
  if (params.attachment_ids !== undefined && params.attachment_ids.length > 0) {
    postBody.attachment_ids = params.attachment_ids
  }
  if (params.suggestion_attribution) {
    postBody.suggestion_attribution = params.suggestion_attribution
  }
  postBody.channel = params.embed_token ? 'embed' : 'web'
  return postBody
}
