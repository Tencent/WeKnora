package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestDeleteTaskMovingConflictStopsRetries(t *testing.T) {
	f := transferFixture(t, access.KBTransferMove)
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.NoError(t, setTransferState(row, knowledgeTransferState{Operation: access.KBTransferMove, Phase: "moving"}))
	require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
	beforeWrites := f.repo.writes
	raw, err := json.Marshal(
		types.KnowledgeListDeletePayload{TenantID: 7, KnowledgeBaseID: "kb", KnowledgeIDs: []string{"doc"}},
	)
	require.NoError(t, err)
	err = f.svc.ProcessKnowledgeListDelete(context.Background(), asynq.NewTask(types.TypeKnowledgeListDelete, raw))
	require.ErrorIs(t, err, asynq.SkipRetry)
	require.ErrorContains(t, err, "unfinished move")
	require.Equal(t, beforeWrites, f.repo.writes)
	require.Zero(t, f.chunkRepo.writes)
	require.Zero(t, f.graph.calls)
}

func TestDeleteWikiRemovesEveryChunkType(t *testing.T) {
	f := newDocumentWriteFixture(t)
	f.kbs.values["kb"].IndexingStrategy.WikiEnabled = true
	refs := types.StringArray{"chunk"}
	chunkTypes := []string{
		types.ChunkTypeParentText, types.ChunkTypeImageOCR, types.ChunkTypeImageCaption, types.ChunkTypeSummary,
	}
	for _, typ := range chunkTypes {
		require.NoError(
			t,
			f.db.Create(
				&types.Chunk{ID: typ, TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb", ChunkType: typ},
			).Error,
		)
		refs = append(refs, typ)
	}
	refs = append(refs, "other-chunk")
	page := &types.WikiPage{
		ID:         "page",
		Slug:       "concept/shared",
		SourceRefs: types.StringArray{"doc", "other-doc"},
		ChunkRefs:  refs,
	}
	wiki := &moveWikiPageRepo{pages: []*types.WikiPage{page}}
	meta := &moveWikiMetaService{}
	f.svc.wikiRepo = wiki
	f.svc.wikiService = meta
	f.svc.taskPendingRepo = &moveWikiPendingRepo{}
	f.svc.task = &transferQueue{}
	require.NoError(t, f.svc.DeleteKnowledge(f.ctx, "doc"))
	require.Len(t, meta.updated, 1)
	require.Equal(t, types.StringArray{"other-chunk"}, meta.updated[0].ChunkRefs)
	require.Equal(t, types.StringArray{"other-doc"}, meta.updated[0].SourceRefs)
}

func TestKnowledgeDeletePreservesLinkedWikiPages(t *testing.T) {
	backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
		if h.db.Name() != "postgres" {
			t.Skip("ListBySourceRef uses PostgreSQL JSONB")
		}
		ctx := context.Background()
		f := newDocumentWriteFixture(t)
		kb := f.kbs.values["kb"]
		kb.IndexingStrategy.WikiEnabled = true
		seedWikiTestKnowledgeBase(t, h.db, kb.ID, kb.TenantID)
		require.NoError(t, h.db.Exec(
			"ALTER TABLE wiki_pages ALTER COLUMN source_refs TYPE jsonb, ALTER COLUMN in_links TYPE jsonb",
		).Error)
		require.NoError(t, h.db.AutoMigrate(&types.WikiPageIssue{}))
		require.NoError(t, f.db.Create(&types.Knowledge{
			ID: "support-doc", TenantID: kb.TenantID, KnowledgeBaseID: kb.ID, ParseStatus: types.ParseStatusCompleted,
		}).Error)
		require.NoError(t, f.db.Create(&types.Chunk{
			ID: "support-chunk", TenantID: kb.TenantID, KnowledgeID: "support-doc", KnowledgeBaseID: kb.ID,
		}).Error)
		f.svc.wikiRepo = h.repo
		f.svc.wikiService = h.svc
		pending := &moveWikiPendingRepo{}
		f.svc.taskPendingRepo = pending
		f.svc.task = &transferQueue{}

		create := func(slug, content string, sources, chunks types.StringArray) *types.WikiPage {
			t.Helper()
			page, err := h.svc.CreatePage(ctx, &types.WikiPage{
				TenantID: kb.TenantID, KnowledgeBaseID: kb.ID, Slug: slug, Title: slug,
				PageType: types.WikiPageTypeConcept, Content: content, SourceRefs: sources, ChunkRefs: chunks,
			})
			require.NoError(t, err)
			return page
		}
		const target = "concept/units"
		const source = "concept/estimate"
		const body = "Standard units of measure for construction. See [[concept/volume]]."
		const sourceBody = "Construction estimates specify the following.\n\n| [[concept/units\\|单位]] | m³ |"
		create("concept/volume", "Volume is measured in cubic meters on quantity estimates.", nil, nil)
		before := create(target, body, types.StringArray{"doc|Original"}, types.StringArray{"chunk"})
		create("concept/unused", "A page with no inbound links and only the deleted source.",
			types.StringArray{"doc"}, types.StringArray{"chunk"})
		create("concept/self", "Only a link to itself, [[concept/self]], points here.",
			types.StringArray{"doc"}, nil)
		create("concept/shared", "A page supported by another document as well as the deleted one.",
			types.StringArray{"doc", "support-doc"}, types.StringArray{"chunk", "support-chunk"})
		create(source, sourceBody, types.StringArray{"support-doc"}, nil)
		linked, err := h.repo.GetBySlug(ctx, kb.ID, target)
		require.NoError(t, err)
		require.Equal(t, types.StringArray{source}, linked.InLinks)

		task := deleteDocumentTask(t, types.KnowledgeListDeletePayload{
			TenantID: kb.TenantID, KnowledgeBaseID: kb.ID, KnowledgeIDs: []string{"doc"},
		})
		require.NoError(t, f.svc.ProcessKnowledgeListDelete(ctx, task))
		_, err = f.repo.GetKnowledgeByID(ctx, kb.TenantID, "doc")
		require.Error(t, err)
		after, err := h.repo.GetBySlug(ctx, kb.ID, target)
		require.NoError(t, err, "deleting the last source must preserve the linked wiki page")
		require.Equal(t, types.StringArray{}, after.SourceRefs)
		require.Empty(t, after.ChunkRefs)
		require.Equal(t, body, after.Content)
		require.Equal(t, before.Version, after.Version)
		require.Equal(t, types.StringArray{source}, after.InLinks)
		require.Equal(t, types.StringArray{"concept/volume"}, after.OutLinks)
		referrer, err := h.repo.GetBySlug(ctx, kb.ID, source)
		require.NoError(t, err)
		require.Equal(t, sourceBody, referrer.Content)
		require.Equal(t, types.StringArray{target}, referrer.OutLinks)
		_, err = h.repo.GetBySlug(ctx, kb.ID, "concept/unused")
		require.ErrorIs(t, err, repository.ErrWikiPageNotFound)
		_, err = h.repo.GetBySlug(ctx, kb.ID, "concept/self")
		require.ErrorIs(t, err, repository.ErrWikiPageNotFound)
		shared, err := h.repo.GetBySlug(ctx, kb.ID, "concept/shared")
		require.NoError(t, err)
		require.Equal(t, types.StringArray{"support-doc"}, shared.SourceRefs)
		require.Equal(t, types.StringArray{"support-chunk"}, shared.ChunkRefs)

		lint := NewWikiLintService(h.svc, markdownLintKBService{kb: kb}, nil)
		report, err := lint.RunLint(ctx, kb.ID)
		require.NoError(t, err)
		for _, issue := range report.Issues {
			require.NotEqual(t, LintIssueBrokenLink, issue.Type)
		}

		require.Len(t, pending.ops, 1)
		var op WikiPendingOp
		require.NoError(t, json.Unmarshal(pending.ops[0].Payload, &op))
		require.Contains(t, op.PageSlugs, target)
		ingest := &wikiIngestService{wikiService: h.svc}
		model := &templateCaptureChatModel{
			response: "SUMMARY: Removed source\nThis page no longer has its original evidence.",
		}
		batch := &WikiBatchContext{SlugTitleMany: func(context.Context, []string) map[string]string { return nil }}
		changed, _, failed, deferred, err := ingest.reduceSlugUpdates(ctx, model, kb.ID, target,
			[]SlugUpdate{
				{
					Slug:              target,
					Type:              "retract",
					KnowledgeID:       op.KnowledgeID,
					DocTitle:          op.DocTitle,
					RetractDocContent: op.DocSummary,
				},
			}, kb.TenantID, batch, nil)
		require.NoError(t, err)
		require.False(t, changed)
		require.False(t, failed)
		require.False(t, deferred)
		require.Empty(t, model.messages)
		retracted, err := h.repo.GetBySlug(ctx, kb.ID, target)
		require.NoError(t, err)
		require.Equal(t, after, retracted)
	})
}

