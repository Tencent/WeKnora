package service

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---------------------------------------------------------------------------
// Regression tests for the retry idempotency of image:multimodal: since an
// index write failure fails the task (so asynq retries it), a retry re-runs
// processImage for the same image. Its OCR/caption chunks must therefore be
// replaced, not appended — and only that image's chunks may be touched.
// ---------------------------------------------------------------------------

// idxImageChunk builds a chunk shaped like the ones processImage persists, so
// the tests can seed "what a previous attempt left in the database".
func idxImageChunk(id, imageURL string, chunkType types.ChunkType, status int) *types.Chunk {
	imageInfo, _ := json.Marshal([]types.ImageInfo{{URL: imageURL, OriginalURL: imageURL}})
	return &types.Chunk{
		ID:              id,
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		Content:         "previous attempt",
		ChunkType:       chunkType,
		ParentChunkID:   "parent-chunk-1",
		IsEnabled:       true,
		Status:          status,
		ImageInfo:       string(imageInfo),
	}
}

// TestProcessImageRetryReplacesChunksInsteadOfAppending is the core regression:
// the first attempt persists its chunk and then fails to index it, and the
// retry — same payload, index write healthy again — must leave exactly one OCR
// chunk behind instead of two.
func TestProcessImageRetryReplacesChunksInsteadOfAppending(t *testing.T) {
	t.Parallel()

	indexErr := errors.New("vector store unavailable")
	backend := &idxEngineRepo{batchSaveErr: indexErr}
	h := newIdxHarness(t, backend, defaultIndexKB())

	if _, err := h.run(t); !errors.Is(err, indexErr) {
		t.Fatalf("first attempt error = %v, want the index failure %v", err, indexErr)
	}
	if got := len(h.repo.byID); got != 1 {
		t.Fatalf("chunks after the failed first attempt = %d, want 1", got)
	}
	firstID := ""
	for id := range h.repo.byID {
		firstID = id
	}

	// asynq hands the very same payload back on retry; the index write works now.
	backend.batchSaveErr = nil
	out, err := h.runRetry(t)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}

	if got := len(h.repo.byID); got != 1 {
		t.Errorf("chunks after the retry = %d, want 1: a retry must replace the image's chunks, "+
			"not append to them", got)
	}
	if _, present := h.repo.byID[firstID]; present {
		t.Errorf("chunk %s from the failed attempt survived the retry", firstID)
	}
	found := false
	for _, id := range h.repo.deleted {
		if id == firstID {
			found = true
		}
	}
	if !found {
		t.Errorf("deleted chunks = %v, want the stale chunk %s", h.repo.deleted, firstID)
	}

	succeeded, failed := decodeIndexTrace(t, out)
	if succeeded != 1 || failed != 0 {
		t.Errorf("out[\"indexed\"] = {succeeded:%d, failed:%d}, want {succeeded:1, failed:0}", succeeded, failed)
	}
	if len(backend.saved) != 2 {
		t.Fatalf("vector-store writes = %d, want one per attempt", len(backend.saved))
	}
	if got := backend.saved[len(backend.saved)-1].SourceID; got == firstID {
		t.Errorf("retry indexed the stale chunk %s instead of its replacement", got)
	}
	for id, chunk := range h.repo.byID {
		if chunk.Status != int(types.ChunkStatusIndexed) {
			t.Errorf("chunk %s status = %d, want indexed", id, chunk.Status)
		}
	}
}

