package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// These tests cover the backfill of image_vector chunks for documents ingested
// before the switch existed. Without it "image search enabled" and "no image is
// ever found" look identical from outside — re-index only flips the visibility
// of vectors that already exist, and nothing errors.

// backfillReader is a stub imageByteReader that serves a fixed byte payload per
// URL and can be told to fail for chosen URLs.
type backfillReader struct {
	bytes   map[string][]byte
	failing map[string]bool
	asked   []string
}

func (r *backfillReader) ReadImageBytes(
	_ context.Context, payload types.ImageMultimodalPayload,
) ([]byte, error) {
	r.asked = append(r.asked, payload.ImageURL)
	if r.failing[payload.ImageURL] {
		return nil, errors.New("object gone from storage")
	}
	data, ok := r.bytes[payload.ImageURL]
	if !ok {
		return nil, errors.New("unknown image")
	}
	return data, nil
}

// mustImageInfoJSON renders ImageInfo the way ingestion stores it: a JSON
// array, since a chunk may reference several images.
func backfillOCRChunk(t *testing.T, id, parentID, url, caption string) *types.Chunk {
	return &types.Chunk{
		ID:              id,
		ChunkType:       types.ChunkTypeImageCaption,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: reindexKBID,
		TenantID:        1,
		ParentChunkID:   parentID,
		IsEnabled:       true,
		ImageInfo:       mustImageInfoJSON(t, []types.ImageInfo{{URL: url, OriginalURL: url, Caption: caption}}),
	}
}

func backfillVectorChunk(t *testing.T, id, url string) *types.Chunk {
	return &types.Chunk{
		ID:              id,
		ChunkType:       types.ChunkTypeImageVector,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: reindexKBID,
		ImageInfo:       mustImageInfoJSON(t, []types.ImageInfo{{URL: url}}),
	}
}

// --- inventory -------------------------------------------------------------

// Re-index must be safe to run twice: without this exclusion a second run
// stacks a duplicate image_vector chunk on every image.
func TestCollectImageBackfillTargetsSkipsImagesThatAlreadyHaveVectors(t *testing.T) {
	covered := backfillOCRChunk(t, "cap-1", "text-1", "resource://a", "一只猫")
	vector := backfillVectorChunk(t, "vec-1", "resource://a")

	targets := collectImageBackfillTargets([]*types.Chunk{covered, vector})

	require.Empty(t, targets, "an image that already has a vector must not be backfilled again")
}

// The case the structured source cannot see: an image whose OCR and caption
// both came back empty leaves no chunk behind, so it is recovered from the
// image references in the text chunks.
func TestCollectImageBackfillTargetsFallsBackToMarkdown(t *testing.T) {
	text := reindexTextChunk("text-1",
		"电路说明\n![diagram](resource://wiring)\n其余正文", true)

	targets := collectImageBackfillTargets([]*types.Chunk{text})

	require.Len(t, targets, 1)
	require.Equal(t, "resource://wiring", targets[0].info.URL)
	require.Equal(t, "text-1", targets[0].parentChunkID,
		"an image found in a text chunk hangs off that text chunk")
}

// OCR and caption chunks for one image each carry the full ImageInfo, so a
// naive collection would embed the same picture twice.
func TestCollectImageBackfillTargetsDeduplicates(t *testing.T) {
	ocr := backfillOCRChunk(t, "ocr-1", "text-1", "resource://a", "")
	ocr.ChunkType = types.ChunkTypeImageOCR
	caption := backfillOCRChunk(t, "cap-1", "text-1", "resource://a", "一只猫")

	targets := collectImageBackfillTargets([]*types.Chunk{ocr, caption})

	require.Len(t, targets, 1, "one image, one vector")
	require.Equal(t, "一只猫", targets[0].info.Caption,
		"the caption travels with the target so the stored chunk keeps it")
}

// Partial text must not produce a garbage URL that later fails to read.
func TestCollectImageBackfillTargetsIgnoresNoise(t *testing.T) {
	chunks := []*types.Chunk{
		reindexTextChunk("text-1", "no images here", false),
		reindexTextChunk("text-2", "broken ![alt]() reference", true),
		backfillOCRChunk(t, "cap-1", "text-1", "  ", "empty url"),
	}

	require.Empty(t, collectImageBackfillTargets(chunks))
}

// --- backfill --------------------------------------------------------------