type cleanupKBFailure struct {
	interfaces.KnowledgeBaseService
	value *types.KnowledgeBase
	err   error
}

func (s *cleanupKBFailure) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.value, s.err
}

func TestCleanupStopsBeforeSideEffectsWhenKBUnavailable(t *testing.T) {
	for _, scenario := range []string{"storage error", "nil KB", "wrong KB", "wrong tenant"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			lookup := &cleanupKBFailure{}
			switch scenario {
			case "storage error":
				lookup.err = errors.New("database unavailable")
			case "wrong KB":
				lookup.value = &types.KnowledgeBase{ID: "other", TenantID: 7}
			case "wrong tenant":
				lookup.value = &types.KnowledgeBase{ID: "kb", TenantID: 8}
			}
			f.svc.kbService = lookup
			row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			row.EmbeddingModelID = "bound-model"
			// Engine/model dependencies remain absent: touching a fallback backend
			// would panic instead of preserving the lookup error and original data.
			err = f.svc.cleanupKnowledgeResources(f.ctx, row)
			require.Error(t, err)
			if lookup.err != nil {
				require.ErrorIs(t, err, lookup.err)
			}
			require.Zero(t, f.chunkRepo.writes)
			require.Zero(t, f.graph.calls)
			require.Empty(t, f.files.deleted)
			require.Empty(t, f.tenants.adjustments)
		})
	}
}

