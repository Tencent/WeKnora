package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type hybridSearchTagRepo struct {
	interfaces.KnowledgeRepository
	lookup func(context.Context, uint64, string, []string) ([]string, error)
}

type hybridSearchTagKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r *hybridSearchTagKBRepo) GetKnowledgeBaseByIDs(context.Context, []string) ([]*types.KnowledgeBase, error) {
	return []*types.KnowledgeBase{r.kb}, nil
}

func (r *hybridSearchTagRepo) ListIDsByTagIDs(
	ctx context.Context, tenantID uint64, kbID string, tagIDs []string,
) ([]string, error) {
	return r.lookup(ctx, tenantID, kbID, tagIDs)
}

func TestBuildRetrievalParamsDocumentTags(t *testing.T) {
	lookupErr := errors.New("tag lookup failed")
	for _, tc := range []struct {
		name        string
		tags        []string
		explicitIDs []string
		matchedIDs  []string
		wantIDs     []string
		lookupErr   error
		wantEmpty   bool
		vectorOff   bool
		keywordsOff bool
	}{
		{
			name: "document tags scope both retrievers", tags: []string{"tag-a", "tag-b"},
			matchedIDs: []string{"doc-a", "doc-b", "doc-a"}, wantIDs: []string{"doc-a", "doc-b"},
		},
		{
			name: "intersect explicit documents", tags: []string{"tag-a"},
			explicitIDs: []string{"doc-b", "untagged"}, matchedIDs: []string{"doc-a", "doc-b"},
			wantIDs: []string{"doc-b"},
		},
		{name: "no tagged documents", tags: []string{"missing"}, wantEmpty: true},
		{
			name: "disjoint explicit scope", tags: []string{"tag-a"},
			explicitIDs: []string{"untagged"}, matchedIDs: []string{"doc-a"}, wantEmpty: true,
		},
		{name: "lookup error", tags: []string{"tag-a"}, lookupErr: lookupErr},
		{name: "no tags preserves scope", explicitIDs: []string{"untagged"}, wantIDs: []string{"untagged"}},
		{
			name: "keyword only", tags: []string{"tag-a"}, vectorOff: true,
			matchedIDs: []string{"doc-a"}, wantIDs: []string{"doc-a"},
		},
		{
			name: "vector only", tags: []string{"tag-a"}, keywordsOff: true,
			matchedIDs: []string{"doc-a"}, wantIDs: []string{"doc-a"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := &knowledgeBaseService{kgRepo: &hybridSearchTagRepo{
				lookup: func(_ context.Context, tenantID uint64, kbID string, tags []string) ([]string, error) {
					calls++
					// A shared KB must resolve tags using its owner, not the caller (tenant 1).
					require.Equal(t, uint64(42), tenantID)
					require.Equal(t, "docs", kbID)
					require.Equal(t, tc.tags, tags)
					return tc.matchedIDs, tc.lookupErr
				},
			}}
			engine := buildBoundComposite(t, &fakeRetrieveEngineService{
				engineType: types.PostgresRetrieverEngineType,
				support:    []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType},
			})
			kb := &types.KnowledgeBase{
				ID: "docs", TenantID: 42, Type: types.KnowledgeBaseTypeDocument,
				EmbeddingModelID: "embedding",
				IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, KeywordEnabled: true},
			}
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
			params, err := s.buildRetrievalParams(ctx, engine, kb, []*types.KnowledgeBase{kb},
				types.SearchParams{
					QueryText: "query", QueryEmbedding: []float32{1}, TagIDs: tc.tags,
					KnowledgeIDs: tc.explicitIDs, DisableVectorMatch: tc.vectorOff,
					DisableKeywordsMatch: tc.keywordsOff,
				}, 5)
			if tc.lookupErr != nil {
				require.ErrorIs(t, err, tc.lookupErr)
				require.Empty(t, params)
				return
			}
			require.NoError(t, err)
			if len(tc.tags) > 0 {
				require.Equal(t, 1, calls)
			} else {
				require.Zero(t, calls)
			}
			if tc.wantEmpty {
				// Empty KnowledgeIDs mean unrestricted retrieval, so neither backend may run.
				require.Empty(t, params)
				return
			}
			wantCount := 2
			if tc.vectorOff || tc.keywordsOff {
				wantCount = 1
			}
			require.Len(t, params, wantCount)
			for _, p := range params {
				require.ElementsMatch(t, tc.wantIDs, p.KnowledgeIDs)
				require.Empty(t, p.TagIDs, "document tags must not filter the FAQ chunk tag field")
				require.Equal(t, []string{"docs"}, p.KnowledgeBaseIDs)
				require.Equal(t, 5, p.TopK)
			}
		})
	}
}

