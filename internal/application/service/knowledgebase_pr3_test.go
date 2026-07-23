package service

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/storageallowlist"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Vector-store binding service-level tests
//
// Coverage:
//   - CreateKnowledgeBase: vector_store_id validation matrix (Normalize,
//     malformed UUID, sentinel wrap, error code distinction).
//   - CopyKnowledgeBase: embedding-model + store defenses, new-KB
//     VectorStoreID copy.
//
// These are pure logic tests that drive `validateVectorStoreBinding` and
// `CopyKnowledgeBase` through stubbed dependencies — no DB I/O.
// ---------------------------------------------------------------------------

// validKBStoreUUID is a well-formed UUID used to drive the validation
// branches that survive the uuid.Parse() pre-flight.
const validKBStoreUUID = "0193b8a0-1111-7000-8000-000000000001"

type fakeOwnership struct {
	owned map[string]uint64 // storeID -> tenantID owner
	err   error
}

func (f *fakeOwnership) StoreOwnedBy(_ context.Context, storeID string, tenantID uint64) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	owner, ok := f.owned[storeID]
	return ok && owner == tenantID, nil
}

type fakeRegistry struct {
	registered map[string]struct{}
}

func (f *fakeRegistry) Register(_ interfaces.RetrieveEngineService) error { return nil }
func (f *fakeRegistry) GetRetrieveEngineService(_ types.RetrieverEngineType) (interfaces.RetrieveEngineService, error) {
	return nil, nil
}
func (f *fakeRegistry) GetAllRetrieveEngineServices() []interfaces.RetrieveEngineService { return nil }
func (f *fakeRegistry) GetByStoreID(storeID string) (interfaces.RetrieveEngineService, error) {
	if _, ok := f.registered[storeID]; ok {
		return nil, nil
	}
	return nil, stderrors.New("not registered")
}

// fakeKBRepo is the smallest KnowledgeBaseRepository needed by
// CreateKnowledgeBase + CopyKnowledgeBase tests. It stores rows in a map
// keyed by ID. No tenant scoping is applied — the tested paths already
// enforce that explicitly.
type fakeKBRepo struct {
	rows      map[string]*types.KnowledgeBase
	createErr error
}

func newFakeKBRepo() *fakeKBRepo { return &fakeKBRepo{rows: map[string]*types.KnowledgeBase{}} }

func (r *fakeKBRepo) CreateKnowledgeBase(_ context.Context, kb *types.KnowledgeBase) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.rows[kb.ID] = kb
	return nil
}
func (r *fakeKBRepo) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return r.rows[id], nil
}
func (r *fakeKBRepo) GetKnowledgeBaseByIDAndTenant(_ context.Context, id string, tenantID uint64) (*types.KnowledgeBase, error) {
	kb := r.rows[id]
	if kb == nil || kb.TenantID != tenantID {
		return nil, stderrors.New("not found")
	}
	return kb, nil
}
func (r *fakeKBRepo) GetKnowledgeBaseByIDs(_ context.Context, _ []string) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (r *fakeKBRepo) ListKnowledgeBases(_ context.Context) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (r *fakeKBRepo) ListKnowledgeBasesByTenantID(_ context.Context, tenantID uint64) ([]*types.KnowledgeBase, error) {
	rows := make([]*types.KnowledgeBase, 0, len(r.rows))
	for _, kb := range r.rows {
		if kb != nil && kb.TenantID == tenantID {
			rows = append(rows, kb)
		}
	}
	return rows, nil
}
func (r *fakeKBRepo) UpdateKnowledgeBase(_ context.Context, _ *types.KnowledgeBase) error {
	return nil
}
func (r *fakeKBRepo) DeleteKnowledgeBase(_ context.Context, _ string) error { return nil }
func (r *fakeKBRepo) TogglePinKnowledgeBase(_ context.Context, _ string, _ uint64) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (r *fakeKBRepo) CountByVectorStoreID(_ context.Context, _ *gorm.DB, _ uint64, _ string) (int64, error) {
	return 0, nil
}
func (r *fakeKBRepo) CountByModelID(_ context.Context, _ uint64, _ string) (int64, error) {
	return 0, nil
}
func (r *fakeKBRepo) SetUserKBPin(_ context.Context, _ uint64, _ string, _ string, _ bool) (*time.Time, error) {
	return nil, nil
}
func (r *fakeKBRepo) ListUserKBPinIDs(_ context.Context, _ uint64, _ string) (map[string]time.Time, error) {
	return map[string]time.Time{}, nil
}