// TestProcessImageRepairKeepsOtherImagesAndDocuments pins the blast radius: the
// repair keys on this image's URL inside this tenant and knowledge, so a sibling
// image sharing the same parent text chunk (and another document reusing the same
// image URL) must come through untouched.
func TestProcessImageRepairKeepsOtherImagesAndDocuments(t *testing.T) {
	t.Parallel()

	backend := &idxEngineRepo{}
	h := newIdxHarness(t, backend, defaultIndexKB())
	imageURL := h.payload.ImageURL

	sibling := idxImageChunk(
		"sibling-caption", "local://img/b.png", types.ChunkTypeImageCaption, int(types.ChunkStatusIndexed))
	text := &types.Chunk{
		ID: "text-1", TenantID: 1, KnowledgeID: "k-1", KnowledgeBaseID: "kb-1",
		Content: "document body", ChunkType: types.ChunkTypeText, IsEnabled: true,
	}
	otherDoc := idxImageChunk(
		"other-doc-ocr", imageURL, types.ChunkTypeImageOCR, int(types.ChunkStatusIndexed))
	otherDoc.KnowledgeID = "k-2"
	stale := idxImageChunk(
		"stale-a-ocr", imageURL, types.ChunkTypeImageOCR, int(types.ChunkStatusStored))
	for _, chunk := range []*types.Chunk{sibling, text, otherDoc, stale} {
		h.repo.byID[chunk.ID] = chunk
	}

	if _, err := h.runRetry(t); err != nil {
		t.Fatalf("processImage: %v", err)
	}

	for _, keep := range []*types.Chunk{sibling, text, otherDoc} {
		if _, present := h.repo.byID[keep.ID]; !present {
			t.Errorf("%s chunk %s was deleted by another image's repair", keep.ChunkType, keep.ID)
		}
	}
	if _, present := h.repo.byID[stale.ID]; present {
		t.Errorf("stale chunk %s of the repaired image survived", stale.ID)
	}
	if got := len(h.repo.byID); got != 4 {
		t.Errorf("chunks = %d, want 4 (sibling + text + other document + the replacement)", got)
	}
	if len(backend.deleted) != 1 || backend.deleted[0] != stale.ID {
		t.Errorf("index deletions = %v, want only %s: the purge is unconditional over the replaced chunks "+
			"of this image (a row can have a retrieval entry without being marked indexed), and must "+
			"still touch nothing else", backend.deleted, stale.ID)
	}
}

// TestProcessImageRepairPurgesIndexEntriesOfReplacedChunks covers the retry that
// follows a late failure (the parent finalize, not the index write): the previous
// attempt's chunks are already marked indexed, so their retrieval-store entries
// must go with the rows — search must never cite a chunk that no longer exists.
func TestProcessImageRepairPurgesIndexEntriesOfReplacedChunks(t *testing.T) {
	t.Parallel()

	backend := &idxEngineRepo{}
	h := newIdxHarness(t, backend, defaultIndexKB())

	// Same type this round produces: replacement is per chunk type, so a stale
	// chunk of a type the retry does not reproduce is deliberately kept (see
	// TestProcessImageRetryKeepsStaleChunksOfTypesNotReproduced).
	indexedStale := idxImageChunk(
		"stale-a-ocr", h.payload.ImageURL, types.ChunkTypeImageOCR, int(types.ChunkStatusIndexed))
	h.repo.byID[indexedStale.ID] = indexedStale

	if _, err := h.runRetry(t); err != nil {
		t.Fatalf("processImage: %v", err)
	}

	if _, present := h.repo.byID[indexedStale.ID]; present {
		t.Errorf("indexed chunk %s from the previous attempt survived the repair", indexedStale.ID)
	}
	if len(backend.deleted) != 1 || backend.deleted[0] != indexedStale.ID {
		t.Errorf("index deletions = %v, want [%s]", backend.deleted, indexedStale.ID)
	}
	if len(h.repo.deleted) != 1 || h.repo.deleted[0] != indexedStale.ID {
		t.Errorf("deleted chunks = %v, want [%s]", h.repo.deleted, indexedStale.ID)
	}
	if got := len(h.repo.byID); got != 1 {
		t.Errorf("chunks = %d, want 1: the stale chunk was replaced", got)
	}
	for id, chunk := range h.repo.byID {
		if id == indexedStale.ID {
			t.Errorf("chunk %s should have been replaced", id)
		}
		if chunk.Status != int(types.ChunkStatusIndexed) {
			t.Errorf("chunk %s status = %d, want indexed", id, chunk.Status)
		}
	}
}