func TestResolveStoreGroupsDocumentTagsPreserveFAQScope(t *testing.T) {
	const localStore = "00000000-0000-0000-0000-000000000001"
	const sharedStore = "00000000-0000-0000-0000-000000000002"
	newKB := func(id string, owner uint64, store string, kind string) *types.KnowledgeBase {
		return &types.KnowledgeBase{
			ID: id, TenantID: owner, VectorStoreID: &store, Type: kind, EmbeddingModelID: "embedding",
			IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, KeywordEnabled: true},
		}
	}
	kbs := []*types.KnowledgeBase{
		newKB("local-docs", 1, localStore, types.KnowledgeBaseTypeDocument),
		newKB("no-matches", 1, localStore, types.KnowledgeBaseTypeDocument),
		newKB("shared-docs", 42, sharedStore, types.KnowledgeBaseTypeDocument),
		newKB("shared-faq", 42, sharedStore, types.KnowledgeBaseTypeFAQ),
	}
	engine := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType},
	}
	lookedUp := make(map[string]uint64)
	s := &knowledgeBaseService{
		retrieveEngine: &fakeFanoutRegistry{byStore: map[string]interfaces.RetrieveEngineService{
			localStore: engine, sharedStore: engine,
		}},
		ownership: &fakeOwnership{owned: map[string]uint64{localStore: 1, sharedStore: 42}},
		kgRepo: &hybridSearchTagRepo{
			lookup: func(_ context.Context, owner uint64, kbID string, tags []string) ([]string, error) {
				lookedUp[kbID] = owner
				require.Equal(t, []string{"tag-a", "tag-b"}, tags)
				switch kbID {
				case "local-docs":
					return []string{"local-document", "excluded-document"}, nil
				case "shared-docs":
					return []string{"shared-document"}, nil
				default:
					return nil, nil
				}
			},
		},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	search := types.SearchParams{
		QueryText: "query", QueryEmbedding: []float32{1}, TagIDs: []string{"tag-a", "tag-b"},
		KnowledgeIDs: []string{"local-document", "shared-document", "faq-entry"},
	}
	groups, err := s.resolveStoreGroups(ctx, kbs[0], kbs, search, 5)
	require.NoError(t, err)
	require.Len(t, groups, 2)
	require.Equal(t, map[string]uint64{"local-docs": 1, "no-matches": 1, "shared-docs": 42}, lookedUp)
	for _, g := range groups {
		g.TopK = 100 // FAQ retries must retain the resolved document scope.
		params := paramsWithTopK(g)
		if g.OwnerTenantID == 1 {
			require.Len(t, params, 2)
		} else {
			require.Len(t, params, 3)
		}
		for _, p := range params {
			require.Equal(t, 100, p.TopK)
			if p.KnowledgeType == types.KnowledgeTypeFAQ {
				require.Equal(t, []string{"shared-faq"}, p.KnowledgeBaseIDs)
				require.Equal(t, search.TagIDs, p.TagIDs)
				require.Equal(t, search.KnowledgeIDs, p.KnowledgeIDs)
				continue
			}
			require.Empty(t, p.TagIDs)
			if g.OwnerTenantID == 1 {
				require.Equal(t, []string{"local-docs"}, p.KnowledgeBaseIDs)
				require.Equal(t, []string{"local-document"}, p.KnowledgeIDs)
			} else {
				require.Equal(t, []string{"shared-docs"}, p.KnowledgeBaseIDs)
				require.Equal(t, []string{"shared-document"}, p.KnowledgeIDs)
			}
		}
	}
}

