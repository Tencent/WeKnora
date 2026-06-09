/** Matches backend types.KnowledgeProcessOverrides (snake_case JSON). */

export interface ParserEngineRule {
  file_types: string[]
  engine: string
}

export interface ChunkingConfigOverride {
  chunk_size?: number
  chunk_overlap?: number
  separators?: string[]
  parser_engine_rules?: ParserEngineRule[]
  enable_parent_child?: boolean
  parent_chunk_size?: number
  child_chunk_size?: number
  strategy?: string
  token_limit?: number
  languages?: string[]
}

export interface VLMConfigOverride {
  enabled?: boolean
  model_id?: string
}

export interface ASRConfigOverride {
  enabled?: boolean
  model_id?: string
  language?: string
}

export interface QuestionGenerationConfigOverride {
  enabled?: boolean
  question_count?: number
}

export interface GraphNodeOverride {
  name: string
  attributes?: string[]
}

export interface GraphRelationOverride {
  node1: string
  node2: string
  type: string
}

export interface ExtractConfigOverride {
  enabled?: boolean
  text?: string
  tags?: string[]
  nodes?: GraphNodeOverride[]
  relations?: GraphRelationOverride[]
}

export interface KnowledgeProcessOverrides {
  parser_engine_rules?: ParserEngineRule[]
  chunking_config?: ChunkingConfigOverride
  enable_multimodel?: boolean
  vlm_config?: VLMConfigOverride
  asr_config?: ASRConfigOverride
  question_generation_config?: QuestionGenerationConfigOverride
  graph_enabled?: boolean
  extract_config?: ExtractConfigOverride
}
