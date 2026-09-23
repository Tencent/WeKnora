package service

import "fmt"

// MaxChunksPerDocument bounds how many chunk rows (children plus parents in
// parent-child mode) a single document may produce before the pipeline
// rejects it. It is the pipeline-level companion to the chunk_size bounds
// enforced at config validation (#3539): a small chunk_size over a large,
// separator-dense document amplifies into millions of chunk rows — each
// persisted and embedded on the shared queue — which starves other tenants'
// document tasks. Counting chunks rather than input bytes is deliberate:
// structureless text is already dampened by the chunker's protected-size
// fallback, while separator-dense text amplifies at chunk granularity no
// matter its byte size.
const MaxChunksPerDocument = 100_000

// enforceChunkBudget rejects split results that exceed the per-document chunk
// budget. The outcome is deterministic — re-running with the same config
// produces the same count — so callers mark the document failed without
// retrying.
func enforceChunkBudget(totalChunks int) error {
	if totalChunks <= MaxChunksPerDocument {
		return nil
	}
	return fmt.Errorf("文档分块数量 %d 超过单文档上限 %d（chunk_size 过小或文档过大），请调大 chunk_size 或拆分文档后重新处理",
		totalChunks, MaxChunksPerDocument)
}
