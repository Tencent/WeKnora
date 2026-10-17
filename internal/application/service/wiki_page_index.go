package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// NewIndexedWikiPageService wires page writes to the KB's ordinary retrieval engines.
// Keep the storage-only constructor available for consumers that do not configure retrieval.
func NewIndexedWikiPageService(
	repo interfaces.WikiPageRepository,
	chunkRepo interfaces.ChunkRepository,
	kbService interfaces.KnowledgeBaseService,
	taskPendingRepo interfaces.TaskPendingOpsRepository,
	redisClient *redis.Client,
	kbShareService interfaces.KBShareService,
	modelService interfaces.ModelService,
	registry interfaces.RetrieveEngineRegistry,
	ownership retriever.TenantStoreOwnership,
	tenantService interfaces.TenantService,
) interfaces.WikiPageService {
	s := NewWikiPageService(repo, chunkRepo, kbService, taskPendingRepo, redisClient, kbShareService).(*wikiPageService)
	s.pageIndex = &wikiPageIndex{chunkRepo, kbService, modelService, registry, ownership, tenantService}
	return s
}

type wikiPageIndex struct {
	chunks    interfaces.ChunkRepository
	kbs       interfaces.KnowledgeBaseService
	models    interfaces.ModelService
	registry  interfaces.RetrieveEngineRegistry
	ownership retriever.TenantStoreOwnership
	tenants   interfaces.TenantService
}

func (s *wikiPageService) syncPageIndex(ctx context.Context, page *types.WikiPage) error {
	if s.pageIndex == nil {
		return nil
	}
	return s.pageIndex.sync(ctx, page, false)
}

func wikiPageKnowledgeID(pageID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("wiki-page:"+pageID)).String()
}

// sync replaces a page's retrieval artifacts. Derived UUIDs form a separate knowledge
// scope: synthesized pages are never attributed to an arbitrary source document,
// and are not inserted as documents (which would feed them back into wiki ingest).
func (w *wikiPageIndex) sync(ctx context.Context, page *types.WikiPage, remove bool) error {
	kb, err := w.kbs.GetKnowledgeBaseByIDOnly(ctx, page.KnowledgeBaseID)
	if err != nil {
		return err
	}
	if kb.TenantID != page.TenantID {
		return fmt.Errorf("wiki page knowledge base tenant mismatch")
	}
	ctx = types.WithExecutionTenant(ctx, page.TenantID)
	knowledgeID := wikiPageKnowledgeID(page.ID)
	old, err := w.chunks.ListWikiPageChunks(ctx, page.TenantID, knowledgeID)
	if err != nil {
		return err
	}
	eligible := !remove && kb.IsWikiEnabled() && kb.NeedsEmbeddingModel() &&
		page.Status == types.WikiPageStatusPublished &&
		page.PageType != types.WikiPageTypeIndex && strings.TrimSpace(page.Content) != ""
	if !eligible && len(old) == 0 {
		return nil
	}
	ids := make([]string, 0, len(old))
	for _, c := range old {
		ids = append(ids, c.ID)
	}
	// Hide stale hits even when a provider/store fails before replacement. A
	// later write (including a retry with unchanged content) retries the sync.
	if err := w.chunks.UpdateChunkFieldsByIDs(ctx, page.TenantID, ids,
		map[string]interface{}{"index_status": "processing"}); err != nil {
		return err
	}
	// Async wiki workers need not carry TenantInfo. Resolve the page owner's
	// engines explicitly instead of borrowing a shared caller's workspace.
	var engine *retriever.CompositeRetrieveEngine
	if kb.VectorStoreID != nil && *kb.VectorStoreID != "" {
		engine, err = retriever.CreateRetrieveEngineForKB(ctx, w.registry, w.ownership, page.TenantID, kb.VectorStoreID)
	} else {
		var tenant *types.Tenant
		tenant, err = w.tenants.GetTenantByID(ctx, page.TenantID)
		if err == nil {
			engine, err = retriever.NewCompositeRetrieveEngine(w.registry, tenant.GetEffectiveEngines())
		}
	}
	if err != nil {
		return err
	}
	if err := deleteWikiPageIndex(ctx, w.chunks, engine, page.TenantID, knowledgeID, old); err != nil {
		return err
	}
	if !eligible {
		return nil
	}
	var embedder embedding.Embedder
	dimension := 0
	if kb.NeedsEmbeddingModel() {
		embedder, err = w.models.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			return err
		}
		dimension = embedder.GetDimensions()
	}
	parts := chunker.Split(page.Content, buildSplitterConfigFromChunking(kb.ChunkingConfig))
	chunks := make([]*types.Chunk, 0, len(parts))
	infos := make([]*types.IndexInfo, 0, len(parts))
	metadata, err := json.Marshal(map[string]string{
		"wiki_page_id": page.ID, "wiki_slug": page.Slug,
		"wiki_index_dimension": strconv.Itoa(dimension), "wiki_index_type": kb.Type,
	})
	if err != nil {
		return err
	}
	for _, part := range parts {
		// Superseded derived rows have been physically purged before replacement.
		id := uuid.NewString()
		c := &types.Chunk{
			ID: id, TenantID: page.TenantID, KnowledgeBaseID: page.KnowledgeBaseID, KnowledgeID: knowledgeID,
			Content: part.Content, ContextHeader: page.Title, ChunkType: types.ChunkTypeWikiPage,
			ChunkIndex: part.Seq, StartAt: part.Start, EndAt: part.End, IsEnabled: true,
			IndexStatus: "processing", Status: int(types.ChunkStatusStored), Metadata: metadata,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		chunks = append(chunks, c)
		infos = append(infos, &types.IndexInfo{
			Content: c.EmbeddingContent(), SourceID: id, SourceType: types.ChunkSourceType,
			ChunkID: id, KnowledgeID: knowledgeID, KnowledgeBaseID: page.KnowledgeBaseID,
			KnowledgeType: kb.Type, IsEnabled: true,
		})
	}
	if err := w.chunks.CreateChunks(ctx, chunks); err != nil {
		return err
	}
	if err := engine.BatchIndex(ctx, embedder, infos); err != nil {
		return err
	}
	ids = ids[:0]
	for _, c := range chunks {
		ids = append(ids, c.ID)
	}
	return w.chunks.UpdateChunkFieldsByIDs(ctx, page.TenantID, ids,
		map[string]interface{}{"index_status": "ready", "status": types.ChunkStatusIndexed})
}