func newBackfillService(
	t *testing.T, kb *types.KnowledgeBase, engine *reindexEngine,
	chunkRepo *reindexChunkRepo, chunks *recordingChunkService, reader imageByteReader,
) *knowledgeService {
	t.Helper()
	svc := newReindexService(t, kb, engine, chunkRepo, &reindexKnowledgeRepo{})
	svc.chunkService = chunks
	svc.imageReader = reader
	return svc
}

// The point of the file: after the switch flips on, images ingested before it
// existed get a real image_vector chunk and an image-modality index entry.
func TestReindexKnowledgeVectorsBackfillsImagesWhenSwitchTurnsOn(t *testing.T) {
	engine := &reindexEngine{}
	chunkRepo := &reindexChunkRepo{chunks: map[string][]*types.Chunk{
		"knowledge-1": {
			reindexTextChunk("text-1", "一只猫趴在窗台上", true),
			backfillOCRChunk(t, "cap-1", "text-1", "resource://cat", "一只猫"),
		},
	}}
	chunks := &recordingChunkService{}
	kb := &types.KnowledgeBase{
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true},
	}
	reader := &backfillReader{bytes: map[string][]byte{"resource://cat": {0x89, 0x50}}}
	svc := newBackfillService(t, kb, engine, chunkRepo, chunks, reader)

	require.NoError(t, svc.reindexKnowledgeVectors(reindexContext(), kb,
		&types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: reindexKBID, TenantID: 1}))

	require.Equal(t, []string{"resource://cat"}, reader.asked)
	require.Len(t, chunks.created, 1)
	created := chunks.created[0]
	require.Equal(t, types.ChunkTypeImageVector, created.ChunkType)
	require.Equal(t, "text-1", created.ParentChunkID,
		"the backfilled chunk must hang off the text chunk the image came from")
	require.True(t, created.IsEnabled, "a disabled vector is invisible to retrieval")
	require.Equal(t, "![image](resource://cat)", created.Content)

	// The picture itself, not a markdown link, has to reach the vector store.
	var imageEntries []*types.IndexInfo
	for _, info := range engine.indexed {
		if info.IsImage() {
			imageEntries = append(imageEntries, info)
		}
	}
	require.Len(t, imageEntries, 1, "exactly one image vector is created")
	require.Equal(t, []byte{0x89, 0x50}, imageEntries[0].ImageBytes)
	require.Equal(t, created.ID, imageEntries[0].ChunkID)
	require.Equal(t, int(types.ChunkStatusIndexed), chunks.status[created.ID])
}

// Outside the chat-template space an image vector is unreachable, so creating
// one would only strand a vector no query can find.
func TestReindexKnowledgeVectorsDoesNotBackfillWhenEnvelopeOff(t *testing.T) {
	engine := &reindexEngine{}
	chunkRepo := &reindexChunkRepo{chunks: map[string][]*types.Chunk{
		"knowledge-1": {
			reindexTextChunk("text-1", "一只猫", true),
			backfillOCRChunk(t, "cap-1", "text-1", "resource://cat", "一只猫"),
			// A second image that does have a vector, so imageIDs is not
			// empty. Without it the run returns early for a different reason
			// and the guard under test is never reached — the two guards in
			// reindexKnowledgeVectors are easy to mistake for one.
			backfillVectorChunk(t, "vec-1", "resource://dog"),
		},
	}}
	chunks := &recordingChunkService{}
	kb := &types.KnowledgeBase{
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: false},
	}
	reader := &backfillReader{bytes: map[string][]byte{"resource://cat": {0x89, 0x50}}}
	svc := newBackfillService(t, kb, engine, chunkRepo, chunks, reader)

	require.NoError(t, svc.reindexKnowledgeVectors(reindexContext(), kb,
		&types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: reindexKBID, TenantID: 1}))

	require.Empty(t, chunks.created, "no image vector outside the multimodal space")
	require.Empty(t, reader.asked, "and therefore no image is even read")
	require.Equal(t, map[string]bool{"vec-1": false}, engine.enabledUpdates,
		"the existing vector is hidden — that is the only work to do here")
}

