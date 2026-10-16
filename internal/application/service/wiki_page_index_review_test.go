package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/hibiken/asynq"

	"gorm.io/driver/postgres"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func wikiReviewFixture(t *testing.T) (context.Context, *wikiPageService, *gorm.DB, *wikiIndexEngine) {
	t.Helper()
	dialect := sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()))
	if dsn := os.Getenv("WIKI_INDEX_TEST_DSN"); dsn != "" {
		// Never drop application tables in a supplied database: each test owns a
		// fresh schema and closes its pool before removing only that schema.
		admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		require.NoError(t, err)
		adminDB, err := admin.DB()
		require.NoError(t, err)
		schema := "wiki_index_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
		t.Cleanup(func() {
			require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
			_ = adminDB.Close()
		})
		config, err := pgx.ParseConfig(dsn)
		require.NoError(t, err)
		config.RuntimeParams["search_path"] = schema
		dialect = postgres.New(postgres.Config{Conn: stdlib.OpenDB(*config)})
	}
	db, err := gorm.Open(dialect, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(
		&types.WikiPage{}, &types.WikiPageRevision{}, &types.Chunk{}, &types.WikiFolder{},
	))
	if db.Name() == "postgres" {
		// Match migrations/versioned/000037_wiki_and_indexing.up.sql; GORM's
		// generic array serializer otherwise creates JSON rather than JSONB.
		for _, column := range []string{
			"category_path", "source_refs", "chunk_refs", "in_links",
			"out_links", "page_metadata", "aliases",
		} {
			require.NoError(t, db.Exec(fmt.Sprintf("ALTER TABLE wiki_pages ALTER COLUMN %s TYPE JSONB USING %s::jsonb",
				column, column)).Error)
		}
	}
	kb := &types.KnowledgeBase{
		ID: "kb-wiki", TenantID: 7, Type: "document", EmbeddingModelID: "embed",
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true, VectorEnabled: true},
		ChunkingConfig:   types.ChunkingConfig{ChunkSize: 100, ChunkOverlap: 10},
	}
	engine := &wikiIndexEngine{items: make(map[string]*types.IndexInfo)}
	svc := NewIndexedWikiPageService(repository.NewWikiPageRepository(db), repository.NewChunkRepository(db),
		&wikiIndexKB{kb: kb}, nil, nil, nil, &wikiIndexModel{embedder: &wikiIndexEmbedder{}},
		wikiIndexRegistry{engine: engine}, nil, wikiIndexTenant{}).(*wikiPageService)
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7)), svc, db, engine
}

func TestWikiPageIndexProductionPublicationRecovery(t *testing.T) {
	for _, failModel := range []bool{false, true} {
		t.Run(fmt.Sprint("model=", failModel), func(t *testing.T) {
			ctx, svc, _, engine := wikiReviewFixture(t)
			p := wikiIndexPage()
			p.Status = types.WikiPageStatusDraft
			_, err := svc.CreatePage(ctx, p)
			require.NoError(t, err)
			ingest := &wikiIngestService{wikiService: svc}
			transient := errors.New("temporary publication failure")
			if failModel {
				svc.pageIndex.models.(*wikiIndexModel).err = transient
			} else {
				engine.err = transient
			}
			require.ErrorIs(t, ingest.publishDraftPages(ctx, p.KnowledgeBaseID, []string{p.Slug}), transient)
			require.Empty(t, engine.items)
			engine.err = nil
			svc.pageIndex.models.(*wikiIndexModel).err = nil
			// Repeat the actual production publisher, without a manual UpdatePage repair.
			require.NoError(t, ingest.publishDraftPages(ctx, p.KnowledgeBaseID, []string{p.Slug}))
			require.NotEmpty(t, engine.items, "publication must recover even if the stored status is already published")
		})
	}
}

type wikiReviewPauseModel struct {
	interfaces.ModelService
	entered chan struct{}
	release chan struct{}
}

