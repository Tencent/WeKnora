package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// reprocessFixture wires a knowledgeService with the collaborators the
// reprocess path touches: a sqlite knowledge/chunk store, a stub resource
// catalog serving the document's stored images, and a recording task
// enqueuer. The KB has no embedding model, so the vector-cleanup branch is
// skipped — indexing is not this feature's seam.
type reprocessFixture struct {
	svc     *knowledgeService
	db      *gorm.DB
	catalog *stubReprocessCatalog
	tasks   *stubReprocessEnqueuer
}

type stubReprocessCatalog struct {
	interfaces.ResourceCatalog
	refs      map[string][]string              // knowledgeID -> resource:// refs
	resources map[string]*types.StoredResource // ref -> resource
}

func (c *stubReprocessCatalog) ListReferencesByOwner(
	_ context.Context, _ string, ownerIDs ...string,
) ([]string, error) {
	var out []string
	for _, id := range ownerIDs {
		out = append(out, c.refs[id]...)
	}
	return out, nil
}

func (c *stubReprocessCatalog) Resolve(_ context.Context, ref string) (*types.StoredResource, error) {
	if r, ok := c.resources[ref]; ok {
		return r, nil
	}
	return nil, apperrors.NewNotFoundError("resource not found: " + ref)
}

type stubReprocessEnqueuer struct {
	tasks []*asynq.Task
}

func (e *stubReprocessEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.tasks = append(e.tasks, task)
	return nil, nil
}

func newReprocessFixture(t *testing.T) *reprocessFixture {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared&_pragma=foreign_keys(1)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.Chunk{}))

	catalog := &stubReprocessCatalog{
		refs:      map[string][]string{},
		resources: map[string]*types.StoredResource{},
	}
	tasks := &stubReprocessEnqueuer{}
	kbs := &documentKBLookup{values: map[string]*types.KnowledgeBase{
		"kb": {
			ID:       "kb",
			TenantID: 7,
			Type:     types.KnowledgeBaseTypeDocument,
			// Image pipeline on, no embedding model: multimodal enabled but
			// the vector cleanup branch never resolves an engine.
			VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm"},
		},
	}}
	svc := &knowledgeService{
		repo:            repository.NewKnowledgeRepository(db),
		chunkRepo:       repository.NewChunkRepository(db),
		kbService:       kbs,
		resourceCatalog: catalog,
		task:            tasks,
	}
	return &reprocessFixture{svc: svc, db: db, catalog: catalog, tasks: tasks}
}

// seedDocument stores a terminal document with a text chunk embedding one
// image URL, plus one old image chunk the run must delete, and registers two
// stored images (one image-kind, one non-image to prove filtering).
func (f *reprocessFixture) seedDocument(t *testing.T, parseStatus string) {
	t.Helper()
	require.NoError(t, f.db.Create(&types.Knowledge{
		ID: "doc", TenantID: 7, KnowledgeBaseID: "kb",
		Title: "legacy", Type: types.KnowledgeTypeManual, ParseStatus: parseStatus,
	}).Error)
	require.NoError(t, f.db.Create(&types.Chunk{
		ID: "text-chunk", TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb",
		ChunkType: types.ChunkTypeText,
		// The markdown embeds the image URL; ownership matching reads this.
		Content:     "see ![fig](local://10000/doc/img-1.png) here",
		IndexStatus: "ready",
	}).Error)
	require.NoError(t, f.db.Create(&types.Chunk{
		ID: "old-image-chunk", TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb",
		ChunkType: types.ChunkTypeImageOCR, Content: "stale ocr", IndexStatus: "ready",
	}).Error)

	f.catalog.refs["doc"] = []string{"resource://img1", "resource://src"}
	f.catalog.resources["resource://img1"] = &types.StoredResource{
		ID: "r1", TenantID: 7, Kind: "image", State: types.ResourceStateActive,
		Provider: "local",
		// Composite storage path: the payload must carry the provider part.
		PhysicalPath: "storage://backend-1/local://10000/doc/img-1.png",
	}
	f.catalog.resources["resource://src"] = &types.StoredResource{
		ID: "r2", TenantID: 7, Kind: "file", State: types.ResourceStateActive,
		Provider: "local", PhysicalPath: "storage://backend-1/local://10000/doc/source.pdf",
	}
}

func reprocessCtx() context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(7))
	return ctx
}

