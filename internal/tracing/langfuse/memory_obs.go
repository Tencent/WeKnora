package langfuse

// SummarizeMemoryRecallOutput builds Langfuse output for a memory.recall span.
//
// It copies rather than returns the caller's map so a span's recorded output
// cannot change afterwards through the map the caller still holds.
func SummarizeMemoryRecallOutput(meta map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	return out
}

// SummarizeRetrievalContextOutput builds Langfuse output for retrieval
// conditioning: who memory says is asking, and what they keep returning to.
func SummarizeRetrievalContextOutput(
	background string, interests []string,
) map[string]interface{} {
	out := map[string]interface{}{
		"background":     TruncateRunes(background, 240),
		"interest_count": len(interests),
		"interests":      interests,
	}
	if out["background"] == "" {
		delete(out, "background")
	}
	return out
}
