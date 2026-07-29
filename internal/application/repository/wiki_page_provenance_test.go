package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const wikiProvenanceAttemptTestDDL = `
CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    knowledge_id VARCHAR(36) NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_wiki_provenance_test_spans_knowledge_attempt
    ON knowledge_processing_spans (knowledge_id, attempt);
`

func setupWikiProvenanceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupWikiPagesTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&types.Chunk{},
		&types.WikiPageBlockSet{},
		&types.WikiPageBlock{},
		&types.WikiBlockSource{},
	))
	for _, stmt := range strings.Split(strings.TrimSpace(wikiProvenanceAttemptTestDDL), ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		require.NoError(t, db.Exec(stmt).Error)
	}
	return db
}

func seedProvenanceTestChunks(t *testing.T, db *gorm.DB, set *types.WikiPageBlockSet) {
	t.Helper()
	seen := make(map[string]struct{})
	seenAttempts := make(map[string]struct{})
	for _, block := range set.Blocks {
		for _, source := range block.Sources {
			if source == nil || source.ChunkID == "" {
				continue
			}
			if source.KnowledgeID != "" && source.KnowledgeAttempt > 0 {
				attemptKey := source.KnowledgeID + "\x00" + strconv.Itoa(source.KnowledgeAttempt)
				if _, exists := seenAttempts[attemptKey]; !exists {
					seenAttempts[attemptKey] = struct{}{}
					require.NoError(t, db.Exec(`
						INSERT INTO knowledge_processing_spans (knowledge_id, attempt)
						VALUES (?, ?)
					`, source.KnowledgeID, source.KnowledgeAttempt).Error)
				}
			}
			digest := sha256.Sum256([]byte(source.Evidence))
			source.ChunkContentHash = fmt.Sprintf("%x", digest[:])
			if _, exists := seen[source.ChunkID]; exists {
				continue
			}
			seen[source.ChunkID] = struct{}{}
			require.NoError(t, db.Create(&types.Chunk{
				ID: source.ChunkID, TenantID: set.TenantID,
				KnowledgeBaseID: set.KnowledgeBaseID, KnowledgeID: source.KnowledgeID,
				Content: source.Evidence, SourceContent: source.Evidence,
				ContentRevision: source.ChunkRevision, ChunkType: types.ChunkTypeText,
				IsEnabled: true, IndexStatus: "ready",
			}).Error)
		}
	}
}

func provenanceTestSet(pageID string, version int) *types.WikiPageBlockSet {
	return &types.WikiPageBlockSet{
		ID:              "set-v" + strconv.Itoa(version),
		TenantID:        1,
		KnowledgeBaseID: "kb-provenance",
		PageID:          pageID,
		PageVersion:     version,
		Status:          types.WikiBlockSetStatusStaged,
		RenderedContent: "# Rendered v" + strconv.Itoa(version),
		RenderedSummary: "summary",
		Blocks: []*types.WikiPageBlock{
			{
				LogicalBlockID:   "logical-a",
				BlockType:        types.WikiBlockTypeParagraph,
				Content:          "Only document A supports this.",
				ContentHash:      "hash-a",
				AuthorType:       types.WikiEditSourcePipeline,
				ProvenanceStatus: types.WikiBlockProvenanceVerified,
				Sources: []*types.WikiBlockSource{{
					KnowledgeID:      "knowledge-a",
					DocumentTitle:    "Document A",
					KnowledgeAttempt: 2,
					ChunkID:          "chunk-a",
					ChunkRevision:    1,
					Evidence:         "evidence a",
					EvidenceHash:     "evidence-hash-a",
					ChunkContentHash: "chunk-hash-a",
					ValidationStatus: types.WikiSourceValidationLocated,
				}},
			},
			{
				LogicalBlockID:   "logical-shared",
				BlockType:        types.WikiBlockTypeParagraph,
				Content:          "Documents A and B support this.",
				ContentHash:      "hash-shared",
				AuthorType:       types.WikiEditSourcePipeline,
				ProvenanceStatus: types.WikiBlockProvenanceVerified,
				Sources: []*types.WikiBlockSource{
					{
						KnowledgeID: "knowledge-a", DocumentTitle: "Document A", KnowledgeAttempt: 2,
						ChunkID: "chunk-a2", Evidence: "shared evidence a", EvidenceHash: "shared-a",
						ValidationStatus: types.WikiSourceValidationLocated,
					},
					{
						KnowledgeID: "knowledge-b", DocumentTitle: "Document B", KnowledgeAttempt: 1,
						ChunkID: "chunk-b", Evidence: "shared evidence b", EvidenceHash: "shared-b",
						ValidationStatus: types.WikiSourceValidationLocated,
					},
				},
			},
		},
	}
}