func (f *reprocessFixture) enqueuedPayloads(t *testing.T) []types.ImageMultimodalPayload {
	t.Helper()
	out := make([]types.ImageMultimodalPayload, 0, len(f.tasks.tasks))
	for _, task := range f.tasks.tasks {
		require.Equal(t, types.TypeImageMultimodal, task.Type())
		var p types.ImageMultimodalPayload
		require.NoError(t, json.Unmarshal(task.Payload(), &p))
		out = append(out, p)
	}
	return out
}

// T007: a terminal document with stored images fans out one correctly-shaped
// payload per image, deletes the old image chunk, and flips the row to
// processing.
func TestReprocessKnowledgeImagesFansOut(t *testing.T) {
	f := newReprocessFixture(t)
	t.Cleanup(func() { liteMultimodalDel(multimodalPendingKey("doc")) })
	f.seedDocument(t, types.ParseStatusCompleted)

	k, err := f.svc.ReprocessKnowledgeImages(reprocessCtx(), "doc")
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusProcessing, k.ParseStatus)

	payloads := f.enqueuedPayloads(t)
	require.Len(t, payloads, 1) // only the image-kind resource
	p := payloads[0]
	require.Equal(t, "doc", p.KnowledgeID)
	require.Equal(t, "kb", p.KnowledgeBaseID)
	require.Equal(t, "text-chunk", p.ChunkID) // matched via rewritten handle
	require.Equal(t, "resource://img1", p.ImageURL)
	// Configuration snapshot travels with the payload (R4).
	require.Equal(t, types.ImagePipelineIDFor(p.ImageAttrsEnabled), p.ImagePipelineID)

	// Old image chunk is gone; the text chunk survives untouched.
	var n int64
	require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "old-image-chunk").Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "text-chunk").Count(&n).Error)
	require.Equal(t, int64(1), n)

	// The fan-in counter was seeded for exactly the enqueued images.
	require.Equal(t, int64(1), liteMultimodalGet(multimodalPendingKey("doc")))
}

// T008: image pipeline disabled on the KB is an explicit config error.
func TestReprocessKnowledgeImagesRequiresPipelineEnabled(t *testing.T) {
	f := newReprocessFixture(t)
	f.seedDocument(t, types.ParseStatusCompleted)
	f.svc.kbService = &documentKBLookup{values: map[string]*types.KnowledgeBase{
		"kb": {ID: "kb", TenantID: 7, Type: types.KnowledgeBaseTypeDocument},
	}}

	_, err := f.svc.ReprocessKnowledgeImages(reprocessCtx(), "doc")
	require.ErrorIs(t, err, ErrImagePipelineDisabled)
	require.Empty(t, f.tasks.tasks)
}

// T016a: a document busy with a parse is refused.
func TestReprocessKnowledgeImagesRejectsBusyDocument(t *testing.T) {
	f := newReprocessFixture(t)
	f.seedDocument(t, types.ParseStatusProcessing)

	_, err := f.svc.ReprocessKnowledgeImages(reprocessCtx(), "doc")
	require.ErrorIs(t, err, ErrDocumentBusy)
	require.Empty(t, f.tasks.tasks)
}

// T016b: pending image tasks refuse a second run (idempotency guard).
func TestReprocessKnowledgeImagesRejectsWhenInFlight(t *testing.T) {
	f := newReprocessFixture(t)
	f.seedDocument(t, types.ParseStatusCompleted)
	liteMultimodalSet(multimodalPendingKey("doc"), 2)
	t.Cleanup(func() { liteMultimodalDel(multimodalPendingKey("doc")) })

	_, err := f.svc.ReprocessKnowledgeImages(reprocessCtx(), "doc")
	require.ErrorIs(t, err, ErrImagesInFlight)
	require.Empty(t, f.tasks.tasks)
}

// T016c: a document with no stored images is a clear "nothing to do", not an
// error path that leaves state behind.
func TestReprocessKnowledgeImagesNoImages(t *testing.T) {
	f := newReprocessFixture(t)
	f.seedDocument(t, types.ParseStatusCompleted)
	f.catalog.refs["doc"] = nil

	_, err := f.svc.ReprocessKnowledgeImages(reprocessCtx(), "doc")
	require.ErrorIs(t, err, ErrNoImagesToProcess)
	require.Empty(t, f.tasks.tasks)
}

