/** Current state of a delegated run, independent of its parent answer. */
export interface CollaborationProgress {
  phase?: string
  tool_name?: string
  tool_call_id?: string
  iteration?: number
  updated_at?: string
}

export interface CollaborationTaskStatus {
  id: string
  name: string
  display_name?: string
  agent_id: string
  session_id: string
  run_id: string
  status: string
  progress?: CollaborationProgress | null
  prompt_id?: string
  latest_message?: string
}

export interface CollaborationStatus {
  group_id: string
  revision: number
  tasks: CollaborationTaskStatus[]
}

export interface CollaborationStatusStreamEvent {
  response_type: 'collaboration_status'
  /** Parent request identifier; child run identifiers live in tasks. */
  id: string
  assistant_message_id?: string
  data: CollaborationStatus
}

export interface ProgressStreamEvent {
  response_type: 'progress'
  id: string
  assistant_message_id?: string
  /** Cumulative visible progress text for this event, safe to replay. */
  content: string
  done: boolean
  data: { event_id: string; iteration?: number }
}

/** Persisted agent step fields used when rebuilding the visible timeline. */
export interface AgentStepRecord extends Record<string, unknown> {
  iteration?: number
  thought?: string
  reasoning_content?: string
  progress?: string
  progress_event_id?: string
}

export interface ChatHistoryMessage extends Record<string, unknown> {
  collaboration_status?: CollaborationStatus
  agent_steps?: AgentStepRecord[]
}

/** An explicit protocol correction replaces only this answer segment. */
export interface AnswerStreamMetadata {
  event_id: string
  replace?: boolean
  truncated?: boolean
  is_fallback?: boolean
}