// deleteWikiPageIndex uses the dimensions captured at indexing time. Cleanup
// still works after the model has changed or been removed from the workspace.
func deleteWikiPageIndex(
	ctx context.Context, chunks interfaces.ChunkRepository, engine *retriever.CompositeRetrieveEngine,
	tenantID uint64, knowledgeID string, old []*types.Chunk,
) error {
	type scope struct {
		dimension int
		kind      string
	}
	seen := make(map[scope]bool)
	for _, chunk := range old {
		var meta map[string]string
		if err := json.Unmarshal(chunk.Metadata, &meta); err != nil {
			return err
		}
		dimension, err := strconv.Atoi(meta["wiki_index_dimension"])
		if err != nil {
			return fmt.Errorf("invalid wiki index dimension: %w", err)
		}
		key := scope{dimension, meta["wiki_index_type"]}
		if !seen[key] && engine != nil {
			if err := engine.DeleteByKnowledgeIDList(ctx, []string{knowledgeID}, key.dimension, key.kind); err != nil {
				return err
			}
			seen[key] = true
		}
	}
	return chunks.PurgeWikiPageChunks(ctx, tenantID, knowledgeID)
}

// cleanupWikiPageIndexes runs before wiki SQL cleanup on every KB-delete
// attempt, including KBs with no remaining source documents and archived pages.
func (s *knowledgeBaseService) cleanupWikiPageIndexes(ctx context.Context, payload types.KBDeletePayload) error {
	if s.wikiRepo == nil {
		return nil
	}
	var engine *retriever.CompositeRetrieveEngine
	resolved := false
	for pageNumber := 1; ; pageNumber++ {
		pages, _, err := s.wikiRepo.List(ctx, &types.WikiPageListRequest{
			KnowledgeBaseID: payload.KnowledgeBaseID, Page: pageNumber, PageSize: 100,
		})
		if err != nil {
			return err
		}
		for _, page := range pages {
			if page.TenantID != payload.TenantID {
				return fmt.Errorf("wiki cleanup tenant mismatch")
			}
			err := s.wikiRepo.WithPageWriteLock(ctx, page.KnowledgeBaseID, page.Slug, func() error {
				knowledgeID := wikiPageKnowledgeID(page.ID)
				old, err := s.chunkRepo.ListWikiPageChunks(ctx, payload.TenantID, knowledgeID)
				if err != nil {
					return err
				}
				if len(old) == 0 {
					return nil
				}
				if !resolved {
					engine, err = retriever.CreateRetrieveEngineFromPayload(ctx, s.retrieveEngine, s.ownership,
						payload.TenantID, payload.EffectiveEngines, payload.VectorStoreID)
					if err != nil && !errors.Is(err, retriever.ErrVectorStoreForbidden) &&
						!errors.Is(err, retriever.ErrVectorStoreNotFound) {
						return err
					}
					// As with document cleanup, a permanently gone/inaccessible store
					// must not prevent deleting this workspace's local SQL rows.
					resolved = true
				}
				if err := deleteWikiPageIndex(ctx, s.chunkRepo, engine,
					payload.TenantID, knowledgeID, old); err != nil {
					return err
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		if len(pages) < 100 {
			return nil
		}
	}
}
