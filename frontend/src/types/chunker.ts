// Types for the /api/v1/chunker/preview endpoint. Mirrors the JSON shape
// produced by internal/handler/chunker_debug.go. Used by the KB editor's
// chunking debug panel to render tier-info / chunk-cards / size stats.

export type StrategyTier = 'heading' | 'heuristic' | 'recursive' | 'legacy'

export interface TierRejection {
  tier: StrategyTier
  reason: string
}

export interface DocProfile {
  total_chars: number
  total_lines: number
  avg_line_len: number
  std_line_len: number
  md_heading_counts: Record<string, number>
  md_heading_total: number
  numbered_section_count: number
  all_caps_short_line_count: number
  blank_paragraph_breaks: number
  form_feed_count: number
  visual_sep_count: number
  german_chapter_count: number
  english_chapter_count: number
  chinese_chapter_count: number
  repeated_footer_count: number
  has_tables: boolean
  has_code: boolean
  code_ratio: number
  detected_langs: string[]
}

export interface PreviewChunk {
  seq: number
  start: number
  end: number
  size_chars: number
  size_tokens_approx: number
  context_header?: string
  content: string
}

export interface PreviewChunkingStats {
  count: number
  avg_chars: number
  min_chars: number
  max_chars: number
  stddev_chars: number
  truncated_to?: number
}

export interface PreviewChunkingResponse {
  selected_tier: StrategyTier
  tier_chain: StrategyTier[]
  rejected: TierRejection[]
  profile: DocProfile
  chunks: PreviewChunk[]
  stats: PreviewChunkingStats
}

export interface PreviewChunkingRequest {
  text: string
  chunking_config: {
    chunk_size: number
    chunk_overlap: number
    separators: string[]
    strategy?: string
    token_limit?: number
    languages?: string[]
  }
}
