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

// SearchResultKnowledgeBaseIDs returns the distinct knowledge base IDs on results.
func SearchResultKnowledgeBaseIDs(results []*SearchResult) []string {
	seen := make(map[string]struct{}, len(results))
	ids := make([]string, 0, len(results))
	for _, result := range results {
		if result == nil || result.KnowledgeBaseID == "" {
			continue
		}
		if _, ok := seen[result.KnowledgeBaseID]; ok {
			continue
		}
		seen[result.KnowledgeBaseID] = struct{}{}
		ids = append(ids, result.KnowledgeBaseID)
	}
	return ids
}

// QuestionGenerationOffIDs returns document knowledge bases whose generated
// questions must stay out of rerank passages.
func QuestionGenerationOffIDs(kbs []*KnowledgeBase) map[string]struct{} {
	disabled := make(map[string]struct{})
	for _, kb := range kbs {
		if QuestionGenerationDisabled(kb) {
			disabled[kb.ID] = struct{}{}
		}
	}
	return disabled
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