func TestWikiProvenanceLifecycleAndKnowledgeCleanup(t *testing.T) {
	db := setupWikiProvenanceTestDB(t)
	repo := NewWikiProvenanceRepository(db)
	ctx := context.Background()
	pageID := "page-provenance"

	setV1 := provenanceTestSet(pageID, 1)
	seedProvenanceTestChunks(t, db, setV1)
	require.NoError(t, repo.SaveStagedBlockSet(ctx, setV1))
	loaded, err := repo.GetBlockSet(ctx, setV1.KnowledgeBaseID, setV1.ID)
	require.NoError(t, err)
	require.Len(t, loaded.Blocks, 2)
	require.Equal(t, "Document A", loaded.Blocks[0].Sources[0].DocumentTitle)
	require.NotEmpty(t, loaded.Blocks[0].Sources[0].CitationKey)
	_, err = repo.GetBlockSet(ctx, "another-kb", setV1.ID)
	require.ErrorIs(t, err, ErrWikiBlockSetNotFound)

	page := makeWikiPage(setV1.KnowledgeBaseID, "concept/provenance", types.WikiPageTypeConcept,
		types.WikiPageStatusPublished)
	page.ID = pageID
	page.Content = "caller content is replaced by the staged render"
	require.NoError(t, repo.CreatePageWithBlockSet(ctx, page, setV1.ID))
	require.Equal(t, setV1.ID, page.CurrentBlockSetID)
	require.Equal(t, setV1.RenderedContent, page.Content)

	currentSet, err := repo.GetCurrentBlockSet(ctx, setV1.KnowledgeBaseID, pageID)
	require.NoError(t, err)
	require.Equal(t, setV1.ID, currentSet.ID)
	refsA, err := repo.ListBlockReferencesByKnowledge(ctx, setV1.KnowledgeBaseID, "knowledge-a")
	require.NoError(t, err)
	require.Len(t, refsA, 2)
	require.Equal(t, "Document A", refsA[0].DocumentTitle)

	setV2 := &types.WikiPageBlockSet{
		ID: "set-v2", PageVersion: 2, GenerationRunID: "run-2",
	}
	require.NoError(t, repo.CloneBlockSetToStaged(ctx, setV1.ID, setV2))
	orphans, err := repo.RemoveKnowledgeSourcesFromStaged(
		ctx, setV1.KnowledgeBaseID, setV2.ID, "knowledge-a",
	)
	require.NoError(t, err)
	require.Len(t, orphans, 1)
	require.NoError(t, repo.DeleteBlocksFromStaged(ctx, setV1.KnowledgeBaseID, setV2.ID, orphans))
	require.NoError(t, repo.UpdateStagedBlockSetRender(
		ctx, setV1.KnowledgeBaseID, setV2.ID, "# Rendered v2", "summary v2",
	))

	var currentPage types.WikiPage
	require.NoError(t, db.First(&currentPage, "id = ?", pageID).Error)
	currentPage.Title = "Provenance v2"
	currentPage.LastEditSource = types.WikiEditSourcePipeline
	currentPage.UpdatedAt = time.Now()
	require.NoError(t, repo.PublishStagedBlockSet(ctx, &currentPage, setV2.ID))
	require.Equal(t, 2, currentPage.Version)
	require.Equal(t, setV2.ID, currentPage.CurrentBlockSetID)

	var oldSet types.WikiPageBlockSet
	require.NoError(t, db.First(&oldSet, "id = ?", setV1.ID).Error)
	require.Equal(t, types.WikiBlockSetStatusSuperseded, oldSet.Status)
	var revision types.WikiPageRevision
	require.NoError(t, db.First(&revision, "page_id = ? AND version = ?", pageID, 1).Error)
	require.Equal(t, setV1.ID, revision.BlockSetID)

	refsA, err = repo.ListBlockReferencesByKnowledge(ctx, setV1.KnowledgeBaseID, "knowledge-a")
	require.NoError(t, err)
	require.Empty(t, refsA, "superseded sources must not leak into the current reverse lookup")
	refsB, err := repo.ListBlockReferencesByKnowledge(ctx, setV1.KnowledgeBaseID, "knowledge-b")
	require.NoError(t, err)
	require.Len(t, refsB, 1)

	setV3 := &types.WikiPageBlockSet{ID: "set-v3", PageVersion: 3}
	require.NoError(t, repo.CloneBlockSetToStaged(ctx, setV2.ID, setV3))
	stale := currentPage
	stale.Version = 1
	err = repo.PublishStagedBlockSet(ctx, &stale, setV3.ID)
	require.True(t, errors.Is(err, ErrWikiPageConflict))
	var stagedV3 types.WikiPageBlockSet
	require.NoError(t, db.First(&stagedV3, "id = ?", setV3.ID).Error)
	require.Equal(t, types.WikiBlockSetStatusStaged, stagedV3.Status)

	// Force a failure at the final set-publication write, after the page row
	// and revision have already been touched inside the transaction. Both the
	// database and the caller's in-memory page must return to their old state.
	require.NoError(t, repo.UpdateStagedBlockSetRender(
		ctx, setV1.KnowledgeBaseID, setV3.ID, "# Rendered v3", "summary v3",
	))
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_set_v3_publish
		BEFORE UPDATE OF status ON wiki_page_block_sets
		WHEN NEW.id = 'set-v3' AND NEW.status = 'published'
		BEGIN
			SELECT RAISE(ABORT, 'forced publish failure');
		END;
	`).Error)
	failedPublish := currentPage
	originalContent := failedPublish.Content
	originalSummary := failedPublish.Summary
	originalUpdatedAt := failedPublish.UpdatedAt
	err = repo.PublishStagedBlockSet(ctx, &failedPublish, setV3.ID)
	require.Error(t, err)
	require.Equal(t, 2, failedPublish.Version)
	require.Equal(t, setV2.ID, failedPublish.CurrentBlockSetID)
	require.Equal(t, originalContent, failedPublish.Content)
	require.Equal(t, originalSummary, failedPublish.Summary)
	require.Equal(t, originalUpdatedAt, failedPublish.UpdatedAt)

	var storedAfterFailure types.WikiPage
	require.NoError(t, db.First(&storedAfterFailure, "id = ?", pageID).Error)
	require.Equal(t, 2, storedAfterFailure.Version)
	require.Equal(t, setV2.ID, storedAfterFailure.CurrentBlockSetID)
	var versionTwoRevisions int64
	require.NoError(t, db.Model(&types.WikiPageRevision{}).
		Where("page_id = ? AND version = ?", pageID, 2).
		Count(&versionTwoRevisions).Error)
	require.Zero(t, versionTwoRevisions)
	require.NoError(t, db.First(&stagedV3, "id = ?", setV3.ID).Error)
	require.Equal(t, types.WikiBlockSetStatusStaged, stagedV3.Status)

	require.NoError(t, repo.DeleteBlockSetsByPage(ctx, pageID))
	for _, model := range []interface{}{&types.WikiPageBlockSet{}, &types.WikiPageBlock{}, &types.WikiBlockSource{}} {
		var count int64
		require.NoError(t, db.Model(model).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestSaveStagedBlockSetRollsBackWholeTreeOnDuplicateEvidence(t *testing.T) {
	db := setupWikiProvenanceTestDB(t)
	repo := NewWikiProvenanceRepository(db)
	set := provenanceTestSet("page-rollback", 1)
	duplicate := *set.Blocks[0].Sources[0]
	duplicate.ID = ""
	set.Blocks[0].Sources = append(set.Blocks[0].Sources, &duplicate)

	err := repo.SaveStagedBlockSet(context.Background(), set)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Model(&types.WikiPageBlockSet{}).Where("id = ?", set.ID).Count(&count).Error)
	require.Zero(t, count, "set row must roll back when a nested source insert fails")
}

func TestStagedBlockSetRetryCanUseSameTargetPageVersion(t *testing.T) {
	db := setupWikiProvenanceTestDB(t)
	repo := NewWikiProvenanceRepository(db)
	ctx := context.Background()
	pageID := "page-stage-retry"

	setV1 := provenanceTestSet(pageID, 1)
	setV1.ID = "retry-set-v1"
	seedProvenanceTestChunks(t, db, setV1)
	require.NoError(t, repo.SaveStagedBlockSet(ctx, setV1))
	page := makeWikiPage(setV1.KnowledgeBaseID, "concept/stage-retry", types.WikiPageTypeConcept,
		types.WikiPageStatusPublished)
	page.ID = pageID
	require.NoError(t, repo.CreatePageWithBlockSet(ctx, page, setV1.ID))

	firstAttempt := &types.WikiPageBlockSet{ID: "retry-set-v2-first", PageVersion: 2}
	require.NoError(t, repo.CloneBlockSetToStaged(ctx, setV1.ID, firstAttempt))
	stale := *page
	stale.Version = 0
	require.ErrorIs(t, repo.PublishStagedBlockSet(ctx, &stale, firstAttempt.ID), ErrWikiPageConflict)

	// A fresh task builds a different physical set for the same target version.
	// The abandoned staged candidate must not make this save fail forever.
	secondAttempt := &types.WikiPageBlockSet{ID: "retry-set-v2-second", PageVersion: 2}
	require.NoError(t, repo.CloneBlockSetToStaged(ctx, setV1.ID, secondAttempt))
	require.NoError(t, repo.PublishStagedBlockSet(ctx, page, secondAttempt.ID))
	require.Equal(t, 2, page.Version)
	require.Equal(t, secondAttempt.ID, page.CurrentBlockSetID)

	var firstStored, secondStored types.WikiPageBlockSet
	require.NoError(t, db.First(&firstStored, "id = ?", firstAttempt.ID).Error)
	require.NoError(t, db.First(&secondStored, "id = ?", secondAttempt.ID).Error)
	require.Equal(t, types.WikiBlockSetStatusStaged, firstStored.Status)
	require.Equal(t, types.WikiBlockSetStatusPublished, secondStored.Status)

	// Although staged retries may coexist, the database must reject a second
	// finalized snapshot for the same (page_id, page_version).
	err := db.Model(&types.WikiPageBlockSet{}).
		Where("id = ?", firstAttempt.ID).
		Update("status", types.WikiBlockSetStatusPublished).Error
	require.Error(t, err)
}

func TestPublishRejectsChunkChangedAfterStaging(t *testing.T) {
	db := setupWikiProvenanceTestDB(t)
	repo := NewWikiProvenanceRepository(db)
	ctx := context.Background()
	set := provenanceTestSet("page-stale-source", 1)
	set.ID = "set-stale-source"
	seedProvenanceTestChunks(t, db, set)
	require.NoError(t, repo.SaveStagedBlockSet(ctx, set))

	require.NoError(t, db.Model(&types.Chunk{}).
		Where("id = ?", "chunk-a").
		Updates(map[string]interface{}{
			"content":          "the source changed while the Wiki was being generated",
			"content_revision": 2,
		}).Error)

	page := makeWikiPage(set.KnowledgeBaseID, "concept/stale-source", types.WikiPageTypeConcept,
		types.WikiPageStatusPublished)
	page.ID = set.PageID
	err := repo.CreatePageWithBlockSet(ctx, page, set.ID)
	require.ErrorIs(t, err, ErrWikiBlockSourceStale)

	var pageCount int64
	require.NoError(t, db.Model(&types.WikiPage{}).Where("id = ?", page.ID).Count(&pageCount).Error)
	require.Zero(t, pageCount, "a stale staged snapshot must not create the page")
	var stored types.WikiPageBlockSet
	require.NoError(t, db.First(&stored, "id = ?", set.ID).Error)
	require.Equal(t, types.WikiBlockSetStatusStaged, stored.Status)
}

func TestPublishRejectsSourceAttemptSupersededAfterStaging(t *testing.T) {
	db := setupWikiProvenanceTestDB(t)
	repo := NewWikiProvenanceRepository(db)
	ctx := context.Background()
	set := provenanceTestSet("page-stale-attempt", 1)
	set.ID = "set-stale-attempt"
	seedProvenanceTestChunks(t, db, set)
	require.NoError(t, repo.SaveStagedBlockSet(ctx, set))

	// The block set was produced by attempt 2. A new parse starts after Reduce
	// staged it but before the repository publishes the page.
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_processing_spans (knowledge_id, attempt)
		VALUES (?, ?)
	`, "knowledge-a", 3).Error)

	page := makeWikiPage(set.KnowledgeBaseID, "concept/stale-attempt", types.WikiPageTypeConcept,
		types.WikiPageStatusPublished)
	page.ID = set.PageID
	err := repo.CreatePageWithBlockSet(ctx, page, set.ID)
	require.ErrorIs(t, err, ErrWikiBlockSourceAttemptConflict)

	var pageCount int64
	require.NoError(t, db.Model(&types.WikiPage{}).Where("id = ?", page.ID).Count(&pageCount).Error)
	require.Zero(t, pageCount, "an old parse attempt must not publish a page")
	var stored types.WikiPageBlockSet
	require.NoError(t, db.First(&stored, "id = ?", set.ID).Error)
	require.Equal(t, types.WikiBlockSetStatusStaged, stored.Status)
}