// The payload carries resource:// handles; chunks parsed before the
// resource-catalog migration embed provider:// URLs. Matching must rewrite
// the legacy provider form to the handle so imageChunkOwner finds the owner.
func TestTextChunksForOwnershipRewritesProviderURLs(t *testing.T) {
	f := newReprocessFixture(t)
	f.seedDocument(t, types.ParseStatusCompleted)
	// seedDocument stores the legacy provider:// form in the chunk.
	parsed, err := f.svc.textChunksForOwnership(reprocessCtx(), 7, "doc",
		map[string]string{"local://10000/doc/img-1.png": "resource://img1"})
	require.NoError(t, err)
	require.Len(t, parsed, 1)
	require.Equal(t, "text-chunk", parsed[0].ChunkID)
	require.Contains(t, parsed[0].Content, "resource://img1")
	require.Equal(t, "text-chunk", imageChunkOwner("resource://img1", parsed))
}

// listDocumentImages must hand out the resource:// handle (the form the file
// proxy authorizes and image_info persists), not the raw provider path.
func TestListDocumentImagesUsesResourceHandles(t *testing.T) {
	f := newReprocessFixture(t)
	f.seedDocument(t, types.ParseStatusCompleted)
	images, _, err := f.svc.listDocumentImages(reprocessCtx(),
		&types.Knowledge{ID: "doc", TenantID: 7})
	require.NoError(t, err)
	require.Len(t, images, 1)
	require.Equal(t, "resource://img1", images[0].ServingURL)
}

// T-fix: upload-era image overrides must be cleared so the KB's live config
// governs; non-image overrides (chunking) survive.
func TestClearStaleImageOverrides(t *testing.T) {
	k := &types.Knowledge{
		ID: "doc", TenantID: 7,
		Metadata: types.JSON(`{"process_overrides":{"enable_multimodel":false,` +
			`"vlm_config":{"enabled":false},"chunking_config":{"strategy":"auto"}}}`),
	}
	require.NoError(t, clearStaleImageOverrides(k))
	overrides, err := k.ProcessOverrides()
	require.NoError(t, err)
	require.NotNil(t, overrides)
	require.Nil(t, overrides.EnableMultimodel)
	require.Nil(t, overrides.VLMConfig)
	require.NotNil(t, overrides.ChunkingConfig)

	// And the KB config now resolves enabled for this document.
	kb := &types.KnowledgeBase{VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm"}}
	require.True(t, ResolveProcessConfig(kb, overrides).EnableMultimodel)
}

// T012: batch accounts accepted vs skipped independently per document.
func TestBatchReprocessKnowledgeImagesAccountsPerDocument(t *testing.T) {
	f := newReprocessFixture(t)
	t.Cleanup(func() { liteMultimodalDel(multimodalPendingKey("doc")) })
	f.seedDocument(t, types.ParseStatusCompleted) // accepted
	require.NoError(t, f.db.Create(&types.Knowledge{
		ID: "doc-noimg", TenantID: 7, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusCompleted,
	}).Error)
	require.NoError(t, f.db.Create(&types.Knowledge{
		ID: "doc-busy", TenantID: 7, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusProcessing,
	}).Error)

	// Batch lists via the repository (tenant-scoped).
	ctx := reprocessCtx()
	result, err := f.svc.BatchReprocessKnowledgeImages(ctx, "kb", nil)
	require.NoError(t, err)
	require.Equal(t, 1, result.Accepted)
	require.Equal(t, 2, result.Skipped)
	require.Equal(t, 1, result.Reasons["no_images"])
	require.Equal(t, 1, result.Reasons["not_terminal"])
	require.Len(t, f.tasks.tasks, 1) // only the seeded document ran
}

// A named id subset restricts the batch to exactly those documents.
func TestBatchReprocessKnowledgeImagesWithSelection(t *testing.T) {
	f := newReprocessFixture(t)
	t.Cleanup(func() { liteMultimodalDel(multimodalPendingKey("doc")) })
	f.seedDocument(t, types.ParseStatusCompleted) // selected
	require.NoError(t, f.db.Create(&types.Knowledge{
		ID: "doc-other", TenantID: 7, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusCompleted,
	}).Error)

	result, err := f.svc.BatchReprocessKnowledgeImages(reprocessCtx(), "kb", []string{"doc"})
	require.NoError(t, err)
	require.Equal(t, 1, result.Accepted)
	require.Len(t, f.tasks.tasks, 1) // the unselected document never ran
}
