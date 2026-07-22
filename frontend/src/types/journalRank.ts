export interface JournalRankCustomDataset {
  label: string
  level: string
}

export interface JournalRankMetadata {
  schema_version: number
  publication: string
  matched_at: string
  source: string
  found: boolean
  official?: Record<string, string>
  official_all?: Record<string, string>
  custom?: JournalRankCustomDataset[]
}

export interface KnowledgeJournalMetadata {
  journal_rank?: JournalRankMetadata
  [key: string]: unknown
}
