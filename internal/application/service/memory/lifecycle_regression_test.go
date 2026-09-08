package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type failingEvidenceMessages struct {
	interfaces.MessageRepository
	missingID string
	failure   error
}

func (r *failingEvidenceMessages) GetMessage(ctx context.Context, sessionID, id string) (*types.Message, error) {
	if id == r.missingID {
		return nil, r.failure
	}
	return r.MessageRepository.GetMessage(ctx, sessionID, id)
}

func TestEvidenceReadSurvivesUnavailableMessage(t *testing.T) {
	for _, failure := range []error{gorm.ErrRecordNotFound, errors.New("temporary database failure")} {
		t.Run(failure.Error(), func(t *testing.T) {
			svc, tenants, messages, _, _ := newExtractionHarness(t)
			ctx := types.WithMemoryAgentScope(enabledCtx(t, tenants, 1, "alice"), "report-agent", 1)
			missing := executionMessage("s", "missing", time.Now())
			kept := executionMessage("s", "kept", time.Now())
			messages.set("s", []*types.Message{kept})
			evidence := append(messageObservations(missing), messageObservations(kept)...)
			exp, err := resolveExperience(transcriptSegment{observations: evidence}, &experienceDecision{
				Trigger: "report error", Applicability: "API v2", Outcome: "success", Evidence: []int{1, 2, 3, 4},
			})
			require.NoError(t, err)
			item, err := svc.Remember(ctx, types.MemoryItem{
				Kind: types.MemoryKindExperience, Content: "Use ISO dates and verify the range", Experience: exp,
			})
			require.NoError(t, err)
			svc.messageRepo = &failingEvidenceMessages{
				MessageRepository: messages,
				missingID:         "missing",
				failure:           failure,
			}
			text, err := svc.ReadMemoryResource(ctx, "memory://evidence/"+item.ID+".md")
			require.NoError(t, err)
			require.Contains(t, text, "Message missing: evidence unavailable")
			require.Contains(t, text, "Message kept / call fixed")
			require.Contains(t, text, "rows for 2026-09-08")
			detail, err := svc.ReadMemoryResource(ctx, "memory://items/"+item.ID+".md")
			require.NoError(t, err)
			require.Contains(t, detail, item.Content)
		})
	}
}

func TestUserMemoryMutationsRefreshSemanticRecall(t *testing.T) {
	for _, kind := range []string{types.MemoryKindFact, types.MemoryKindExperience} {
		for _, mutation := range []string{"edit", "confirm"} {
			t.Run(kind+"/"+mutation, func(t *testing.T) {
				svc, tenants, _ := newVectorHarness(t)
				ctx := types.WithMemoryAgentScope(enabledCtx(t, tenants, 1, "alice"), "report-agent", 1)
				item := types.MemoryItem{Kind: kind, Topic: "回答方式", Content: "配置连接池", Inferred: mutation == "confirm"}
				if mutation == "confirm" {
					item.Content = "回答时请直接给结论并保持内容简洁"
				}
				if kind == types.MemoryKindExperience {
					item.Experience = &types.MemoryExperience{
						AgentID: "report-agent", AgentTenantID: 1,
						Trigger: "回答请求", Applicability: "报告", Outcome: "uncertain",
						Evidence: []types.MemoryEvidence{
							{SessionID: "s", MessageID: "m", ToolCallID: "c", ToolName: "report_query"},
						},
					}
					if mutation == "edit" {
						item.Experience.Outcome = "success"
					}
				}
				saved, err := svc.Remember(ctx, item)
				require.NoError(t, err)
				scope := scopeFor(t, ctx)
				if mutation == "edit" {
					saved, err = svc.UpdateItem(ctx, saved.ID, "回答时请直接给结论并保持内容简洁", 3)
				} else {
					require.Equal(t, types.MemoryStatusPending, saved.Status)
					require.NoError(t, svc.repo.DeleteItemEmbedding(ctx, scope, saved.ID))
					saved, err = svc.ConfirmItem(ctx, saved.ID)
				}
				require.NoError(t, err)
				vectors, err := svc.repo.ItemEmbeddings(ctx, scope, []string{saved.ID}, "embed-1")
				require.NoError(t, err)
				require.NotEmpty(t, vectors, "mutation must refresh vectors without waiting for maintenance")
				recall := svc.Recall(ctx, "别铺垫那么多")
				if kind == types.MemoryKindExperience {
					require.Contains(t, recall.Prompt, saved.ID)
				} else {
					require.NotEmpty(t, recall.Items)
					require.Equal(t, saved.ID, recall.Items[0].ID)
				}
			})
		}
	}
}