func TestMoveReparseKBLookupFailurePreservesSourceCheckpoint(t *testing.T) {
	f := transferFixture(t, access.KBTransferMove)
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.NoError(
		t,
		row.SetManualMetadata(types.NewManualKnowledgeMetadata("content", types.ManualKnowledgeStatusPublish, 1)),
	)
	require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
	failure := errors.New("source KB load failed")
	f.svc.kbService = &cleanupKBFailure{err: failure}
	err = f.svc.moveOneKnowledge(f.ctx, "doc", f.kbs.values["kb"], f.kbs.values["other"], "reparse")
	require.ErrorIs(t, err, failure)
	row, err = f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.Equal(t, "kb", row.KnowledgeBaseID)
	state, err := transferState(row)
	require.NoError(t, err)
	require.Equal(t, "moving", state.Phase)
	require.Zero(t, f.chunkRepo.writes)
	require.Zero(t, f.graph.calls)
}

func TestRemoveSourceRefHandlesTitledAndPaddedRefs(t *testing.T) {
	refs := types.StringArray{"doc-1", "doc-1|Title", " doc-1 |padded", "doc-10|Other", "doc-2"}
	got := removeSourceRef(refs, "doc-1")
	require.Equal(t, types.StringArray{"doc-10|Other", "doc-2"}, got)
	require.Nil(t, removeSourceRef(types.StringArray{"doc-1|T"}, "doc-1"))
	require.Equal(t, types.StringArray{"doc-1|T"}, removeSourceRef(types.StringArray{"doc-1|T"}, ""))
}