// TestProcessImageFirstAttemptSkipsRepairScan pins the cost bound: the repair
// scan reads every multimodal chunk of the knowledge, so it only runs on a
// retry. A first attempt has no previous attempt to clean up after and must
// leave existing chunks alone instead of paying that scan once per image.
func TestProcessImageFirstAttemptSkipsRepairScan(t *testing.T) {
	t.Parallel()

	backend := &idxEngineRepo{}
	h := newIdxHarness(t, backend, defaultIndexKB())

	existing := idxImageChunk(
		"existing-ocr", h.payload.ImageURL, types.ChunkTypeImageOCR, int(types.ChunkStatusIndexed))
	h.repo.byID[existing.ID] = existing

	if _, err := h.run(t); err != nil {
		t.Fatalf("processImage: %v", err)
	}

	if _, present := h.repo.byID[existing.ID]; !present {
		t.Errorf("chunk %s was removed by a first attempt", existing.ID)
	}
	if len(h.repo.deleted) != 0 || len(backend.deleted) != 0 {
		t.Errorf("deletions = rows %v, index %v; want none on a first attempt", h.repo.deleted, backend.deleted)
	}
}

// TestProcessImageRetryIndexesRetainedChunksWithoutNewContent covers the retry
// that extracts nothing new: the previous attempt persisted this image's OCR
// chunk and then failed to index it, and the round that used to produce the text
// now yields nothing. The chunk is kept — this attempt did not reproduce its type
// — and it must still be indexed, otherwise the image stays unsearchable while
// the task reports success: the silent index failure this PR removes.
func TestProcessImageRetryIndexesRetainedChunksWithoutNewContent(t *testing.T) {
	t.Parallel()

	backend := &idxEngineRepo{}
	h := newIdxHarness(t, backend, defaultIndexKB())
	// Nothing is extracted this round: no observation, no caption, no OCR.
	h.payload.EnableOCR = false
	h.payload.EnableCaption = false
	h.payload.ImageAttrsEnabled = false

	retained := idxImageChunk(
		"retained-ocr", h.payload.ImageURL, types.ChunkTypeImageOCR, int(types.ChunkStatusStored))
	h.repo.byID[retained.ID] = retained

	out, err := h.runRetry(t)
	if err != nil {
		t.Fatalf("processImage: %v", err)
	}

	if _, present := h.repo.byID[retained.ID]; !present {
		t.Fatalf("chunk %s was deleted: a round that extracted nothing must keep the previous attempt's chunk",
			retained.ID)
	}
	if len(backend.saved) != 1 || backend.saved[0].SourceID != retained.ID {
		t.Fatalf("vector-store writes = %v, want the retained chunk %s", backend.saved, retained.ID)
	}
	if got := h.repo.byID[retained.ID].Status; got != int(types.ChunkStatusIndexed) {
		t.Errorf("retained chunk status = %d, want indexed (%d)", got, types.ChunkStatusIndexed)
	}
	succeeded, failed := decodeIndexTrace(t, out)
	if succeeded != 1 || failed != 0 {
		t.Errorf("out[\"indexed\"] = {succeeded:%d, failed:%d}, want {succeeded:1, failed:0}", succeeded, failed)
	}
	if len(h.repo.created) != 0 {
		t.Errorf("persisted chunks = %d, want none: this attempt extracted nothing", len(h.repo.created))
	}
}

