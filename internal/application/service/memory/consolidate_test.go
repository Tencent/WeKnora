package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// newConsolidationHarness gives the service a model, because consolidation is
// the model's decision: without one there is nothing to test but the refusal.
func newConsolidationHarness(t *testing.T) (*Service, *stubTenantRepo, *stubModelService) {
	t.Helper()
	svc, _, tenantRepo := newMemoryHarness(t)
	models := &stubModelService{
		workspaceModels: []*types.Model{
			{ID: "chat-1", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive},
		},
		response: `{"statement":"回答直接给结论，不要铺垫"}`,
	}
	svc.modelService = models
	return svc, tenantRepo, models
}

func seedItem(
	t *testing.T, svc *Service, ctx context.Context, scope interfaces.MemoryScope,
	kind, topic, content, key string,
) {
	t.Helper()
	require.NoError(t, svc.repo.CreateItem(ctx, &types.MemoryItem{
		ID: uuid.New().String(), TenantID: scope.TenantID, SubjectID: scope.SubjectID,
		Kind: kind, Topic: topic, Content: content, NormalizedKey: key,
		Status: types.MemoryStatusActive, Origin: types.MemoryOriginManual,
		Importance: 3, ValidFrom: time.Now(),
	}))
}

func seedSimilarPreferences(t *testing.T, svc *Service, ctx context.Context, scope interfaces.MemoryScope) {
	t.Helper()
	seedItem(t, svc, ctx, scope, types.MemoryKindPreference, "回答风格", "回答直接给结论不要铺垫", "k-a")
	seedItem(t, svc, ctx, scope, types.MemoryKindPreference, "回答风格", "回答直接给结论不用铺垫", "k-b")
}

func TestConsolidateNowMergesNearDuplicatesWithoutWaiting(t *testing.T) {
	svc, tenantRepo, _ := newConsolidationHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := svc.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	require.NoError(t, svc.repo.MarkConsolidated(ctx, scope))
	seedSimilarPreferences(t, svc, ctx, scope)

	result, err := svc.ConsolidateNow(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Merged, "an explicit review must merge even if the daily pass just ran")
	require.Empty(t, result.Skipped)

	_, total, err := svc.ListItems(ctx, types.MemoryStatusActive, 10, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)

	_, superseded, err := svc.ListItems(ctx, types.MemoryStatusSuperseded, 10, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, superseded, int64(1))
}

// The pair that prompted this: two profiles that contradict each other share
// 0.50 of their tokens, just under the bar the unattended pass uses, so the
// button reported "nothing to do" in milliseconds without the model ever
// seeing the one contradiction a person would want resolved.
func TestAReviewSomeoneAskedForShowsTheModelBorderlinePairs(t *testing.T) {
	svc, tenantRepo, models := newConsolidationHarness(t)
	models.response = `{"statement":"我叫wizardchen，我是一个作家"}`
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := svc.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	seedItem(t, svc, ctx, scope, types.MemoryKindProfile, "", "我叫wizard，我是一个画家", "p-a")
	seedItem(t, svc, ctx, scope, types.MemoryKindProfile, "职业", "我叫wizardchen，我是一个作家", "p-b")

	items, _, err := svc.repo.ListItems(ctx, scope, types.MemoryStatusActive, 50, 0)
	require.NoError(t, err)
	require.Empty(t, clusterSimilar(items),
		"this pair is below the unattended bar; that is what makes it worth asking about")

	result, err := svc.ConsolidateNow(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Candidates)
	require.Equal(t, 1, result.Merged)
	require.Equal(t, 2, result.Reviewed)
}

// Candidate selection is recall, not judgement. The model is asked about every
// group and says so when the records are different things, and that answer has
// to survive as "we looked" rather than "nothing looked alike".
func TestTheModelGetsTheFinalSayOnWhatIsADuplicate(t *testing.T) {
	svc, tenantRepo, models := newConsolidationHarness(t)
	models.response = `{"statement":""}`
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := svc.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	seedSimilarPreferences(t, svc, ctx, scope)

	result, err := svc.ConsolidateNow(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.Merged)
	require.Equal(t, 1, result.Candidates)
	require.Equal(t, types.MemoryConsolidationSkipModelDeclined, result.Skipped)

	_, total, err := svc.ListItems(ctx, types.MemoryStatusActive, 10, 0)
	require.NoError(t, err)
	require.Equal(t, int64(2), total, "the model said these are different things")
}

// Merging supersedes wordings the user gave us. Doing that on a token overlap
// because the model was unreachable would destroy information on the strength
// of a heuristic that was never meant to decide anything.
func TestAnUnreachableModelStopsTheReviewInsteadOfGuessing(t *testing.T) {
	svc, tenantRepo, _ := newConsolidationHarness(t)
	svc.modelService = nil
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := svc.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	seedSimilarPreferences(t, svc, ctx, scope)

	result, err := svc.ConsolidateNow(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.Merged)
	require.Equal(t, types.MemoryConsolidationSkipModelUnavailable, result.Skipped)

	_, total, err := svc.ListItems(ctx, types.MemoryStatusActive, 10, 0)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
}

// Zeroes are the normal outcome, so a review that changed nothing has to say
// which kind of nothing it was.
func TestAReviewThatChangesNothingSaysWhy(t *testing.T) {
	svc, tenantRepo, _ := newConsolidationHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := svc.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	seedItem(t, svc, ctx, scope, types.MemoryKindInterest, "小微SDK设备接入", "小微SDK设备接入", "i-a")
	seedItem(t, svc, ctx, scope, types.MemoryKindInterest, "WeKnora混合检索", "WeKnora混合检索", "i-b")

	result, err := svc.ConsolidateNow(ctx)
	require.NoError(t, err)
	require.Equal(t, types.MemoryConsolidationSkipNoCandidates, result.Skipped)
	require.Equal(t, 2, result.Reviewed)
}

func TestScheduledConsolidationIgnoresAHandfulOfMemories(t *testing.T) {
	svc, tenantRepo, models := newConsolidationHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := svc.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	seedSimilarPreferences(t, svc, ctx, scope)

	svc.consolidateIfDue(ctx, scope, svc.workspaceConfig(ctx, 1), "chat-1")
	require.Zero(t, models.callCount(),
		"the daily pass must not spend a model call on a handful of items")
	_, total, err := svc.ListItems(ctx, types.MemoryStatusActive, 10, 0)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
}