// Force compile-time conformance check so any future interface change
// surfaces as a build error here rather than at usage site.
var _ interfaces.KnowledgeBaseRepository = (*fakeKBRepo)(nil)

func newPR3KBService(repo *fakeKBRepo, registry *fakeRegistry, ownership *fakeOwnership) *knowledgeBaseService {
	return &knowledgeBaseService{
		repo:           repo,
		retrieveEngine: registry,
		ownership:      ownership,
	}
}

func ctxWithTenant(tenantID uint64) context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
}

func ctxWithTenantStorage(tenantID uint64, defaultProvider string) context.Context {
	ctx := ctxWithTenant(tenantID)
	tenant := &types.Tenant{
		ID: tenantID,
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: defaultProvider,
		},
	}
	return context.WithValue(ctx, types.TenantInfoContextKey, tenant)
}

func TestCreateKnowledgeBase_DefaultStorageProviderFromTenant(t *testing.T) {
	repo := newFakeKBRepo()
	svc := newPR3KBService(repo, &fakeRegistry{registered: map[string]struct{}{}}, &fakeOwnership{})

	kb, err := svc.CreateKnowledgeBase(ctxWithTenantStorage(1, "minio"), &types.KnowledgeBase{Name: "kb"})
	require.NoError(t, err)
	assert.Equal(t, "minio", kb.GetStorageProvider())

	kbExplicit, err := svc.CreateKnowledgeBase(ctxWithTenantStorage(1, "minio"), &types.KnowledgeBase{
		Name:                  "kb2",
		StorageProviderConfig: &types.StorageProviderConfig{Provider: "cos"},
	})
	require.NoError(t, err)
	assert.Equal(t, "cos", kbExplicit.GetStorageProvider())
}

func TestCreateKnowledgeBase_DefaultStorageProviderRespectsAllowList(t *testing.T) {
	t.Setenv(storageallowlist.AllowListEnv, "minio")
	repo := newFakeKBRepo()
	svc := newPR3KBService(repo, &fakeRegistry{registered: map[string]struct{}{}}, &fakeOwnership{})

	kb, err := svc.CreateKnowledgeBase(ctxWithTenantStorage(1, ""), &types.KnowledgeBase{Name: "kb"})
	require.NoError(t, err)
	assert.Equal(t, "minio", kb.GetStorageProvider())

	kbDisallowedDefault, err := svc.CreateKnowledgeBase(ctxWithTenantStorage(1, "local"), &types.KnowledgeBase{Name: "kb2"})
	require.NoError(t, err)
	assert.Equal(t, "minio", kbDisallowedDefault.GetStorageProvider())
}

// ---------------------------------------------------------------------------
// CreateKnowledgeBase — vector_store_id binding validation matrix
// ---------------------------------------------------------------------------

