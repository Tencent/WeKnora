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
	out, err := h.run(t)
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

	if _, err := h.run(t); err != nil {
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
	if len(backend.deleted) != 0 {
		t.Errorf("index deletions = %v, want none: no chunk owned by the repaired image was indexed", backend.deleted)
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

	indexedStale := idxImageChunk(
		"stale-a-caption", h.payload.ImageURL, types.ChunkTypeImageCaption, int(types.ChunkStatusIndexed))
	h.repo.byID[indexedStale.ID] = indexedStale

	if _, err := h.run(t); err != nil {
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
