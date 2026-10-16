package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type wikiPublicationLatencyEmbedder struct {
	embedding.Embedder
	deadlines []time.Time
}

func (e *wikiPublicationLatencyEmbedder) BatchEmbedWithPool(
	ctx context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	deadline, _ := ctx.Deadline()
	e.deadlines = append(e.deadlines, deadline)
	// Each healthy call fits the cleanup budget; two sequential calls exceed it.
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return make([][]float32, len(texts)), nil
	}
}

type wikiPublicationLatencyModel struct {
	interfaces.ModelService
	embedder embedding.Embedder
}

func (m wikiPublicationLatencyModel) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return m.embedder, nil
}

type wikiPublicationLatencyChat struct{}

func (wikiPublicationLatencyChat) Chat(
	context.Context, []chat.Message, *chat.ChatOptions,
) (*types.ChatResponse, error) {
	return &types.ChatResponse{Content: "SUMMARY: Remaining facts\nRemaining wiki content after retraction."}, nil
}

func (wikiPublicationLatencyChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (wikiPublicationLatencyChat) GetModelName() string { return "publication-latency-fixture" }
func (wikiPublicationLatencyChat) GetModelID() string   { return "publication-latency-fixture" }

func TestWikiPageIndexBatchPublicationCumulativeLatency(t *testing.T) {
	if os.Getenv("WIKI_INDEX_TEST_DSN") == "" {
		t.Skip("requires the PostgreSQL integration fixture")
	}
	ctx, pages, _, engine := wikiReviewFixture(t)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	workerDeadline, _ := ctx.Deadline()
	delayed := &wikiPublicationLatencyEmbedder{Embedder: &wikiIndexEmbedder{}}
	pages.pageIndex.models = wikiPublicationLatencyModel{embedder: delayed}
	var slugs []string
	for i := 0; i < 2; i++ {
		page := wikiIndexPage()
		page.Slug = fmt.Sprintf("concept/slow-publication-%d", i)
		page.Status = types.WikiPageStatusDraft
		page.SourceRefs = types.StringArray{"remaining-source"}
		_, err := pages.CreatePage(ctx, page)
		require.NoError(t, err)
		slugs = append(slugs, page.Slug)
	}
	ingest, db := newWikiLookupRecoveryService(t, "kb-wiki", nil)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}))
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: "kb-wiki", TenantID: 7}).Error)
	ingest.wikiService = pages
	ingest.kbService.(*wikiLookupKBService).kb.TenantID = 7
	ingest.modelService = wikiReviewChatModel{model: wikiPublicationLatencyChat{}}
	payload, err := json.Marshal(WikiPendingOp{Op: WikiOpRetract, KnowledgeID: "removed-source", PageSlugs: slugs})
	require.NoError(t, err)
	require.NoError(t, ingest.pendingRepo.Enqueue(ctx, &types.TaskPendingOp{
		TenantID: 7, TaskType: wikiTaskType, Scope: wikiTaskScope, ScopeID: "kb-wiki",
		DedupKey: "removed-source", Op: WikiOpRetract, Payload: payload,
	}))
	trigger, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-wiki"})
	require.NoError(t, err)
	start := time.Now()
	require.NoError(t, ingest.ProcessWikiIngest(ctx, asynq.NewTask(types.TypeWikiIngest, trigger)),
		"healthy cumulative embedding latency must use the processing deadline")
	require.Greater(t, time.Since(start), wikiIngestCleanupTimeout)
	require.Len(t, delayed.deadlines, 2)
	for _, deadline := range delayed.deadlines {
		require.Equal(t, workerDeadline, deadline)
	}
	require.Len(t, engine.items, 2, "both pages must finish indexing in the same successful attempt")
	var count int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("task_type = ?", wikiTaskType).Count(&count).Error)
	require.Zero(t, count, "publication must finish before acknowledging its pending operation")
	require.NoError(t, db.Model(&types.TaskPendingOp{}).
		Where("task_type = ?", types.TypeWikiFinalize).Count(&count).Error)
	require.Positive(t, count, "finalize bookkeeping needs a fresh cleanup context after slow publication")
}