func (m *wikiReviewPauseModel) GetEmbeddingModel(ctx context.Context, id string) (embedding.Embedder, error) {
	close(m.entered)
	select {
	case <-m.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return m.ModelService.GetEmbeddingModel(ctx, id)
}

func TestWikiPageIndexOverlappingWrites(t *testing.T) {
	for _, operation := range []string{"archive", "delete", "update"} {
		t.Run(operation, func(t *testing.T) {
			ctx, svc, db, engine := wikiReviewFixture(t)
			p, err := svc.CreatePage(ctx, wikiIndexPage())
			require.NoError(t, err)
			// A second service/repository instance models another caller, not a lock
			// shared accidentally through the first service object's fields.
			other := *svc
			other.repo = repository.NewWikiPageRepository(db)
			otherIndex := *svc.pageIndex
			other.pageIndex = &otherIndex
			pause := &wikiReviewPauseModel{
				ModelService: svc.pageIndex.models, entered: make(chan struct{}), release: make(chan struct{}),
			}
			svc.pageIndex.models = pause
			first := *p
			first.Content = "first replacement body"
			firstDone := make(chan error, 1)
			go func() { _, err := svc.UpdatePage(ctx, &first); firstDone <- err }()
			select {
			case <-pause.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("replacement did not reach embedding")
			}
			second := *p
			second.Content = "last replacement body"
			secondDone := make(chan error, 1)
			go func() {
				switch operation {
				case "archive":
					second.Status = types.WikiPageStatusArchived
					secondDone <- other.UpdatePageMeta(ctx, &second)
				case "delete":
					secondDone <- other.DeletePage(ctx, p.KnowledgeBaseID, p.Slug)
				default:
					_, err := other.UpdatePage(ctx, &second)
					secondDone <- err
				}
			}()
			// Before the fix the lifecycle write completes while the first replacement
			// is paused after deletion. With serialization it waits until release.
			var secondErr error
			finished := false
			select {
			case secondErr = <-secondDone:
				finished = true
			case <-time.After(100 * time.Millisecond):
			}
			close(pause.release)
			require.NoError(t, <-firstDone)
			if !finished {
				secondErr = <-secondDone
			}
			require.NoError(t, secondErr)
			rows, err := svc.chunkRepo.ListAllChunksByKnowledgeID(ctx, 7, wikiPageKnowledgeID(p.ID))
			require.NoError(t, err)
			if operation == "update" {
				require.Len(t, rows, 1, "only the last generation may remain")
				require.Len(t, engine.items, 1)
				require.Equal(t, second.Content, rows[0].Content)
			} else {
				require.Empty(t, rows, "completed archive/delete must not be followed by searchable chunks")
				require.Empty(t, engine.items)
			}
		})
	}
}

func TestWikiPageIndexBoundedDerivedRows(t *testing.T) {
	for _, cleanup := range []string{"archive", "delete", "kb"} {
		t.Run(cleanup, func(t *testing.T) {
			ctx, svc, db, engine := wikiReviewFixture(t)
			p, err := svc.CreatePage(ctx, wikiIndexPage())
			require.NoError(t, err)
			for i := 0; i < 4; i++ {
				require.NoError(t, svc.UpdatePageMeta(ctx, p))
			}
			var live, total int64
			query := db.Model(&types.Chunk{}).Where("tenant_id = ? AND knowledge_id = ?", 7, wikiPageKnowledgeID(p.ID))
			require.NoError(t, query.Count(&live).Error)
			require.NoError(t, db.Unscoped().Model(&types.Chunk{}).
				Where("tenant_id = ? AND knowledge_id = ?", 7, wikiPageKnowledgeID(p.ID)).Count(&total).Error)
			require.Equal(t, live, total, "unchanged metadata writes must not retain soft-deleted content generations")
			// Include a legacy retained generation to prove cleanup reclaims rows
			// that a scoped query would never see.
			require.NoError(t, db.Where("tenant_id = ? AND knowledge_id = ?", 7, wikiPageKnowledgeID(p.ID)).
				Delete(&types.Chunk{}).Error)
			switch cleanup {
			case "archive":
				p.Status = types.WikiPageStatusArchived
				require.NoError(t, svc.UpdatePageMeta(ctx, p))
			case "delete":
				require.NoError(t, svc.DeletePage(ctx, p.KnowledgeBaseID, p.Slug))
			default:
				s := &knowledgeBaseService{
					wikiRepo: svc.repo, chunkRepo: svc.chunkRepo, retrieveEngine: wikiIndexRegistry{engine: engine},
				}
				require.NoError(t, s.cleanupWikiPageIndexes(ctx, types.KBDeletePayload{
					TenantID: 7, KnowledgeBaseID: p.KnowledgeBaseID,
					EffectiveEngines: []types.RetrieverEngineParams{{
						RetrieverType:       types.VectorRetrieverType,
						RetrieverEngineType: types.PostgresRetrieverEngineType,
					}},
				}))
			}
			require.NoError(t, db.Unscoped().Model(&types.Chunk{}).
				Where("tenant_id = ? AND knowledge_id = ?", 7, wikiPageKnowledgeID(p.ID)).Count(&total).Error)
			require.Zero(t, total, "lifecycle cleanup must physically remove every derived generation")
		})
	}
}

type wikiReviewChatModel struct {
	interfaces.ModelService
	model chat.Chat
}

func (m wikiReviewChatModel) GetChatModel(context.Context, string) (chat.Chat, error) {
	return m.model, nil
}

// Exercise the production task entry and durable queue, not a manual page edit.
// A publication failure must not acknowledge its contributing pending operation.
func TestWikiPageIndexBatchPublicationRecovery(t *testing.T) {
	if os.Getenv("WIKI_INDEX_TEST_DSN") == "" {
		t.Skip("requires the PostgreSQL integration fixture")
	}

	ctx, pages, _, engine := wikiReviewFixture(t)
	page := wikiIndexPage()
	page.Status = types.WikiPageStatusDraft
	page.SourceRefs = types.StringArray{"remaining-source"}
	_, err := pages.CreatePage(ctx, page)
	require.NoError(t, err)
	ingest, db := newWikiLookupRecoveryService(t, page.KnowledgeBaseID, nil)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}))
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: page.KnowledgeBaseID, TenantID: 7}).Error)
	ingest.wikiService = pages
	ingest.kbService.(*wikiLookupKBService).kb.TenantID = 7
	ingest.modelService = wikiReviewChatModel{model: &scriptedTemplateChatModel{responses: []*types.ChatResponse{
		{Content: "SUMMARY: Remaining facts\nRemaining wiki content after retraction."},
		{Content: "SUMMARY: Remaining facts\nRemaining wiki content after retraction."},
	}}}
	payload, err := json.Marshal(WikiPendingOp{
		Op: WikiOpRetract, KnowledgeID: "removed-source", PageSlugs: []string{page.Slug},
	})
	require.NoError(t, err)
	require.NoError(t, ingest.pendingRepo.Enqueue(ctx, &types.TaskPendingOp{
		TenantID: 7, TaskType: wikiTaskType, Scope: wikiTaskScope, ScopeID: page.KnowledgeBaseID,
		DedupKey: "removed-source", Op: WikiOpRetract, Payload: payload,
	}))
	trigger, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: page.KnowledgeBaseID})
	require.NoError(t, err)
	task := asynq.NewTask(types.TypeWikiIngest, trigger)
	engine.err = errors.New("publication backend unavailable")
	require.ErrorContains(t, ingest.ProcessWikiIngest(ctx, task), "publication backend unavailable")
	var count int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("task_type = ?", wikiTaskType).Count(&count).Error)
	require.EqualValues(t, 1, count, "failed publication must retain its durable work")
	engine.err = nil
	require.NoError(t, ingest.ProcessWikiIngest(ctx, task))
	require.NotEmpty(t, engine.items)
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("task_type = ?", wikiTaskType).Count(&count).Error)
	require.Zero(t, count, "successful publication settles the retained work")
}