func TestCreateKnowledgeBase_VectorStoreBinding(t *testing.T) {
	registered := map[string]struct{}{validKBStoreUUID: {}}

	t.Run("nil vector_store_id persists unchanged", func(t *testing.T) {
		repo := newFakeKBRepo()
		svc := newPR3KBService(repo,
			&fakeRegistry{registered: registered},
			&fakeOwnership{owned: map[string]uint64{validKBStoreUUID: 1}},
		)
		kb, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{Name: "kb"})
		require.NoError(t, err)
		assert.Nil(t, kb.VectorStoreID)
		assert.Len(t, repo.rows, 1)
	})

	t.Run("empty string vector_store_id normalizes to nil", func(t *testing.T) {
		repo := newFakeKBRepo()
		svc := newPR3KBService(repo,
			&fakeRegistry{registered: registered},
			&fakeOwnership{},
		)
		empty := ""
		kb, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{Name: "kb", VectorStoreID: &empty})
		require.NoError(t, err)
		assert.Nil(t, kb.VectorStoreID)
	})

	t.Run("malformed UUID rejected with BindingInvalid code", func(t *testing.T) {
		repo := newFakeKBRepo()
		svc := newPR3KBService(repo,
			&fakeRegistry{registered: registered},
			&fakeOwnership{},
		)
		bad := "not-a-uuid"
		_, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{Name: "kb", VectorStoreID: &bad})
		require.Error(t, err)
		appErr, ok := apperrors.IsAppError(err)
		require.True(t, ok)
		assert.Equal(t, apperrors.ErrVectorStoreBindingInvalid, appErr.Code)
		assert.NotContains(t, appErr.Message, bad, "UUID must not echo back")
		assert.Empty(t, repo.rows, "must not persist on validation failure")
	})

	t.Run("valid UUID + owned + registered → persist OK", func(t *testing.T) {
		repo := newFakeKBRepo()
		svc := newPR3KBService(repo,
			&fakeRegistry{registered: registered},
			&fakeOwnership{owned: map[string]uint64{validKBStoreUUID: 1}},
		)
		id := validKBStoreUUID
		kb, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{Name: "kb", VectorStoreID: &id})
		require.NoError(t, err)
		require.NotNil(t, kb.VectorStoreID)
		assert.Equal(t, validKBStoreUUID, *kb.VectorStoreID)
	})

	t.Run("valid UUID + cross-tenant → BindingInvalid", func(t *testing.T) {
		repo := newFakeKBRepo()
		svc := newPR3KBService(repo,
			&fakeRegistry{registered: registered},
			&fakeOwnership{owned: map[string]uint64{validKBStoreUUID: 2}}, // owned by tenant 2
		)
		id := validKBStoreUUID
		_, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{Name: "kb", VectorStoreID: &id})
		require.Error(t, err)
		appErr, ok := apperrors.IsAppError(err)
		require.True(t, ok)
		assert.Equal(t, apperrors.ErrVectorStoreBindingInvalid, appErr.Code)
	})

	t.Run("valid UUID + owned but unregistered → Unavailable", func(t *testing.T) {
		repo := newFakeKBRepo()
		svc := newPR3KBService(repo,
			&fakeRegistry{registered: map[string]struct{}{}}, // not registered
			&fakeOwnership{owned: map[string]uint64{validKBStoreUUID: 1}},
		)
		id := validKBStoreUUID
		_, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{Name: "kb", VectorStoreID: &id})
		require.Error(t, err)
		appErr, ok := apperrors.IsAppError(err)
		require.True(t, ok)
		assert.Equal(t, apperrors.ErrVectorStoreUnavailable, appErr.Code)
	})

	t.Run("ownership infra error → 500", func(t *testing.T) {
		repo := newFakeKBRepo()
		svc := newPR3KBService(repo,
			&fakeRegistry{registered: registered},
			&fakeOwnership{err: stderrors.New("db down")},
		)
		id := validKBStoreUUID
		_, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{Name: "kb", VectorStoreID: &id})
		require.Error(t, err)
		appErr, ok := apperrors.IsAppError(err)
		require.True(t, ok)
		assert.Equal(t, 500, appErr.HTTPCode)
	})

	// Compile-time check: the retriever sentinels still match what we expect
	// in the wrapper logic. If they are ever renamed or removed, this fails
	// at build time rather than silently degrading the wrap.
	_ = retriever.ErrVectorStoreForbidden
	_ = retriever.ErrVectorStoreNotFound
}

// ---------------------------------------------------------------------------
// CopyKnowledgeBase — embedding model + vector store defenses
// ---------------------------------------------------------------------------

