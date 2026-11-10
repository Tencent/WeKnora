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
