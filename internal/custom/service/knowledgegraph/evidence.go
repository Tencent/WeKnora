package knowledgegraph

import (
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/model"
)

type TranscriptEvidenceRef struct {
	VideoID     string
	Generation  string
	KnowledgeID string
	ChunkIndex  int
	HasIndex    bool
}

// BuildEvidenceChunkIndex creates the shared lookup used by graph completion
// and graph reads. Keys remain scoped by video and transcript generation.
func BuildEvidenceChunkIndex(chunks []model.VideoTranscriptChunk) (map[string]model.VideoTranscriptChunk, map[string]model.VideoTranscriptChunk) {
	byEvidence := make(map[string]model.VideoTranscriptChunk, len(chunks)*2)
	byIndex := make(map[string]model.VideoTranscriptChunk, len(chunks))
	for _, chunk := range chunks {
		prefix := chunk.VideoID + "\x00" + chunk.Generation + "\x00"
		if knowledgeID := strings.TrimSpace(chunk.KnowledgeID); knowledgeID != "" {
			byEvidence[prefix+knowledgeID] = chunk
		}
		if evidenceID := strings.TrimSpace(chunk.EvidenceSentenceID); evidenceID != "" {
			byEvidence[prefix+evidenceID] = chunk
		}
		byIndex[prefix+strconv.Itoa(chunk.ChunkIndex)] = chunk
	}
	return byEvidence, byIndex
}

// ResolveEvidenceChunk resolves every evidence reference form accepted by the
// product graph against the active video and transcript generation.
func ResolveEvidenceChunk(
	byEvidence map[string]model.VideoTranscriptChunk,
	byIndex map[string]model.VideoTranscriptChunk,
	videoID string,
	generation string,
	evidenceID string,
) (model.VideoTranscriptChunk, bool) {
	videoID = strings.TrimSpace(videoID)
	generation = strings.TrimSpace(generation)
	evidenceID = strings.TrimSpace(evidenceID)
	if chunk, ok := byEvidence[videoID+"\x00"+generation+"\x00"+evidenceID]; ok {
		return chunk, true
	}
	ref := ParseTranscriptEvidenceRef(evidenceID)
	if ref.VideoID == "" || ref.Generation == "" || ref.VideoID != videoID || ref.Generation != generation {
		return model.VideoTranscriptChunk{}, false
	}
	if chunk, ok := byEvidence[ref.VideoID+"\x00"+ref.Generation+"\x00"+ref.KnowledgeID]; ok {
		return chunk, true
	}
	if ref.HasIndex {
		if chunk, ok := byIndex[ref.VideoID+"\x00"+ref.Generation+"\x00"+strconv.Itoa(ref.ChunkIndex)]; ok {
			return chunk, true
		}
	}
	return model.VideoTranscriptChunk{}, false
}

func ParseTranscriptEvidenceRef(value string) TranscriptEvidenceRef {
	value = strings.TrimSpace(value)
	ref := TranscriptEvidenceRef{KnowledgeID: value}
	if pipe := strings.Index(value, "|"); pipe >= 0 {
		ref.KnowledgeID = strings.TrimSpace(value[:pipe])
		value = strings.TrimSpace(value[pipe+1:])
	}
	parts := strings.Split(value, "/")
	for index, part := range parts {
		if part != "transcript" || index+3 >= len(parts) {
			continue
		}
		ref.VideoID = strings.TrimSpace(parts[index+1])
		ref.Generation = strings.TrimSpace(parts[index+2])
		rawIndex := strings.TrimSpace(parts[index+3])
		trimmedIndex := strings.TrimLeft(rawIndex, "0")
		if trimmedIndex == "" && rawIndex != "" {
			trimmedIndex = "0"
		}
		if chunkIndex, err := strconv.Atoi(trimmedIndex); err == nil {
			ref.ChunkIndex = chunkIndex
			ref.HasIndex = true
		}
		return ref
	}
	return ref
}
