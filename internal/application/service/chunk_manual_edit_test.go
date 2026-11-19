package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const sepNL = "\n"

// manualChunkRepo wraps the real repository over a temp SQLite DB and
// records mutations so tests can assert what reached storage.
type manualChunkRepo struct {
	interfaces.ChunkRepository
	created    []*types.Chunk
	deletedIDs []string
	db         *gorm.DB
	savedRows  []*types.Chunk
	savedRev   []*types.ChunkRevision
}

func (r *manualChunkRepo) CreateChunks(ctx context.Context, chunks []*types.Chunk) error {
	r.created = append(r.created, chunks...)
	return r.ChunkRepository.CreateChunks(ctx, chunks)
}

func (r *manualChunkRepo) DeleteChunks(ctx context.Context, tenantID uint64, ids []string) error {
	r.deletedIDs = append(r.deletedIDs, ids...)
	return r.ChunkRepository.DeleteChunks(ctx, tenantID, ids)
}

func (r *manualChunkRepo) SaveChunkRevision(
	ctx context.Context, chunk *types.Chunk, revision *types.ChunkRevision, expected int,
) error {
	copyOfRow := *chunk
	copyOfRev := *revision
	r.savedRows = append(r.savedRows, &copyOfRow)
	r.savedRev = append(r.savedRev, &copyOfRev)
	return r.ChunkRepository.SaveChunkRevision(ctx, chunk, revision, expected)
}

func newManualChunkDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "manual.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&types.Chunk{}, &types.ChunkRevision{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedManualChunk(t *testing.T, db *gorm.DB, row *types.Chunk) {
	t.Helper()
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
}

// helper builds an editor-granted context over tenant 1 / KB kb.
func manualChunkCtx() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	return (&access.KBAccess{
		KnowledgeBase: &types.KnowledgeBase{ID: "kb", TenantID: 1},
		Caller:        types.CallerFromContext(ctx), EffectiveTenantID: 1,
		Permission: types.OrgRoleEditor,
	}).Context(ctx)
}

// --- create ---

func TestCreateManualChunkRejectsEmptyAndOversizeContent(t *testing.T) {
	svc, _ := newManualChunkService(t)
	ctx := manualChunkCtx()

	if _, err := svc.CreateManualChunk(ctx, "knowledge", "   ", nil); err == nil {
		t.Fatal("blank content accepted")
	}
	huge := strings.Repeat("a", maxEditableChunkLength+1)
	if _, err := svc.CreateManualChunk(ctx, "knowledge", huge, nil); err == nil {
		t.Fatal("oversize content accepted")
	}
}