func TestExpiredLeaseRecoversAllClaimedSessions(t *testing.T) {
	svc, tenants, messages, models, queue := newExtractionHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	scope := scopeFor(t, ctx)
	at := time.Now().Add(-time.Hour)
	first := userMessage("first", "already processed", at)
	first.ID = "first-message"
	second := userMessage("second", "recover this orphaned session", at.Add(time.Second))
	second.ID = "second-message"
	messages.set("first", []*types.Message{first})
	messages.set("second", []*types.Message{second})
	svc.ScheduleExtraction(ctx, "first", first.ID, "model")
	svc.ScheduleExtraction(ctx, "second", second.ID, "model")
	task := queue.pop()
	acquired, err := svc.repo.TryAcquireExtraction(ctx, scope, "dead-worker", time.Now().Add(-time.Second))
	require.NoError(t, err)
	require.True(t, acquired)
	claimed, _, err := svc.repo.ClaimPendingSessions(ctx, scope)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"first", "second"}, claimed)
	require.NoError(
		t,
		svc.repo.AdvanceExtraction(ctx, scope, "first", types.MemoryExtractionCursor{At: at, ID: first.ID}),
	)
	subject, err := svc.repo.GetSubject(ctx, scope)
	require.NoError(t, err)
	require.Empty(t, subject.PendingSessions)
	require.True(t, subject.ExtractionProgress["first"].Claimed, "advancing a segment must not acknowledge its session")
	require.True(t, subject.ExtractionProgress["second"].Claimed)
	// Simulate a crash without release/requeue defers; retry payload names only first.
	models.response = `{"memories":[]}`
	require.NoError(t, svc.Handle(ctx, task))
	require.Equal(t, 1, models.calls)
	require.Contains(t, models.lastPrompt, "recover this orphaned session")
	subject, err = svc.repo.GetSubject(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, second.ID, subject.ExtractionProgress["second"].ID)
	require.False(t, subject.ExtractionProgress["first"].Claimed)
	require.False(t, subject.ExtractionProgress["second"].Claimed)
}

func TestClaimAcknowledgementPreservesNewTurnsAndRejectsOtherOwners(t *testing.T) {
	svc, _, tenants := newMemoryHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := svc.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	_, _, err = svc.repo.EnqueuePendingSession(ctx, scope, "s", time.Minute)
	require.NoError(t, err)
	_, err = svc.repo.TryAcquireExtraction(ctx, scope, "owner", time.Now().Add(time.Minute))
	require.NoError(t, err)
	claimed, _, err := svc.repo.ClaimPendingSessions(ctx, scope)
	require.NoError(t, err)
	_, _, err = svc.repo.EnqueuePendingSession(ctx, scope, "s", time.Minute)
	require.NoError(t, err)
	require.Error(t, svc.repo.CompleteExtractionSessions(ctx, scope, "different-owner", claimed))
	require.NoError(t, svc.repo.CompleteExtractionSessions(ctx, scope, "owner", claimed))
	pending, _, err := svc.repo.ClaimPendingSessions(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, []string{"s"}, pending)
}

func TestExtractionRanksOldExperienceBeforeLimitingCandidates(t *testing.T) {
	svc, tenants, messages, models, queue := newExtractionHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := svc.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	at := time.Now().Add(-time.Hour)
	for i := 0; i < 230; i++ {
		content := "unrelated procedure"
		if i == 0 {
			content = "LEGACY_RECOVERY_TARGET: Repair E_OLD_529 with an ISO date."
		}
		require.NoError(t, svc.repo.CreateItem(ctx, &types.MemoryItem{
			ID: fmt.Sprintf(
				"candidate-%d",
				i,
			), TenantID: 1, SubjectID: scope.SubjectID, Kind: types.MemoryKindExperience,
			Status: types.MemoryStatusActive, Origin: types.MemoryOriginExtracted, Topic: fmt.Sprintf("topic-%d", i),
			NormalizedKey: fmt.Sprintf(
				"key-%d",
				i,
			), Content: content, ValidFrom: at.Add(time.Duration(i) * time.Second),
			Experience: &types.MemoryExperience{AgentID: "report-agent", AgentTenantID: 1, Applicability: "API v2"},
		}))
	}
	user := userMessage("s", "Fix E_OLD_529", time.Now())
	user.ID = "user"
	messages.set("s", []*types.Message{user, executionMessage("s", "answer", time.Now().Add(time.Second))})
	models.response = `{"memories":[]}`
	svc.ScheduleExtraction(ctx, "s", "answer", "model")
	require.NoError(t, svc.Handle(ctx, queue.pop()))
	require.Contains(t, models.lastPromptContaining("Existing notes:"), "LEGACY_RECOVERY_TARGET")
}
