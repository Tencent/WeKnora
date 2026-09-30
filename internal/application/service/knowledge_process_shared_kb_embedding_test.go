package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// sharedKBModelService stands in for a deployment where each tenant owns its own
// model rows. ownerTenantID owns embeddingModelID, so the plain ctx-tenant
// lookup reports "Model not found" for anyone else — which is exactly the
// failure a shared knowledge base used to hit, and which the two counters make
// observable.
type sharedKBModelService struct {
	interfaces.ModelService
	embedder        embedding.Embedder
	ownerTenantID   uint64
	embeddingModel  string
	ctxLookups      []uint64
	ownerLookups    []uint64
	ownerLookupErrs error
}

func (s *sharedKBModelService) GetEmbeddingModel(
	ctx context.Context, modelID string,
) (embedding.Embedder, error) {
	tenantID, _ := types.TenantIDFromContext(ctx)
	s.ctxLookups = append(s.ctxLookups, tenantID)
	if tenantID != s.ownerTenantID {
		return nil, errors.New("Model not found")
	}
	return s.embedder, nil
}

func (s *sharedKBModelService) GetEmbeddingModelForTenant(
	_ context.Context, _ string, tenantID uint64,
) (embedding.Embedder, error) {
	s.ownerLookups = append(s.ownerLookups, tenantID)
	if tenantID != s.ownerTenantID {
		return nil, errors.New("Model not found")
	}
	if s.ownerLookupErrs != nil {
		return nil, s.ownerLookupErrs
	}
	return s.embedder, nil
}

// sharedKBService wires the same collaborators as the other processChunks
// tests, with a model service that only knows the owning tenant's row.
func sharedKBService(
	knowledge *types.Knowledge, modelSvc *sharedKBModelService,
) (*knowledgeService, *parentChildChunkService, *parentChildRetrieveEngine) {
	chunkService := &parentChildChunkService{}
	retrieveEngine := &parentChildRetrieveEngine{}
	svc := &knowledgeService{
		repo:           &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkRepo:      chunkService,
		modelService:   modelSvc,
		retrieveEngine: parentChildRetrieveRegistry{engine: retrieveEngine},
		graphEngine:    parentChildGraphRepo{},
		tenantRepo:     parentChildTenantRepo{},
		task:           parentChildTaskEnqueuer{},
	}
	return svc, chunkService, retrieveEngine
}

// sharedKBContext is the request context a viewer of a shared knowledge base
// carries: the owner's tenant in the knowledge row, the viewer's in ctx. The
// tenant object the vector store is resolved from belongs to the viewer too —
// the model row is the only thing that lives with the owner.
func sharedKBContext(viewerTenant uint64) context.Context {
	tenant := &types.Tenant{
		ID: viewerTenant,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{
			{
				RetrieverType:       types.VectorRetrieverType,
				RetrieverEngineType: types.PostgresRetrieverEngineType,
			},
		}},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, viewerTenant)
	return context.WithValue(ctx, types.TenantInfoContextKey, tenant)
}

// A knowledge base shared into a shared space is written by the owning tenant
// but parsed with the viewer's tenant in ctx. The embedding model belongs to
// the owner, so resolving it by ctx alone reported "Model not found" and
// processChunks failed the document — leaving every document in the shared
// knowledge base unsearchable for anyone but its owner.
func TestProcessChunksIndexesSharedKnowledgeBaseUnderOwningTenant(t *testing.T) {
	const (
		ownerTenant  uint64 = 10000
		viewerTenant uint64 = 10001
	)
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        ownerTenant,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
	}
	modelSvc := &sharedKBModelService{
		embedder:       parentChildEmbedder{},
		ownerTenantID:  ownerTenant,
		embeddingModel: "embedding-1",
	}
	svc, chunkService, retrieveEngine := sharedKBService(knowledge, modelSvc)
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         ownerTenant,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}

	svc.processChunks(sharedKBContext(viewerTenant), kb, knowledge,
		[]types.ParsedChunk{{Content: "shared body", Seq: 0, Start: 0, End: 11}})

	require.Equal(t, []uint64{ownerTenant}, modelSvc.ownerLookups,
		"a shared knowledge base must resolve its embedding model under the owning tenant")
	require.Empty(t, modelSvc.ctxLookups,
		"the viewer's tenant does not own the model row, so the ctx lookup must not be reached")
	require.Len(t, chunkService.created, 1, "the document must be chunked, not failed")
	require.Len(t, retrieveEngine.indexed, 1, "and indexed, so the viewer can search it")
}

