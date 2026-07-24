package service

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/contentcache"
)

func nextStableChunkID(seen map[string]int, input contentcache.ChunkIDInput) string {
	if seen == nil {
		input.Occurrence = 1
		return contentcache.StableChunkID(input)
	}
	key := contentcache.ChunkIdentityKey(input)
	seen[key]++
	input.Occurrence = seen[key]
	return contentcache.StableChunkID(input)
}

func multimodalPendingKey(knowledgeID string, attempt int) string {
	if attempt > 0 {
		return fmt.Sprintf("multimodal:pending:%s:%d", knowledgeID, attempt)
	}
	return fmt.Sprintf("multimodal:pending:%s", knowledgeID)
}

func stableGeneratedQuestionID(chunkID, question string) string {
	hash := contentcache.TextHash(chunkID + "\x00" + question)
	if len(hash) > 16 {
		hash = hash[:16]
	}
	return "q" + hash
}