func TestBuildRetrievalParamsDocumentTagsFromRelations(t *testing.T) {
	db := setupKnowledgeSharedAccessDB(t)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeTagRelation{}))
	for _, doc := range []*types.Knowledge{
		{ID: "doc-a", TenantID: 42, KnowledgeBaseID: "docs"},
		{ID: "doc-b", TenantID: 42, KnowledgeBaseID: "docs"},
		{ID: "untagged", TenantID: 42, KnowledgeBaseID: "docs"},
		{ID: "other-kb", TenantID: 42, KnowledgeBaseID: "other"},
		{ID: "other-tenant", TenantID: 1, KnowledgeBaseID: "docs"},
	} {
		seedKnowledge(t, db, doc)
	}
	require.NoError(t, db.Create(&[]types.KnowledgeTagRelation{
		{KnowledgeID: "doc-a", TagID: "tag-a"},
		{KnowledgeID: "doc-a", TagID: "tag-b"},
		{KnowledgeID: "doc-b", TagID: "tag-b"},
		{KnowledgeID: "other-kb", TagID: "tag-a"},
		{KnowledgeID: "other-tenant", TagID: "tag-a"},
	}).Error)
	s := &knowledgeBaseService{kgRepo: repository.NewKnowledgeRepository(db)}
	engine := buildBoundComposite(t, &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.KeywordsRetrieverType},
	})
	kb := &types.KnowledgeBase{
		ID: "docs", TenantID: 42, Type: types.KnowledgeBaseTypeDocument,
		IndexingStrategy: types.IndexingStrategy{KeywordEnabled: true},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	params, err := s.buildRetrievalParams(ctx, engine, kb, []*types.KnowledgeBase{kb},
		types.SearchParams{TagIDs: []string{"tag-a", "tag-b"}}, 5)
	require.NoError(t, err)
	require.Len(t, params, 1)
	require.ElementsMatch(t, []string{"doc-a", "doc-b"}, params[0].KnowledgeIDs)
	require.Empty(t, params[0].TagIDs)
}

func TestHybridSearchDocumentTagsRequireKBAccess(t *testing.T) {
	const storeID = "00000000-0000-0000-0000-000000000002"
	store := storeID
	kb := &types.KnowledgeBase{
		ID: "shared-docs", TenantID: 42, VectorStoreID: &store,
		Type:             types.KnowledgeBaseTypeDocument,
		IndexingStrategy: types.IndexingStrategy{KeywordEnabled: true},
	}
	lookups := 0
	engine := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.KeywordsRetrieverType},
	}
	shares := &fakeKBShareService{allowedKBs: map[string]bool{}}
	s := &knowledgeBaseService{
		repo: &hybridSearchTagKBRepo{kb: kb},
		kgRepo: &hybridSearchTagRepo{
			lookup: func(_ context.Context, owner uint64, kbID string, _ []string) ([]string, error) {
				lookups++
				require.Equal(t, kb.TenantID, owner)
				require.Equal(t, kb.ID, kbID)
				return nil, nil
			},
		},
		kbShareService: shares,
		retrieveEngine: &fakeFanoutRegistry{byStore: map[string]interfaces.RetrieveEngineService{storeID: engine}},
		ownership:      &fakeOwnership{owned: map[string]uint64{storeID: 42}},
	}
	ctx := newSharedAccessContext()
	search := types.SearchParams{TagIDs: []string{"missing"}, MatchCount: 5}
	_, err := s.hybridSearchCandidates(ctx, kb.ID, search)
	require.Error(t, err)
	require.Zero(t, lookups, "authorization must run before looking up tags in another tenant")
	shares.allowedKBs[kb.ID] = true
	results, err := s.hybridSearchCandidates(ctx, kb.ID, search)
	require.NoError(t, err)
	require.Empty(t, results)
	require.Equal(t, 1, lookups)
	require.Zero(t, engine.retrieveCalls.Load(), "no matching tags must not become an unrestricted search")
}