func TestPublishRechecksSourceAttemptAtFinalStateTransition(t *testing.T) {
	db := setupWikiProvenanceTestDB(t)
	repo := NewWikiProvenanceRepository(db)
	ctx := context.Background()
	set := provenanceTestSet("page-attempt-toctou", 1)
	set.ID = "set-attempt-toctou"
	seedProvenanceTestChunks(t, db, set)
	require.NoError(t, repo.SaveStagedBlockSet(ctx, set))

	// Simulate a new parse attempt being persisted after the transaction's
	// initial source validation but before staged -> published. The trigger runs
	// as part of the page INSERT, precisely inside that former TOCTOU window.
	require.NoError(t, db.Exec(`
		CREATE TRIGGER advance_source_attempt_during_wiki_publish
		AFTER INSERT ON wiki_pages
		BEGIN
			INSERT INTO knowledge_processing_spans (knowledge_id, attempt)
			VALUES ('knowledge-a', 3);
		END;
	`).Error)

	page := makeWikiPage(set.KnowledgeBaseID, "concept/attempt-toctou", types.WikiPageTypeConcept,
		types.WikiPageStatusPublished)
	page.ID = set.PageID
	err := repo.CreatePageWithBlockSet(ctx, page, set.ID)
	require.ErrorIs(t, err, ErrWikiBlockSourceAttemptConflict)

	var pageCount int64
	require.NoError(t, db.Model(&types.WikiPage{}).Where("id = ?", page.ID).Count(&pageCount).Error)
	require.Zero(t, pageCount, "the page insert must roll back when the final attempt guard loses")
	var stored types.WikiPageBlockSet
	require.NoError(t, db.First(&stored, "id = ?", set.ID).Error)
	require.Equal(t, types.WikiBlockSetStatusStaged, stored.Status)
	var latest int
	require.NoError(t, db.Raw(`
		SELECT COALESCE(MAX(attempt), 0)
		FROM knowledge_processing_spans
		WHERE knowledge_id = ?
	`, "knowledge-a").Scan(&latest).Error)
	require.Equal(t, 2, latest, "the trigger insert must roll back with the rejected publication")
}