// An image deleted from storage must not take the rest of the document with
// it: aborting on the first missing object leaves later images un-searchable.
func TestBackfillImageVectorsContinuesAfterUnreadableImage(t *testing.T) {
	kb := &types.KnowledgeBase{
		ID:               reindexKBID,
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true},
	}
	embedder := &capabilityEmbedder{supportsImage: true}
	engine := &fakeImageVectorIndexer{}
	chunks := &recordingChunkService{}
	reader := &backfillReader{
		bytes:   map[string][]byte{"resource://good": {0x01, 0x02}},
		failing: map[string]bool{"resource://gone": true},
	}
	svc := &knowledgeService{chunkService: chunks}

	created, failed := svc.backfillImageVectors(context.Background(), reader, kb,
		&types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: reindexKBID, TenantID: 1},
		embedder, engine, []imageBackfillTarget{
			{info: types.ImageInfo{URL: "resource://gone"}, parentChunkID: "text-1"},
			{info: types.ImageInfo{URL: "resource://good"}, parentChunkID: "text-2"},
		})

	require.Equal(t, 1, created)
	require.Equal(t, 1, failed)
	require.Len(t, chunks.created, 1, "the readable image is not collateral damage")
	require.Equal(t, "text-2", chunks.created[0].ParentChunkID)
	require.Len(t, engine.got, 1)
}

// A zero-byte read is not a valid image and must not be embedded.
func TestBackfillImageVectorsSkipsEmptyBytes(t *testing.T) {
	kb := &types.KnowledgeBase{
		ID:               reindexKBID,
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true},
	}
	chunks := &recordingChunkService{}
	reader := &backfillReader{bytes: map[string][]byte{"resource://empty": {}}}
	svc := &knowledgeService{chunkService: chunks}

	created, failed := svc.backfillImageVectors(context.Background(), reader, kb,
		&types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: reindexKBID, TenantID: 1},
		&capabilityEmbedder{supportsImage: true}, &fakeImageVectorIndexer{},
		[]imageBackfillTarget{{info: types.ImageInfo{URL: "resource://empty"}}})

	require.Equal(t, 0, created)
	require.Equal(t, 1, failed)
	require.Empty(t, chunks.created)
}

// --- ingestion -------------------------------------------------------------

// fakeTaskEnqueuer stands in for a real asynq client. Only its non-nil-ness
// matters: whether the multimodal fan-out can enqueue at all is what decides
// who owns a document's image vectors.
type fakeTaskEnqueuer struct{ interfaces.TaskEnqueuer }

func ingestImageVectorService(reader imageByteReader, enqueuer interfaces.TaskEnqueuer) (
	*knowledgeService, *recordingChunkService, *fakeImageVectorIndexer,
) {
	chunks := &recordingChunkService{}
	engine := &fakeImageVectorIndexer{}
	svc := &knowledgeService{chunkService: chunks, imageReader: reader, task: enqueuer}
	return svc, chunks, engine
}

func ingestKB(imageVectorEnabled bool) *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:       reindexKBID,
		TenantID: 1,
		IndexingStrategy: types.IndexingStrategy{
			VectorEnabled: true, ImageVectorEnabled: imageVectorEnabled,
		},
	}
}

// The gap the ingestion half exists for: image vectors on, no VLM configured.
// The multimodal task never runs, so without this the document keeps no image
// vector and nothing errors.
func TestIngestIndexesImageVectorsWithoutVLM(t *testing.T) {
	reader := &backfillReader{bytes: map[string][]byte{
		"resource://cat": {0x89, 0x50},
		"resource://dog": {0x89, 0x51},
	}}
	svc, chunks, engine := ingestImageVectorService(reader, nil)
	parsed := []types.ParsedChunk{
		{ChunkID: "text-1", Content: "正文 ![](resource://cat) 正文"},
		{ChunkID: "text-2", Content: "正文 ![](resource://dog) 正文"},
	}

	svc.maybeIndexImageVectorsOnIngest(context.Background(), ingestKB(true),
		&types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: reindexKBID, TenantID: 1},
		&capabilityEmbedder{supportsImage: true}, engine,
		ProcessChunksOptions{StoredImages: []docparser.StoredImage{
			{ServingURL: "resource://cat", OriginalRef: "cat.png"},
			{ServingURL: "resource://dog"},
		}}, parsed)

	require.Len(t, chunks.created, 2, "both images of the document get a vector")
	require.Equal(t, "text-1", chunks.created[0].ParentChunkID,
		"the vector hangs off the text chunk that carries the image reference")
	require.Equal(t, "text-2", chunks.created[1].ParentChunkID)
	require.Equal(t, "![image](resource://cat)", chunks.created[0].Content)
	require.Equal(t, "cat.png", decodeChunkImageInfos(chunks.created[0].ImageInfo)[0].OriginalURL,
		"the reference the document actually used survives alongside the serving URL")

	for _, info := range engine.got {
		require.True(t, info.IsImage(), "the pixels reach the vector store, not the markdown link")
	}
	require.Equal(t, []byte{0x89, 0x50}, engine.got[0].ImageBytes)
	require.Equal(t, []byte{0x89, 0x51}, engine.got[1].ImageBytes)
}