func TestCopyKnowledgeBase_Defenses(t *testing.T) {
	mkKB := func(id string, tenant uint64, embed string, vsid *string) *types.KnowledgeBase {
		return &types.KnowledgeBase{
			ID: id, Name: id, TenantID: tenant, EmbeddingModelID: embed, VectorStoreID: vsid,
		}
	}
	storeA := "store-A"
	storeB := "store-B"

	t.Run("dstKB empty -> new KB inherits VectorStoreID", func(t *testing.T) {
		repo := newFakeKBRepo()
		repo.rows["src"] = mkKB("src", 1, "embed-1", &storeA)
		svc := newPR3KBService(repo, &fakeRegistry{}, &fakeOwnership{})

		src, tgt, err := svc.CopyKnowledgeBase(ctxWithTenant(1), "src", "")
		require.NoError(t, err)
		require.NotNil(t, src.VectorStoreID)
		require.NotNil(t, tgt.VectorStoreID)
		assert.Equal(t, *src.VectorStoreID, *tgt.VectorStoreID, "M17: VectorStoreID preserved")

		// Reload from fake repo and re-verify
		stored := repo.rows[tgt.ID]
		require.NotNil(t, stored)
		require.NotNil(t, stored.VectorStoreID)
		assert.Equal(t, storeA, *stored.VectorStoreID)
	})

	t.Run("dstKB empty + source nil VectorStoreID -> new target nil too", func(t *testing.T) {
		repo := newFakeKBRepo()
		repo.rows["src"] = mkKB("src", 1, "embed-1", nil)
		svc := newPR3KBService(repo, &fakeRegistry{}, &fakeOwnership{})

		_, tgt, err := svc.CopyKnowledgeBase(ctxWithTenant(1), "src", "")
		require.NoError(t, err)
		assert.Nil(t, tgt.VectorStoreID)
	})

	t.Run("dstKB set + same embedding model + both nil stores -> OK", func(t *testing.T) {
		repo := newFakeKBRepo()
		repo.rows["src"] = mkKB("src", 1, "embed-1", nil)
		repo.rows["dst"] = mkKB("dst", 1, "embed-1", nil)
		svc := newPR3KBService(repo, &fakeRegistry{}, &fakeOwnership{})

		_, _, err := svc.CopyKnowledgeBase(ctxWithTenant(1), "src", "dst")
		require.NoError(t, err)
	})

	t.Run("dstKB set + same store UUID -> OK", func(t *testing.T) {
		repo := newFakeKBRepo()
		repo.rows["src"] = mkKB("src", 1, "embed-1", &storeA)
		repo.rows["dst"] = mkKB("dst", 1, "embed-1", &storeA)
		svc := newPR3KBService(repo, &fakeRegistry{}, &fakeOwnership{})

		_, _, err := svc.CopyKnowledgeBase(ctxWithTenant(1), "src", "dst")
		require.NoError(t, err)
	})

	t.Run("dstKB set + different embedding models -> 400", func(t *testing.T) {
		repo := newFakeKBRepo()
		repo.rows["src"] = mkKB("src", 1, "embed-1", &storeA)
		repo.rows["dst"] = mkKB("dst", 1, "embed-2", &storeA)
		svc := newPR3KBService(repo, &fakeRegistry{}, &fakeOwnership{})

		_, _, err := svc.CopyKnowledgeBase(ctxWithTenant(1), "src", "dst")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "different embedding models")
	})

	t.Run("dstKB set + different stores -> 400 (no Phase number)", func(t *testing.T) {
		repo := newFakeKBRepo()
		repo.rows["src"] = mkKB("src", 1, "embed-1", &storeA)
		repo.rows["dst"] = mkKB("dst", 1, "embed-1", &storeB)
		svc := newPR3KBService(repo, &fakeRegistry{}, &fakeOwnership{})

		_, _, err := svc.CopyKnowledgeBase(ctxWithTenant(1), "src", "dst")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "different vector stores")
		assert.NotContains(t, err.Error(), "Phase 4", "internal roadmap labels must not leak to end-user error messages")
	})

	t.Run("dstKB set + one nil + one set -> 400", func(t *testing.T) {
		repo := newFakeKBRepo()
		repo.rows["src"] = mkKB("src", 1, "embed-1", nil)
		repo.rows["dst"] = mkKB("dst", 1, "embed-1", &storeA)
		svc := newPR3KBService(repo, &fakeRegistry{}, &fakeOwnership{})

		_, _, err := svc.CopyKnowledgeBase(ctxWithTenant(1), "src", "dst")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "different vector stores")
	})
}

