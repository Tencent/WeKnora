package types

import "fmt"

// QuestionGenerationActive reports whether a document knowledge base still
// wants generated questions in retrieval. FAQ knowledge bases index their own
// similar questions under the same source-id shape and are never treated as
// this switch.
func QuestionGenerationActive(kb *KnowledgeBase) bool {
	return kb != nil &&
		kb.Type != KnowledgeBaseTypeFAQ &&
		kb.QuestionGenerationConfig != nil &&
		kb.QuestionGenerationConfig.Enabled
}

// QuestionGenerationDisabled reports whether generated-question rows for a
// document knowledge base must stay out of recall and rerank. A nil config is
// off: question generation only runs when the switch is explicitly enabled.
func QuestionGenerationDisabled(kb *KnowledgeBase) bool {
	return kb != nil && kb.Type != KnowledgeBaseTypeFAQ && !QuestionGenerationActive(kb)
}

// IsGeneratedQuestionSource reports whether an index row is a generated
// question for its parent chunk. Rows are written as
// source_id = chunk_id + "-" + question id (or the hashed "-q" form) with
// source_type chunk. The chunk body itself uses source_id = chunk_id.
// Identification does not use the length of source_id.
func IsGeneratedQuestionSource(chunkID, sourceID string, sourceType SourceType) bool {
	if sourceType != ChunkSourceType || chunkID == "" || sourceID == "" || sourceID == chunkID {
		return false
	}
	return len(sourceID) > len(chunkID)+1 && sourceID[:len(chunkID)+1] == chunkID+"-"
}

// GeneratedQuestionRowPredicate is the SQL form of IsGeneratedQuestionSource.
// alias is the table qualifier without a dot, or empty for an unqualified
// column. source_type is inlined; chunk ids are ASCII so length and substr
// agree on PostgreSQL and SQLite.
func GeneratedQuestionRowPredicate(alias string) string {
	col := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}
	return fmt.Sprintf(
		"%s = %d AND %s <> '' AND %s <> %s AND substr(%s, 1, length(%s) + 1) = %s || '-'",
		col("source_type"), int(ChunkSourceType),
		col("chunk_id"),
		col("source_id"), col("chunk_id"),
		col("source_id"), col("chunk_id"), col("chunk_id"),
	)
}

// EnabledChunkBodySQL matches a parent chunk body that retrieval still treats
// as enabled. NULL is_enabled is historical data and counts as on. The caller
// binds one boolean argument for is_enabled.
func EnabledChunkBodySQL(alias string) string {
	col := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}
	return fmt.Sprintf(
		"%s = %d AND %s <> '' AND %s = %s AND (%s IS NULL OR %s = ?)",
		col("source_type"), int(ChunkSourceType),
		col("chunk_id"),
		col("source_id"), col("chunk_id"),
		col("is_enabled"), col("is_enabled"),
	)
}

// ApplySkipGeneratedQuestions marks rerank passages whose knowledge base has
// question generation turned off. disabled maps knowledge base IDs.
func ApplySkipGeneratedQuestions(results []*SearchResult, disabled map[string]struct{}) {
	if len(disabled) == 0 {
		return
	}
	for _, result := range results {
		if result == nil {
			continue
		}
		if _, ok := disabled[result.KnowledgeBaseID]; ok {
			result.SkipGeneratedQuestions = true
		}
	}
}

// FilterGeneratedQuestionHits drops generated-question index rows that belong
// to one of kbIDs. Engines that already exclude those rows in SQL return the
// same slice contents.
func FilterGeneratedQuestionHits(kbIDs []string, results []*RetrieveResult) []*RetrieveResult {
	if len(kbIDs) == 0 {
		return results
	}
	blocked := make(map[string]struct{}, len(kbIDs))
	for _, id := range kbIDs {
		if id != "" {
			blocked[id] = struct{}{}
		}
	}
	if len(blocked) == 0 {
		return results
	}
	for _, result := range results {
		if result == nil || len(result.Results) == 0 {
			continue
		}
		kept := make([]*IndexWithScore, 0, len(result.Results))
		for _, hit := range result.Results {
			if hit != nil {
				_, blockKB := blocked[hit.KnowledgeBaseID]
				if blockKB && IsGeneratedQuestionSource(hit.ChunkID, hit.SourceID, hit.SourceType) {
					continue
				}
			}
			kept = append(kept, hit)
		}
		result.Results = kept
	}
	return results
}

// GeneratedQuestionAlignResult is what aligning the index and chunk metadata
// to the current question-generation switch changed.
type GeneratedQuestionAlignResult struct {
	Enabled        bool  `json:"enabled"`
	IndexRows      int64 `json:"index_rows"`
	MetadataChunks int64 `json:"metadata_chunks"`
	IndexUpdated   bool  `json:"index_updated"`
}