// When the multimodal fan-out is going to run it already indexes every image;
// running both writers would embed each picture twice.
func TestIngestSkipsImageVectorsWhenMultimodalTaskOwnsThem(t *testing.T) {
	reader := &backfillReader{bytes: map[string][]byte{"resource://cat": {0x89, 0x50}}}
	svc, chunks, engine := ingestImageVectorService(reader, &fakeTaskEnqueuer{})
	parsed := []types.ParsedChunk{{ChunkID: "text-1", Content: "![](resource://cat)"}}

	svc.maybeIndexImageVectorsOnIngest(context.Background(), ingestKB(true),
		&types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: reindexKBID, TenantID: 1},
		&capabilityEmbedder{supportsImage: true}, engine,
		ProcessChunksOptions{
			EnableMultimodel: true,
			StoredImages:     []docparser.StoredImage{{ServingURL: "resource://cat"}},
		}, parsed)

	require.Empty(t, chunks.created, "the multimodal task owns these vectors")
	require.Empty(t, reader.asked, "and no image is read twice")
	require.Empty(t, engine.got)
}

// EnableMultimodel is on but there is no enqueuer, so the fan-out would do
// nothing at all — trusting it would lose the vectors.
func TestIngestIndexesImageVectorsWhenMultimodalCannotEnqueue(t *testing.T) {
	reader := &backfillReader{bytes: map[string][]byte{"resource://cat": {0x89, 0x50}}}
	svc, chunks, _ := ingestImageVectorService(reader, nil)
	parsed := []types.ParsedChunk{{ChunkID: "text-1", Content: "![](resource://cat)"}}

	svc.maybeIndexImageVectorsOnIngest(context.Background(), ingestKB(true),
		&types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: reindexKBID, TenantID: 1},
		&capabilityEmbedder{supportsImage: true}, &fakeImageVectorIndexer{},
		ProcessChunksOptions{
			EnableMultimodel: true,
			StoredImages:     []docparser.StoredImage{{ServingURL: "resource://cat"}},
		}, parsed)

	require.Len(t, chunks.created, 1, "a fan-out that cannot enqueue owns nothing")
}

// With the switch off the KB lives in the plain `input` space, where no query
// can reach an image vector — writing one would only strand it.
func TestIngestSkipsImageVectorsOutsideMultimodalEnvelope(t *testing.T) {
	reader := &backfillReader{bytes: map[string][]byte{"resource://cat": {0x89, 0x50}}}
	svc, chunks, engine := ingestImageVectorService(reader, nil)
	parsed := []types.ParsedChunk{{ChunkID: "text-1", Content: "![](resource://cat)"}}

	svc.maybeIndexImageVectorsOnIngest(context.Background(), ingestKB(false),
		&types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: reindexKBID, TenantID: 1},
		&capabilityEmbedder{supportsImage: true}, engine,
		ProcessChunksOptions{StoredImages: []docparser.StoredImage{{ServingURL: "resource://cat"}}},
		parsed)

	require.Empty(t, chunks.created, "no image vector outside the multimodal space")
	require.Empty(t, reader.asked, "and therefore no image is even read")
}

// Ingestion writes ImageInfo as an array and the chunk stores it back as one;
// a mismatch would silently drop every caption from backfilled chunks.
func TestCollectImageBackfillTargetsKeepsImageInfoRoundTrip(t *testing.T) {
	raw := mustImageInfoJSON(t, []types.ImageInfo{{
		URL: "resource://a", OriginalURL: "resource://orig", Caption: "一只猫", OCRText: "猫",
	}})
	chunk := backfillOCRChunk(t, "cap-1", "text-1", "", "")
	chunk.ImageInfo = raw

	targets := collectImageBackfillTargets([]*types.Chunk{chunk})

	require.Len(t, targets, 1)
	require.Equal(t, types.ImageInfo{
		URL: "resource://a", OriginalURL: "resource://orig", Caption: "一只猫", OCRText: "猫",
	}, targets[0].info)
}

// Compile-time guarantee that the stubs still satisfy the seams they replace.
var (
	_ imageByteReader         = (*backfillReader)(nil)
	_ interfaces.ChunkService = (*recordingChunkService)(nil)
)
