package memory

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestListDocumentsShowsHabitsNotOneOffs(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	ref := []types.MemoryDocAffinity{{
		KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", Title: "排班手册",
	}}
	svc.RecordAnswerSources(ctx, ref)
	docs, total, err := svc.ListDocuments(ctx, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total, "one citation is noise, not a habit")
	require.Empty(t, docs)

	svc.RecordAnswerSources(ctx, ref)
	docs, total, err = svc.ListDocuments(ctx, 10, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, docs, 1)
	require.Equal(t, "排班手册", docs[0].Title)
	require.Equal(t, 2, docs[0].Hits)
	require.Equal(t, []string{"doc-1"}, svc.FamiliarKnowledgeIDs(ctx))
}

func TestListDocumentsDoesNotLeakAcrossPeople(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	alice := enabledCtx(t, tenantRepo, 1, "alice")
	bob := enabledCtx(t, tenantRepo, 1, "bob")
	ref := []types.MemoryDocAffinity{{KnowledgeID: "doc-1", Title: "排班手册"}}
	svc.RecordAnswerSources(alice, ref)
	svc.RecordAnswerSources(alice, ref)

	docs, total, err := svc.ListDocuments(bob, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, docs)
	require.Empty(t, svc.FamiliarKnowledgeIDs(bob))
}

func TestLearningDocumentsIncludesOneOffsAndStaysPersonScoped(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	alice := enabledCtx(t, tenantRepo, 1, "alice")
	bob := enabledCtx(t, tenantRepo, 1, "bob")
	aliceOtherTenant := enabledCtx(t, tenantRepo, 2, "alice")
	svc.RecordAnswerSources(alice, []types.MemoryDocAffinity{{
		KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", Title: "排班手册",
	}})

	aliceDocs, err := svc.LearningDocuments(alice, "", nil, 10)
	require.NoError(t, err)
	require.Len(t, aliceDocs, 1)
	require.Equal(t, 1, aliceDocs[0].Hits)

	bobDocs, err := svc.LearningDocuments(bob, "", nil, 10)
	require.NoError(t, err)
	require.Empty(t, bobDocs)

	otherTenantDocs, err := svc.LearningDocuments(aliceOtherTenant, "", nil, 10)
	require.NoError(t, err)
	require.Empty(t, otherTenantDocs)
}

func TestLearningDocumentsFiltersKnowledgeBaseBeforeLimit(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	highRankedOtherKB := []types.MemoryDocAffinity{{
		KnowledgeID: "other-doc", KnowledgeBaseID: "kb-other", Title: "Other",
	}}
	for range 3 {
		svc.RecordAnswerSources(ctx, highRankedOtherKB)
	}
	svc.RecordAnswerSources(ctx, []types.MemoryDocAffinity{{
		KnowledgeID: "target-doc", KnowledgeBaseID: "kb-target", Title: "Target",
	}})

	docs, err := svc.LearningDocuments(ctx, "kb-target", nil, 1)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "target-doc", docs[0].KnowledgeID)
}

func TestLearningDocumentsFiltersReturnedKnowledgeIDsBeforeLimit(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	for _, id := range []string{"high-1", "high-2", "returned"} {
		svc.RecordAnswerSources(ctx, []types.MemoryDocAffinity{{
			KnowledgeID: id, KnowledgeBaseID: "kb", Title: id,
		}})
	}
	for range 3 {
		svc.RecordAnswerSources(ctx, []types.MemoryDocAffinity{{
			KnowledgeID: "high-1", KnowledgeBaseID: "kb", Title: "high-1",
		}})
	}

	docs, err := svc.LearningDocuments(ctx, "kb", []string{"returned"}, 0)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "returned", docs[0].KnowledgeID)
}

func TestDeleteDocumentStopsPersonalizingRetrieval(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	ref := []types.MemoryDocAffinity{{KnowledgeID: "doc-1", Title: "排班手册"}}
	svc.RecordAnswerSources(ctx, ref)
	svc.RecordAnswerSources(ctx, ref)

	docs, _, err := svc.ListDocuments(ctx, 10, 0)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.NoError(t, svc.DeleteDocument(ctx, docs[0].ID))

	left, total, err := svc.ListDocuments(ctx, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, left)
	require.Empty(t, svc.DocumentAffinity(ctx, []string{"doc-1"}))
}

func TestClearDropsDocumentAffinity(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	ref := []types.MemoryDocAffinity{{KnowledgeID: "doc-1", Title: "排班手册"}}
	svc.RecordAnswerSources(ctx, ref)
	svc.RecordAnswerSources(ctx, ref)

	_, err := svc.Clear(ctx)
	require.NoError(t, err)
	docs, total, err := svc.ListDocuments(ctx, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, docs)
}
