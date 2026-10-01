package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// citedChunkRepo answers the two chunk-repository queries resolveCitedChunks
// and CollectImageInfoByChunkIDs exercise, keyed by the requested ID / parent
// ID lists. Unimplemented interface methods stay nil: they must never be
// reached by this code path, and a call would panic loudly in tests.
type citedChunkRepo struct {
	interfaces.ChunkRepository
	byID     map[string][]*types.Chunk
	byParent map[string][]*types.Chunk
}

func (r *citedChunkRepo) ListChunksByID(
	_ context.Context, _ uint64, ids []string,
) ([]*types.Chunk, error) {
	var out []*types.Chunk
	for _, id := range ids {
		out = append(out, r.byID[id]...)
	}
	return out, nil
}

func (r *citedChunkRepo) ListChunksByParentIDs(
	_ context.Context, _ uint64, parentIDs []string,
) ([]*types.Chunk, error) {
	var out []*types.Chunk
	for _, id := range parentIDs {
		out = append(out, r.byParent[id]...)
	}
	return out, nil
}

func citationChunk(id, content string) *types.Chunk {
	return &types.Chunk{
		ID:          id,
		TenantID:    1,
		KnowledgeID: "kn-1",
		Content:     content,
		ChunkType:   types.ChunkTypeText,
		IsEnabled:   true,
	}
}

func imageChildChunk(id, parentID, chunkType types.ChunkType, imageInfoJSON string, enabled bool) *types.Chunk {
	return &types.Chunk{
		ID:            id,
		TenantID:      1,
		KnowledgeID:   "kn-1",
		ChunkType:     chunkType,
		IsEnabled:     enabled,
		ParentChunkID: parentID,
		ImageInfo:     imageInfoJSON,
	}
}

// TestResolveCitedChunksEnrichesWithImageInfo verifies that the Reduce phase
// receives cited text chunks enriched with the caption/OCR info of their
// (enabled) image child chunks — the same treatment the summary page gets via
// reconstructEnrichedContent. Disabled image children must be skipped so the
// decorative-image cleanup composes with this path.
func TestResolveCitedChunksEnrichesWithImageInfo(t *testing.T) {
	const handle = "resource://aaaaaaaaaaaaaaaaaaaaaa"
	withImage := citationChunk("chunk-with-image", "Revenue chart\n\n![chart]("+handle+")")
	plain := citationChunk("chunk-plain", "Plain text with no images at all.")

	captionInfo := `[{"url":"` + handle + `","caption":"Quarterly revenue by region"}]`
	disabledInfo := `[{"url":"` + handle + `","caption":"DECORATIVE_CAPTION_MUST_NOT_APPEAR"}]`

	repo := &citedChunkRepo{
		byID: map[string][]*types.Chunk{
			"chunk-with-image": {withImage},
			"chunk-plain":      {plain},
		},
		byParent: map[string][]*types.Chunk{
			"chunk-with-image": {
				imageChildChunk("img-cap", "chunk-with-image", types.ChunkTypeImageCaption, captionInfo, true),
				imageChildChunk("img-off", "chunk-with-image", types.ChunkTypeImageCaption, disabledInfo, false),
			},
		},
	}

	svc := &wikiIngestService{chunkRepo: repo}
	out := svc.resolveCitedChunks(context.Background(), 1, []SlugUpdate{
		{KnowledgeID: "kn-1", SourceChunks: []string{"chunk-with-image", "chunk-plain"}},
	})

	enriched, ok := out["chunk-with-image"]
	if !ok {
		t.Fatalf("cited chunk missing from result: %v", out)
	}
	if !strings.Contains(enriched, "<image url=\""+handle+"\">") ||
		!strings.Contains(enriched, "Quarterly revenue by region") {
		t.Fatalf("cited content not enriched with caption:\n%s", enriched)
	}
	if strings.Contains(enriched, "DECORATIVE_CAPTION_MUST_NOT_APPEAR") {
		t.Fatalf("disabled image child leaked into enrichment:\n%s", enriched)
	}
	// The original Markdown must survive the enrichment, not be replaced.
	if !strings.Contains(enriched, "![chart]("+handle+")") {
		t.Fatalf("original markdown image lost during enrichment:\n%s", enriched)
	}

	if got := out["chunk-plain"]; got != "Plain text with no images at all." {
		t.Fatalf("image-free cited chunk must pass through unchanged, got:\n%s", got)
	}
}

// TestResolveCitedChunksStillFallsBackWithoutRepoRows keeps the documented
// missing/out-of-tenant behaviour: IDs the repo does not know are silently
// skipped so the Reduce phase can fall back to the Details paraphrase.
func TestResolveCitedChunksStillFallsBackWithoutRepoRows(t *testing.T) {
	repo := &citedChunkRepo{byID: map[string][]*types.Chunk{}, byParent: map[string][]*types.Chunk{}}

	svc := &wikiIngestService{chunkRepo: repo}
	out := svc.resolveCitedChunks(context.Background(), 1, []SlugUpdate{
		{KnowledgeID: "kn-1", SourceChunks: []string{"ghost-chunk"}},
	})

	if len(out) != 0 {
		t.Fatalf("unknown cited chunk must be skipped, got %v", out)
	}
}
