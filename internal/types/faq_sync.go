package types

import "encoding/json"

// FAQChunkSyncPair links source/destination FAQ chunks sharing content_hash.
type FAQChunkSyncPair struct {
	SrcChunkID string
	DstChunkID string
}

// FAQChunkDiffResult is the output of FAQChunkDiff.
type FAQChunkDiffResult struct {
	ChunksToAdd    []string
	ChunksToDelete []string
	MatchedPairs   []FAQChunkSyncPair
}

// FAQChunkStatus holds lightweight fields for clone status sync.
type FAQChunkStatus struct {
	ID             string
	TagID          string
	IsEnabled      bool
	Flags          ChunkFlags
	AnswerStrategy AnswerStrategy
	Metadata       JSON
}

// FAQChunkStatusFieldsEqual compares status fields (tag excluded).
func FAQChunkStatusFieldsEqual(a, b *FAQChunkStatus) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.IsEnabled != b.IsEnabled || a.Flags != b.Flags {
		return false
	}
	return NormalizeAnswerStrategy(a.AnswerStrategy) == NormalizeAnswerStrategy(b.AnswerStrategy)
}

// FAQChunkNeedsStatusSync reports whether a matched pair needs status sync.
func FAQChunkNeedsStatusSync(src, dst *FAQChunkStatus, mappedSrcTagID string) bool {
	if src == nil || dst == nil {
		return false
	}
	if mappedSrcTagID != dst.TagID {
		return true
	}
	return !FAQChunkStatusFieldsEqual(src, dst)
}

// NormalizeAnswerStrategy treats empty as "all".
func NormalizeAnswerStrategy(s AnswerStrategy) AnswerStrategy {
	if s == "" {
		return AnswerStrategyAll
	}
	return s
}

// PatchFAQAnswerStrategy copies answer_strategy from src metadata onto dst metadata.
func PatchFAQAnswerStrategy(dstMeta, srcMeta JSON) (JSON, error) {
	var dstM, srcM FAQChunkMetadata
	if len(dstMeta) > 0 {
		if err := json.Unmarshal(dstMeta, &dstM); err != nil {
			return nil, err
		}
	}
	if len(srcMeta) > 0 {
		if err := json.Unmarshal(srcMeta, &srcM); err != nil {
			return nil, err
		}
	}
	dstM.AnswerStrategy = srcM.AnswerStrategy
	bytes, err := json.Marshal(&dstM)
	if err != nil {
		return nil, err
	}
	return JSON(bytes), nil
}