// A lock held on an independent PostgreSQL connection represents another
// worker/process, bypassing the in-process stripes entirely.
func TestWikiPageIndexPostgresExternalWriterLock(t *testing.T) {
	if os.Getenv("WIKI_INDEX_TEST_DSN") == "" {
		t.Skip("requires the PostgreSQL integration fixture")
	}
	ctx, svc, db, _ := wikiReviewFixture(t)
	page, err := svc.CreatePage(ctx, wikiIndexPage())
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	conn, err := sqlDB.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	hash := fnv.New64a()
	_, _ = hash.Write([]byte("wiki-page:" + page.KnowledgeBaseID + "\x00" + page.Slug))
	key := int64(hash.Sum64())
	_, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { page.Status = types.WikiPageStatusArchived; done <- svc.UpdatePageMeta(ctx, page) }()
	var premature error
	completed := false
	select {
	case premature = <-done:
		completed = true
	case <-time.After(100 * time.Millisecond):
	}
	_, err = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", key)
	require.NoError(t, err)
	require.False(t, completed, "another worker's lock must fence persistence and indexing")
	if !completed {
		premature = <-done
	}
	require.NoError(t, premature)
	rows, err := svc.chunkRepo.ListAllChunksByKnowledgeID(ctx, 7, wikiPageKnowledgeID(page.ID))
	require.NoError(t, err)
	require.Empty(t, rows)
}

type wikiReviewStalePublisher struct {
	interfaces.WikiPageService
}

func (s wikiReviewStalePublisher) GetPageBySlug(ctx context.Context, kbID, slug string) (*types.WikiPage, error) {
	page, err := s.WikiPageService.GetPageBySlug(ctx, kbID, slug)
	if err != nil {
		return nil, err
	}
	archived := *page
	archived.Status = types.WikiPageStatusArchived
	if err := s.UpdatePageMeta(ctx, &archived); err != nil {
		return nil, err
	}
	return page, nil
}

func TestWikiPageIndexPublicationDoesNotUndoArchive(t *testing.T) {
	ctx, svc, _, engine := wikiReviewFixture(t)
	page := wikiIndexPage()
	page.Status = types.WikiPageStatusDraft
	_, err := svc.CreatePage(ctx, page)
	require.NoError(t, err)
	ingest := &wikiIngestService{wikiService: wikiReviewStalePublisher{WikiPageService: svc}}
	require.NoError(t, ingest.publishDraftPages(ctx, page.KnowledgeBaseID, []string{page.Slug}))
	stored, err := svc.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	require.Equal(t, types.WikiPageStatusArchived, stored.Status)
	require.Empty(t, engine.items)
}

func TestWikiPageIndexMetadataUsesPersistedBody(t *testing.T) {
	ctx, svc, _, _ := wikiReviewFixture(t)
	page, err := svc.CreatePage(ctx, wikiIndexPage())
	require.NoError(t, err)
	stale := *page
	page.Content = "Latest persisted body"
	_, err = svc.UpdatePage(ctx, page)
	require.NoError(t, err)
	require.NoError(t, svc.UpdatePageMeta(ctx, &stale))
	rows, err := svc.chunkRepo.ListAllChunksByKnowledgeID(ctx, 7, wikiPageKnowledgeID(page.ID))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, page.Content, rows[0].Content)
}