func TestCreateManualChunkDefaultsAndPersist(t *testing.T) {
	svc, repo := newManualChunkServiceWithRepo(t)
	ctx := manualChunkCtx()

	created, err := svc.CreateManualChunk(ctx, "knowledge", "  manual note  ", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Content != "manual note" {
		t.Fatalf("content not trimmed: %q", created.Content)
	}
	if !created.IsEnabled {
		t.Fatal("default is_enabled should be true")
	}
	if created.ChunkType != types.ChunkTypeText {
		t.Fatalf("chunk type: %s", created.ChunkType)
	}
	if created.KnowledgeID != "knowledge" || created.KnowledgeBaseID != "kb" {
		t.Fatal("created row not bound to the document")
	}
	if created.ContentRevision != 0 {
		t.Fatalf("first revision must be 0, got %d", created.ContentRevision)
	}
	if len(repo.created) != 1 {
		t.Fatalf("persisted %d rows", len(repo.created))
	}
}

// --- merge ---

func TestMergeChunksRequiresAdjacentSameDocument(t *testing.T) {
	svc, _ := newManualChunkService(t,
		manualTextChunk("a", 1, 0, 10),
		manualTextChunk("b", 2, 10, 20),
		manualTextChunk("far", 9, 90, 99))
	ctx := manualChunkCtx()
	a := "chunk-a"
	far := "chunk-far"

	// non-adjacent merge must be rejected
	if _, err := svc.MergeChunks(ctx, "knowledge", []string{a, far}, nil); err == nil {
		t.Fatal("non-adjacent merge accepted")
	}
	// single chunk is not a merge
	if _, err := svc.MergeChunks(ctx, "knowledge", []string{a}, nil); err == nil {
		t.Fatal("single-chunk merge accepted")
	}
}

func TestMergeChunksConcatenatesAndBumpsRevision(t *testing.T) {
	svc, repo := newManualChunkServiceWithRepo(t,
		manualTextChunk("a", 1, 0, 10),
		manualTextChunk("b", 2, 10, 20),
	)
	ctx := manualChunkCtx()
	a := "chunk-a"
	b := "chunk-b"

	merged, err := svc.MergeChunks(ctx, "knowledge", []string{a, b}, nil)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.ID != a {
		t.Fatalf("survivor should keep the first id, got %s", merged.ID)
	}
	if merged.Content != "a body"+sepNL+sepNL+"b body" {
		t.Fatalf("merged content: %q", merged.Content)
	}
	if merged.ContentRevision != 1 {
		t.Fatal("merge must bump the survivor revision")
	}
	if len(repo.savedRev) != 1 {
		t.Fatalf("expected one revision snapshot, got %d", len(repo.savedRev))
	}
	if len(repo.deletedIDs) != 1 || repo.deletedIDs[0] != b {
		t.Fatalf("absorbed chunks not deleted: %v", repo.deletedIDs)
	}
}

// --- split ---

func TestSplitChunkSplitsBodyAndReindexes(t *testing.T) {
	svc, repo := newManualChunkServiceWithRepo(t,
		manualTextChunk("src", 1, 0, 10),
	)
	requireRowUpdate(t, svc, repo, "chunk-src", "first half"+sepNL+"second half")
	ctx := manualChunkCtx()
	expected := 0

	pair, err := svc.SplitChunk(ctx, "chunk-src", 10, &expected, nil)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if pair[0].ID != "chunk-src" {
		t.Fatal("original must keep its id")
	}
	if pair[0].Content != "first half" || pair[1].Content != "second half" {
		t.Fatalf("split bodies wrong: %q / %q", pair[0].Content, pair[1].Content)
	}
	if pair[0].ContentRevision != 1 {
		t.Fatal("original revision must bump")
	}
	if len(repo.created) != 1 || repo.created[0].ID != pair[1].ID {
		t.Fatal("suffix row not persisted")
	}
}

func TestSplitChunkRejectsBadOffsets(t *testing.T) {
	svc, repo := newManualChunkServiceWithRepo(t,
		manualTextChunk("src", 1, 0, 10),
	)
	ctx := manualChunkCtx()
	requireRowUpdate(t, svc, repo, "chunk-src", "0123456789")
	zero := 0
	full := 10
	if _, err := svc.SplitChunk(ctx, "chunk-src", zero, nil, nil); err == nil {
		t.Fatal("offset 0 accepted")
	}
	if _, err := svc.SplitChunk(ctx, "chunk-src", full, nil, nil); err == nil {
		t.Fatal("offset == length accepted")
	}
}

func manualTextChunk(name string, index, start, end int) *types.Chunk {
	return &types.Chunk{
		ID: "chunk-" + name, TenantID: 1,
		KnowledgeID: "knowledge", KnowledgeBaseID: "kb",
		Content: name + " body", ChunkType: types.ChunkTypeText,
		ChunkIndex: index, StartAt: start, EndAt: end,
		IsEnabled: true, IndexStatus: "ready", ContentRevision: 0,
	}
}

func newManualChunkServiceWithRepo(t *testing.T, rows ...*types.Chunk) (*chunkService, *manualChunkRepo) {
	t.Helper()
	db := newManualChunkDB(t)
	repo := &manualChunkRepo{ChunkRepository: repository.NewChunkRepository(db), db: db}
	for _, row := range rows {
		if row == nil {
			continue
		}
		seedManualChunk(t, db, row)
	}
	svc := &chunkService{
		chunkRepository: repo,
		knowledgeRepo:   manualChunkKnowledgeRepo{},
		kbRepository:    manualChunkKBRepo{},
	}
	return svc, repo
}

func newManualChunkService(t *testing.T, rows ...*types.Chunk) (*chunkService, *manualChunkRepo) {
	t.Helper()
	return newManualChunkServiceWithRepo(t, rows...)
}

type manualChunkKnowledgeRepo struct {
	interfaces.KnowledgeRepository
}

func (manualChunkKnowledgeRepo) GetKnowledgeByID(_ context.Context, _ uint64, id string) (*types.Knowledge, error) {
	return &types.Knowledge{ID: id, TenantID: 1, KnowledgeBaseID: "kb"}, nil
}

type manualChunkKBRepo struct {
	interfaces.KnowledgeBaseRepository
}

func (manualChunkKBRepo) GetKnowledgeBaseByID(_ context.Context, _ string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: "kb", TenantID: 1}, nil
}

// requireRowUpdate overwrites stored content so the service reads it from DB.
func requireRowUpdate(
	t *testing.T, _ *chunkService, repo *manualChunkRepo, id, content string,
) {
	t.Helper()
	if err := repo.db.Model(&types.Chunk{}).Where("id = ?", id).
		Update("content", content).Error; err != nil {
		t.Fatal(err)
	}
}