// The owning tenant processing its own knowledge base is the ordinary path and
// must keep using the plain ctx-tenant lookup, so nothing changes where there
// is no cross-tenant sharing involved.
func TestProcessChunksKeepsCtxLookupForOwnKnowledgeBase(t *testing.T) {
	const ownerTenant uint64 = 10000
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        ownerTenant,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
	}
	modelSvc := &sharedKBModelService{
		embedder:       parentChildEmbedder{},
		ownerTenantID:  ownerTenant,
		embeddingModel: "embedding-1",
	}
	svc, chunkService, _ := sharedKBService(knowledge, modelSvc)
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         ownerTenant,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}

	svc.processChunks(sharedKBContext(ownerTenant), kb, knowledge,
		[]types.ParsedChunk{{Content: "owned body", Seq: 0, Start: 0, End: 11}})

	require.Equal(t, []uint64{ownerTenant}, modelSvc.ctxLookups,
		"an owned knowledge base still resolves through the ctx tenant")
	require.Empty(t, modelSvc.ownerLookups,
		"the cross-tenant branch is for shared knowledge bases only")
	require.Len(t, chunkService.created, 1)
}

// The helper is shared by every processing stage, so pin the branch itself and
// not only its effect through processChunks: a provider failure on the owner
// side must still surface, and a ctx without a tenant must not panic here —
// the model service reports that on its own terms, as it did before.
func TestResolveKBEmbeddingModel(t *testing.T) {
	const (
		ownerTenant  uint64 = 10000
		viewerTenant uint64 = 10001
	)
	shared := &types.KnowledgeBase{
		ID: "kb-1", TenantID: ownerTenant, EmbeddingModelID: "embedding-1",
	}
	owned := &types.KnowledgeBase{
		ID: "kb-2", TenantID: ownerTenant, EmbeddingModelID: "embedding-1",
	}
	newSvc := func() (*knowledgeService, *sharedKBModelService) {
		modelSvc := &sharedKBModelService{
			embedder: parentChildEmbedder{}, ownerTenantID: ownerTenant, embeddingModel: "embedding-1",
		}
		return &knowledgeService{modelService: modelSvc}, modelSvc
	}

	t.Run("shared knowledge base resolves under the owner", func(t *testing.T) {
		svc, modelSvc := newSvc()
		embedder, err := svc.resolveKBEmbeddingModel(sharedKBContext(viewerTenant), shared)
		require.NoError(t, err)
		require.NotNil(t, embedder)
		require.Equal(t, []uint64{ownerTenant}, modelSvc.ownerLookups)
		require.Empty(t, modelSvc.ctxLookups)
	})

	t.Run("owned knowledge base resolves through ctx", func(t *testing.T) {
		svc, modelSvc := newSvc()
		embedder, err := svc.resolveKBEmbeddingModel(sharedKBContext(ownerTenant), owned)
		require.NoError(t, err)
		require.NotNil(t, embedder)
		require.Equal(t, []uint64{ownerTenant}, modelSvc.ctxLookups)
		require.Empty(t, modelSvc.ownerLookups)
	})

	t.Run("a missing tenant is the model service's error to report", func(t *testing.T) {
		svc, modelSvc := newSvc()
		_, err := svc.resolveKBEmbeddingModel(context.Background(), shared)
		require.Error(t, err)
		require.Empty(t, modelSvc.ownerLookups)
	})

	t.Run("an owner-side provider failure still surfaces", func(t *testing.T) {
		svc, modelSvc := newSvc()
		modelSvc.ownerLookupErrs = errors.New("dial tcp: connection refused")
		_, err := svc.resolveKBEmbeddingModel(sharedKBContext(viewerTenant), shared)
		require.ErrorContains(t, err, "connection refused")
	})
}