// TestProcessImageRetryKeepsStaleChunksOfTypesNotReproduced pins the per-type
// scope of the replacement: the previous attempt indexed both an OCR and a
// caption chunk, while this retry reproduces only the OCR one. The caption chunk
// is still the image's only caption, so it must be kept — deleting every
// multimodal chunk of the image dropped searchable content that main keeps.
func TestProcessImageRetryKeepsStaleChunksOfTypesNotReproduced(t *testing.T) {
	t.Parallel()

	backend := &idxEngineRepo{}
	h := newIdxHarness(t, backend, defaultIndexKB())

	staleCaption := idxImageChunk(
		"stale-caption", h.payload.ImageURL, types.ChunkTypeImageCaption, int(types.ChunkStatusIndexed))
	staleOCR := idxImageChunk(
		"stale-ocr", h.payload.ImageURL, types.ChunkTypeImageOCR, int(types.ChunkStatusIndexed))
	h.repo.byID[staleCaption.ID] = staleCaption
	h.repo.byID[staleOCR.ID] = staleOCR

	if _, err := h.runRetry(t); err != nil {
		t.Fatalf("processImage: %v", err)
	}

	if _, present := h.repo.byID[staleCaption.ID]; !present {
		t.Errorf("caption chunk %s was deleted although this attempt produced no caption", staleCaption.ID)
	}
	if _, present := h.repo.byID[staleOCR.ID]; present {
		t.Errorf("OCR chunk %s from the previous attempt survived although this attempt replaced it", staleOCR.ID)
	}
	if got := len(h.repo.byID); got != 2 {
		t.Errorf("chunks = %d, want 2: the kept caption plus the replacement OCR chunk", got)
	}
	for _, id := range backend.deleted {
		if id == staleCaption.ID {
			t.Errorf("index deletions = %v, must not contain the kept caption %s", backend.deleted, staleCaption.ID)
		}
	}
}

// TestProcessImageRepairPurgesIndexEntriesOfUnindexedStaleChunks covers the retry
// after a partially-successful index write: the previous attempt persisted the
// row, but CompositeRetrieveEngine.BatchIndex writes to several engines
// concurrently, so it can store an entry and still return an error (and a
// BatchIndex that succeeded followed by a failed UpdateChunk leaves the status
// unset). The row therefore says "not indexed" while a retrieval entry exists.
// The replacement must delete that entry unconditionally: skipping every row that
// is not flagged indexed leaves an orphan entry that occupies a top-k slot and is
// dropped when the results are assembled.
func TestProcessImageRepairPurgesIndexEntriesOfUnindexedStaleChunks(t *testing.T) {
	t.Parallel()

	backend := &idxEngineRepo{}
	h := newIdxHarness(t, backend, defaultIndexKB())

	partial := idxImageChunk(
		"partial-ocr", h.payload.ImageURL, types.ChunkTypeImageOCR, int(types.ChunkStatusStored))
	h.repo.byID[partial.ID] = partial

	if _, err := h.runRetry(t); err != nil {
		t.Fatalf("processImage: %v", err)
	}

	if len(backend.deleted) != 1 || backend.deleted[0] != partial.ID {
		t.Errorf("index deletions = %v, want [%s]: a row persisted without being marked indexed "+
			"can still have a retrieval entry", backend.deleted, partial.ID)
	}
	if _, present := h.repo.byID[partial.ID]; present {
		t.Errorf("stale chunk %s survived the repair", partial.ID)
	}
}

// TestProcessImageRepairAbortsWhenIndexPurgeFails pins the failure contract of
// the purge: when the retrieval entries of the replaced chunks cannot be removed,
// the rows must not be deleted and no replacement may be written, because the
// rows are the only record of the ids still to purge. The error propagates so
// asynq retries the whole image with rows and entries still consistent.
func TestProcessImageRepairAbortsWhenIndexPurgeFails(t *testing.T) {
	t.Parallel()

	purgeErr := errors.New("vector store unavailable")
	backend := &idxEngineRepo{deleteErr: purgeErr}
	h := newIdxHarness(t, backend, defaultIndexKB())

	stale := idxImageChunk(
		"stale-ocr", h.payload.ImageURL, types.ChunkTypeImageOCR, int(types.ChunkStatusIndexed))
	h.repo.byID[stale.ID] = stale

	if _, err := h.runRetry(t); !errors.Is(err, purgeErr) {
		t.Fatalf("processImage error = %v, want the purge failure %v", err, purgeErr)
	}

	if _, present := h.repo.byID[stale.ID]; !present {
		t.Errorf("stale chunk %s was deleted although its index entries could not be purged", stale.ID)
	}
	if len(h.repo.deleted) != 0 {
		t.Errorf("deleted chunks = %v, want none when the purge failed", h.repo.deleted)
	}
	if len(h.repo.created) != 0 {
		t.Errorf("persisted chunks = %d, want none when the purge failed", len(h.repo.created))
	}
}
