package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// The callback runs after the real service has read its inputs and entered
// the model, but before that model returns. Database operations remain real.
type clearingModelService struct {
	interfaces.ModelService
	beforeReturn func()
}

func (m *clearingModelService) GetChatModel(ctx context.Context, id string) (chat.Chat, error) {
	model, err := m.ModelService.GetChatModel(ctx, id)
	return &clearingChat{delegateChat: model, beforeReturn: m.beforeReturn}, err
}

type delegateChat = chat.Chat

type clearingChat struct {
	delegateChat
	beforeReturn func()
}

func (m *clearingChat) Chat(
	ctx context.Context, messages []chat.Message, opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	result, err := m.delegateChat.Chat(ctx, messages, opts)
	m.beforeReturn()
	return result, err
}

func assertClearedStore(t *testing.T, svc *Service, scope interfaces.MemoryScope) {
	t.Helper()
	items, total, err := svc.repo.ListItems(t.Context(), scope, "", 100, 0)
	require.NoError(t, err)
	require.Zero(t, total, "late task restored items: %v", items)
	topics, err := svc.repo.TopTopics(t.Context(), scope, 100)
	require.NoError(t, err)
	require.Empty(t, topics, "late task restored topics")
	subject, err := svc.repo.GetSubject(t.Context(), scope)
	require.NoError(t, err)
	require.Empty(t, subject.BlockText, "late snapshot restored the profile cache")
}

func TestClearFencesExtractionModelResults(t *testing.T) {
	for _, response := range []string{
		`{"memories":[{"action":"add","kind":"fact","topic":"old-secret"` +
			`,"content":"old-secret is PostgreSQL","source":1}],"topics":["old-secret"]}`,
		`{"memories":[],"topics":["old-secret"]}`,
	} {
		t.Run(response, func(t *testing.T) {
			svc, tenants, messages, models, _ := newExtractionHarness(t)
			ctx := enabledCtx(t, tenants, 1, "alice")
			scope := scopeFor(t, ctx)
			messages.messages = []*types.Message{{
				ID: "old", SessionID: "session", Role: "user",
				Content: "old-secret is PostgreSQL", CreatedAt: time.Now().Add(-time.Minute),
			}}
			models.response = response
			cleared := false
			svc.modelService = &clearingModelService{ModelService: models, beforeReturn: func() {
				if !cleared {
					_, err := svc.repo.ClearSubjectMemory(ctx, scope)
					require.NoError(t, err)
					cleared = true
				}
			}}
			_ = svc.Handle(ctx, extractTask(t, types.MemoryExtractPayload{
				TenantID: 1, SubjectID: scope.SubjectID, SessionID: "session", ChatModelID: "model",
			}))
			require.True(t, cleared)
			assertClearedStore(t, svc, scope)
		})
	}
}

func TestClearFencesConsolidationModelResults(t *testing.T) {
	svc, tenants, models := newConsolidationHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := svc.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	seedSimilarPreferences(t, svc, ctx, scope)
	cleared := false
	svc.modelService = &clearingModelService{ModelService: models, beforeReturn: func() {
		_, err := svc.repo.ClearSubjectMemory(ctx, scope)
		require.NoError(t, err)
		cleared = true
	}}
	_, err = svc.ConsolidateNow(ctx)
	require.NoError(t, err)
	require.True(t, cleared)
	assertClearedStore(t, svc, scope)
}

type clearingResidentRepo struct {
	interfaces.MemoryRepository
	clear func()
}

func (r *clearingResidentRepo) ListActiveResident(
	ctx context.Context, scope interfaces.MemoryScope, limit int,
) ([]*types.MemoryItem, error) {
	items, err := r.MemoryRepository.ListActiveResident(ctx, scope, limit)
	if r.clear != nil {
		clearMemory := r.clear
		r.clear = nil
		clearMemory()
	}
	return items, err
}

func TestClearFencesLateProfileSnapshot(t *testing.T) {
	svc, _, tenants := newMemoryHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := svc.Remember(ctx, types.MemoryItem{Kind: types.MemoryKindProfile, Content: "old private biography"})
	require.NoError(t, err)
	repo := svc.repo
	svc.repo = &clearingResidentRepo{MemoryRepository: repo, clear: func() {
		_, err := repo.ClearSubjectMemory(ctx, scope)
		require.NoError(t, err)
	}}
	svc.rebuildBlock(ctx, scope)
	assertClearedStore(t, svc, scope)
	require.NotContains(t, svc.Recall(ctx, "who am I").Prompt, "old private biography")
}

func TestClearCutsOffExtractionHistoryAndPriorContext(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(map[bool]string{true: "queued session", false: "unknown legacy payload"}[known], func(t *testing.T) {
			svc, tenants, messages, models, _ := newExtractionHarness(t)
			ctx := enabledCtx(t, tenants, 1, "alice")
			scope := scopeFor(t, ctx)
			_, err := svc.repo.EnsureSubject(ctx, scope)
			require.NoError(t, err)
			if known {
				_, _, err = svc.repo.EnqueuePendingSession(ctx, scope, "session", time.Minute)
				require.NoError(t, err)
			}
			messages.messages = []*types.Message{{
				ID: "old", SessionID: "session", Role: "user",
				Content: "forgotten-history-marker", CreatedAt: time.Now().Add(-time.Hour),
			}}
			_, err = svc.repo.ClearSubjectMemory(ctx, scope)
			require.NoError(t, err)
			messages.messages = append(messages.messages, &types.Message{
				ID: "new", SessionID: "session", Role: "user",
				Content: "future-message-marker", CreatedAt: time.Now().Add(time.Second),
			})
			if known {
				_, _, err = svc.repo.EnqueuePendingSession(ctx, scope, "session", time.Minute)
				require.NoError(t, err)
			}
			models.response = `{"memories":[]}`
			require.NoError(t, svc.Handle(ctx, extractTask(t, types.MemoryExtractPayload{
				TenantID: 1, SubjectID: scope.SubjectID, SessionID: "session", ChatModelID: "model",
			})))
			require.Contains(t, models.seenTranscripts(), "future-message-marker")
			require.NotContains(t, models.seenTranscripts(), "forgotten-history-marker",
				"both transcript and prior context must respect clear",
			)
		})
	}
}