func TestMarkStagedBlockSetFailedOnlyTransitionsStaged(t *testing.T) {
	db := setupWikiProvenanceTestDB(t)
	repo := NewWikiProvenanceRepository(db)
	ctx := context.Background()

	statuses := []string{
		types.WikiBlockSetStatusStaged,
		types.WikiBlockSetStatusPublished,
		types.WikiBlockSetStatusSuperseded,
		types.WikiBlockSetStatusFailed,
	}
	for _, status := range statuses {
		set := provenanceTestSet("page-mark-"+status, 1)
		set.ID = "set-mark-" + status
		require.NoError(t, repo.SaveStagedBlockSet(ctx, set))
		if status != types.WikiBlockSetStatusStaged {
			require.NoError(t, db.Model(&types.WikiPageBlockSet{}).
				Where("id = ?", set.ID).
				Update("status", status).Error)
		}

		err := repo.MarkStagedBlockSetFailed(ctx, set.KnowledgeBaseID, set.ID)
		if status == types.WikiBlockSetStatusStaged {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, ErrWikiBlockSetNotStaged)
		}

		var stored types.WikiPageBlockSet
		require.NoError(t, db.First(&stored, "id = ?", set.ID).Error)
		if status == types.WikiBlockSetStatusStaged {
			require.Equal(t, types.WikiBlockSetStatusFailed, stored.Status)
		} else {
			require.Equal(t, status, stored.Status)
		}
	}

	untouched := provenanceTestSet("page-mark-wrong-kb", 1)
	untouched.ID = "set-mark-wrong-kb"
	require.NoError(t, repo.SaveStagedBlockSet(ctx, untouched))
	require.ErrorIs(t,
		repo.MarkStagedBlockSetFailed(ctx, "another-kb", untouched.ID),
		ErrWikiBlockSetNotFound,
	)
	var stored types.WikiPageBlockSet
	require.NoError(t, db.First(&stored, "id = ?", untouched.ID).Error)
	require.Equal(t, types.WikiBlockSetStatusStaged, stored.Status)
	require.ErrorIs(t,
		repo.MarkStagedBlockSetFailed(ctx, untouched.KnowledgeBaseID, "missing-set"),
		ErrWikiBlockSetNotFound,
	)
}