func TestDuplicateKnowledgeBase_CreatesSettingsOnlyDuplicate(t *testing.T) {
	repo := newFakeKBRepo()
	source := &types.KnowledgeBase{
		ID:          "src",
		Name:        "Source KB",
		Type:        types.KnowledgeBaseTypeDocument,
		Description: "source description",
		TenantID:    1,
		CreatorID:   "source-owner",
		IsTemporary: true,
		ChunkingConfig: types.ChunkingConfig{
			ChunkSize:    800,
			ChunkOverlap: 80,
			Separators:   []string{"\n\n", "."},
			ParserEngineRules: []types.ParserEngineRule{{
				FileTypes: []string{"pdf"},
				Engine:    "docreader",
			}},
		},
		ImageProcessingConfig: types.ImageProcessingConfig{ModelID: "vlm-image"},
		EmbeddingModelID:      "embed-1",
		SummaryModelID:        "summary-1",
		VLMConfig:             types.VLMConfig{Enabled: true, ModelID: "vlm-1"},
		ASRConfig:             types.ASRConfig{Enabled: true, ModelID: "asr-1", Language: "zh"},
		StorageProviderConfig: &types.StorageProviderConfig{Provider: "local"},
		ExtractConfig: &types.ExtractConfig{
			Enabled: true,
			Text:    "extract entities",
			Tags:    []string{"product"},
			Nodes:   []*types.GraphNode{{Name: "Product"}},
		},
		QuestionGenerationConfig: &types.QuestionGenerationConfig{Enabled: true, QuestionCount: 5},
		WikiConfig:               &types.WikiConfig{SynthesisModelID: "wiki-llm", MaxPagesPerIngest: 12},
		IndexingStrategy: types.IndexingStrategy{
			VectorEnabled:  true,
			KeywordEnabled: true,
			WikiEnabled:    true,
			GraphEnabled:   true,
		},
		IsPinned:        true,
		KnowledgeCount:  9,
		ChunkCount:      27,
		IsProcessing:    true,
		ProcessingCount: 2,
		ShareCount:      3,
		CreatorName:     "Source Owner",
	}
	repo.rows["src"] = source
	svc := newPR3KBService(repo, &fakeRegistry{}, &fakeOwnership{})
	ctx := context.WithValue(ctxWithTenant(1), types.UserIDContextKey, "copy-user")

	target, err := svc.DuplicateKnowledgeBase(ctx, "src")
	require.NoError(t, err)

	require.NotEmpty(t, target.ID)
	assert.Equal(t, uint64(1), target.TenantID)
	assert.Equal(t, "copy-user", target.CreatorID)
	assert.Equal(t, "Source KB 副本", target.Name)
	assert.Equal(t, source.Description, target.Description)
	assert.Equal(t, source.ChunkingConfig, target.ChunkingConfig)
	assert.Equal(t, source.ImageProcessingConfig, target.ImageProcessingConfig)
	assert.Equal(t, source.EmbeddingModelID, target.EmbeddingModelID)
	assert.Equal(t, source.SummaryModelID, target.SummaryModelID)
	assert.Equal(t, source.VLMConfig, target.VLMConfig)
	assert.Equal(t, source.ASRConfig, target.ASRConfig)
	require.NotNil(t, target.StorageProviderConfig)
	assert.Equal(t, "local", target.StorageProviderConfig.Provider)
	require.NotNil(t, target.ExtractConfig)
	assert.Equal(t, source.ExtractConfig.Text, target.ExtractConfig.Text)
	require.NotNil(t, target.QuestionGenerationConfig)
	assert.Equal(t, 5, target.QuestionGenerationConfig.QuestionCount)
	require.NotNil(t, target.WikiConfig)
	assert.Equal(t, "wiki-llm", target.WikiConfig.SynthesisModelID)
	assert.Equal(t, source.IndexingStrategy, target.IndexingStrategy)

	assert.False(t, target.IsTemporary)
	assert.False(t, target.IsPinned)
	assert.Nil(t, target.PinnedAt)
	assert.Zero(t, target.KnowledgeCount)
	assert.Zero(t, target.ChunkCount)
	assert.False(t, target.IsProcessing)
	assert.Zero(t, target.ProcessingCount)
	assert.Zero(t, target.ShareCount)
	assert.Empty(t, target.CreatorName)
	require.Same(t, target, repo.rows[target.ID])
}

func TestDuplicateKnowledgeBase_UsesDistinctNameWhenDuplicateExists(t *testing.T) {
	repo := newFakeKBRepo()
	repo.rows["src"] = &types.KnowledgeBase{
		ID:       "src",
		Name:     "Source KB",
		Type:     types.KnowledgeBaseTypeDocument,
		TenantID: 1,
	}
	repo.rows["existing-copy"] = &types.KnowledgeBase{
		ID:       "existing-copy",
		Name:     "Source KB 副本",
		Type:     types.KnowledgeBaseTypeDocument,
		TenantID: 1,
	}
	svc := newPR3KBService(repo, &fakeRegistry{}, &fakeOwnership{})

	target, err := svc.DuplicateKnowledgeBase(ctxWithTenant(1), "src")
	require.NoError(t, err)

	assert.Equal(t, "Source KB 副本 2", target.Name)
}

func TestDuplicateKnowledgeBase_UsesLocalizedEnglishSuffix(t *testing.T) {
	repo := newFakeKBRepo()
	repo.rows["src"] = &types.KnowledgeBase{
		ID:       "src",
		Name:     "Source KB",
		Type:     types.KnowledgeBaseTypeDocument,
		TenantID: 1,
	}
	svc := newPR3KBService(repo, &fakeRegistry{}, &fakeOwnership{})
	ctx := context.WithValue(ctxWithTenant(1), types.LanguageContextKey, "en-US")

	target, err := svc.DuplicateKnowledgeBase(ctx, "src")
	require.NoError(t, err)

	assert.Equal(t, "Source KB Copy", target.Name)
}