func TestClearCutoffIncludesEqualTimestampMessages(t *testing.T) {
	svc, tenants, messages, models, _ := newExtractionHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := svc.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	_, err = svc.repo.ClearSubjectMemory(ctx, scope)
	require.NoError(t, err)
	subject, err := svc.repo.GetSubject(ctx, scope)
	require.NoError(t, err)
	cutoff := *subject.ExtractCursor
	messages.messages = []*types.Message{
		{ID: "old-at-cutoff", SessionID: "session", Role: "user", Content: "equal-timestamp-secret", CreatedAt: cutoff},
		{
			ID: "new", SessionID: "session", Role: "user",
			Content: "fresh-timestamp-marker", CreatedAt: cutoff.Add(time.Second),
		},
	}
	models.response = `{"memories":[]}`
	require.NoError(t, svc.Handle(ctx, extractTask(t, types.MemoryExtractPayload{
		TenantID: 1, SubjectID: scope.SubjectID, SessionID: "session", ChatModelID: "model",
	})))
	require.Contains(t, models.seenTranscripts(), "fresh-timestamp-marker")
	require.NotContains(t, models.seenTranscripts(), "equal-timestamp-secret")
}

func TestDisableFencesRunningExtractionWithoutClearingHistory(t *testing.T) {
	svc, tenants, messages, models, _ := newExtractionHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	scope := scopeFor(t, ctx)
	retained := seedAdminItem(t, svc, scope, types.MemoryStatusPending, "retained proposal")
	messages.messages = []*types.Message{{
		ID: "old", SessionID: "session", Role: "user",
		Content: "late private fact", CreatedAt: time.Now(),
	}}
	models.response = `{"memories":[{"action":"add","kind":"fact"` +
		`,"content":"late private fact","source":1}],"topics":["late topic"]}`
	disabled := false
	svc.modelService = &clearingModelService{ModelService: models, beforeReturn: func() {
		require.NoError(t, svc.repo.UpdateSubjectEnabled(ctx, scope, false))
		disabled = true
	}}
	_ = svc.Handle(ctx, extractTask(t, types.MemoryExtractPayload{
		TenantID: 1, SubjectID: scope.SubjectID, SessionID: "session", ChatModelID: "model",
	}))
	require.True(t, disabled)
	items, total, err := svc.repo.ListItems(ctx, scope, "", 100, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total, "opt-out must fence old LLM results")
	require.Equal(t, retained.ID, items[0].ID)
	subject, err := svc.repo.GetSubject(ctx, scope)
	require.NoError(t, err)
	require.False(t, subject.Enabled)
	require.Nil(t, subject.ExtractCursor, "disabling does not delete conversation history")
	admin := memoryAdminContext(ctx, 1, types.TenantRoleAdmin)
	_, err = svc.AdminConfirmItem(admin, subject.ID, retained.ID)
	require.NoError(t, err, "fresh admin operations remain available on disabled subjects")
}

func TestClearOldWorkerCannotReleaseNewLeaseAndFutureWritesWork(t *testing.T) {
	svc, tenants, messages, models, _ := newExtractionHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	scope := scopeFor(t, ctx)
	messages.messages = []*types.Message{{
		ID: "old", SessionID: "session", Role: "user",
		Content: "old memory", CreatedAt: time.Now().Add(-time.Minute),
	}}
	models.response = `{"memories":[{"action":"add","content":"old memory","source":1}]}`
	svc.modelService = &clearingModelService{ModelService: models, beforeReturn: func() {
		_, err := svc.repo.ClearSubjectMemory(ctx, scope)
		require.NoError(t, err)
		_, _, err = svc.repo.EnqueuePendingSession(ctx, scope, "future-session", time.Minute)
		require.NoError(t, err)
		batch, err := svc.repo.ClaimPendingSessions(ctx, scope, "future-session", "new-worker", time.Hour)
		require.NoError(t, err)
		require.NotNil(t, batch)
	}}
	_ = svc.Handle(ctx, extractTask(t, types.MemoryExtractPayload{
		TenantID: 1, SubjectID: scope.SubjectID, SessionID: "session", ChatModelID: "model",
	}))
	subject, err := svc.repo.GetSubject(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, "new-worker", subject.ExtractionState.LeaseID)
	assertClearedStore(t, svc, scope)
	svc.modelService = models
	_, err = svc.Remember(ctx, types.MemoryItem{Kind: types.MemoryKindFact, Content: "future memory is allowed"})
	require.NoError(t, err)
	_, total, err := svc.repo.ListItems(ctx, scope, types.MemoryStatusActive, 100, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
}