func TestDeletePageIfVersionRejectsConcurrentPageChange(t *testing.T) {
	db := setupWikiProvenanceTestDB(t)
	repo := NewWikiProvenanceRepository(db)
	ctx := context.Background()

	set := provenanceTestSet("page-cas-delete", 1)
	set.ID = "set-cas-delete"
	seedProvenanceTestChunks(t, db, set)
	require.NoError(t, repo.SaveStagedBlockSet(ctx, set))
	page := makeWikiPage(
		set.KnowledgeBaseID,
		"concept/cas-delete",
		types.WikiPageTypeConcept,
		types.WikiPageStatusPublished,
	)
	page.ID = set.PageID
	require.NoError(t, repo.CreatePageWithBlockSet(ctx, page, set.ID))
	require.NoError(t, db.Create(&types.WikiPageRevision{
		ID:              "revision-cas-delete",
		TenantID:        page.TenantID,
		KnowledgeBaseID: page.KnowledgeBaseID,
		PageID:          page.ID,
		Slug:            page.Slug,
		Version:         page.Version - 1,
		Title:           page.Title,
		Content:         "old content",
	}).Error)
	require.NoError(t, db.Create(&types.Chunk{
		ID:              "wp-" + page.ID,
		TenantID:        page.TenantID,
		KnowledgeBaseID: page.KnowledgeBaseID,
		KnowledgeID:     page.ID,
		Content:         page.Content,
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
		IndexStatus:     "ready",
	}).Error)

	require.ErrorIs(
		t,
		repo.DeletePageIfVersion(ctx, set.KnowledgeBaseID, page.ID, page.Version+1),
		ErrWikiBlockSetConflict,
	)
	var stillCurrent types.WikiPage
	require.NoError(t, db.First(&stillCurrent, "id = ?", page.ID).Error)
	var count int64
	require.NoError(t, db.Model(&types.WikiPageRevision{}).Where("page_id = ?", page.ID).Count(&count).Error)
	require.EqualValues(t, 1, count, "CAS conflict must not delete revision history")
	require.NoError(t, db.Model(&types.WikiPageBlockSet{}).Where("page_id = ?", page.ID).Count(&count).Error)
	require.EqualValues(t, 1, count, "CAS conflict must not delete provenance")
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", "wp-"+page.ID).Count(&count).Error)
	require.EqualValues(t, 1, count, "CAS conflict must not delete the retrieval chunk")

	require.NoError(t, repo.DeletePageIfVersion(ctx, set.KnowledgeBaseID, page.ID, page.Version))
	require.ErrorIs(t, db.First(&types.WikiPage{}, "id = ?", page.ID).Error, gorm.ErrRecordNotFound)
	var deleted types.WikiPage
	require.NoError(t, db.Unscoped().First(&deleted, "id = ?", page.ID).Error)
	var revisionCount, blockSetCount, blockCount, sourceCount, liveChunkCount int64
	require.NoError(t, db.Model(&types.WikiPageRevision{}).Where("page_id = ?", page.ID).Count(&revisionCount).Error)
	require.NoError(t, db.Model(&types.WikiPageBlockSet{}).Where("page_id = ?", page.ID).Count(&blockSetCount).Error)
	require.NoError(t, db.Model(&types.WikiPageBlock{}).Where("block_set_id = ?", set.ID).Count(&blockCount).Error)
	require.NoError(t, db.Model(&types.WikiBlockSource{}).Count(&sourceCount).Error)
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", "wp-"+page.ID).Count(&liveChunkCount).Error)
	require.Zero(t, revisionCount)
	require.Zero(t, blockSetCount)
	require.Zero(t, blockCount)
	require.Zero(t, sourceCount)
	require.Zero(t, liveChunkCount)
}
