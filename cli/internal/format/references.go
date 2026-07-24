package format

import sdk "github.com/Tencent/WeKnora/client"

// ReferenceIndex is the bounded citation pointer exposed by projected JSON,
// text, and MCP output. ChunkID is the chunk the model cited; ParentChunkID is
// retained when callers prefer to fetch the larger self-contained passage.
// Full chunk text and retrieval metadata remain available only in raw NDJSON.
type ReferenceIndex struct {
	KBID          string `json:"kb_id,omitempty"`
	ChunkID       string `json:"chunk_id"`
	ParentChunkID string `json:"parent_chunk_id,omitempty"`
}

// IndexReferences projects full SDK search results into stable lookup keys.
// It never mutates the SDK events, which keeps the raw NDJSON path lossless.
// fallbackKBID is used by `chat`, whose single KB is known by the CLI even
// when an older server omits knowledge_base_id from a reference.
func IndexReferences(refs []*sdk.SearchResult, fallbackKBID string) []ReferenceIndex {
	indexes := make([]ReferenceIndex, 0, len(refs))
	for _, r := range refs {
		if r == nil || r.ID == "" {
			continue
		}
		kbID := r.KnowledgeBaseID
		if kbID == "" {
			kbID = fallbackKBID
		}
		indexes = append(indexes, ReferenceIndex{
			KBID:          kbID,
			ChunkID:       r.ID,
			ParentChunkID: r.ParentChunkID,
		})
	}
	return indexes
}
