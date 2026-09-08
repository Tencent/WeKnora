package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func executionMessage(session, id string, at time.Time) *types.Message {
	return &types.Message{
		ID: id, SessionID: session, Role: "assistant", AgentID: "report-agent", AgentTenantID: 1,
		IsCompleted: true, CreatedAt: at, Content: "The second request returned the requested date range.",
		AgentSteps: types.AgentSteps{{Thought: "PRIVATE_THOUGHT", ToolCalls: []types.ToolCall{
			{
				ID:     "failed",
				Name:   "report_query",
				Args:   map[string]interface{}{"date": "08/09/2026", "api_key": "do-not-save"},
				Result: &types.ToolResult{Success: false, Error: "invalid date format"},
			},
			{
				ID:     "fixed",
				Name:   "report_query",
				Args:   map[string]interface{}{"date": "2026-09-08"},
				Result: &types.ToolResult{Success: true, Output: "rows for 2026-09-08; verified date range"},
			},
		}}},
	}
}

func lessonResponse(t *testing.T, content string) string {
	t.Helper()
	data, err := json.Marshal(map[string]interface{}{
		"task_summary": "Tried a local date and got invalid date format; ISO date succeeded with " +
			"verified range. No broader claim.",
		"memories": []interface{}{
			map[string]interface{}{
				"action":  "add",
				"kind":    "preference",
				"topic":   "language",
				"content": "Reply in Chinese",
				"source":  1,
			},
			map[string]interface{}{
				"action":  "add",
				"kind":    "experience",
				"topic":   "report_query date format",
				"content": content,
				"experience": map[string]interface{}{
					"trigger": "report_query invalid date format", "applicability": "report API v2 ISO dates",
					"avoid": "Local date strings fail", "outcome": "success", "evidence": []int{1, 2},
				},
			},
		},
	})
	require.NoError(t, err)
	return string(data)
}

func TestUnifiedExtractionAndCrossSessionFileRecall(t *testing.T) {
	svc, tenants, messages, models, queue := newExtractionHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	ctx = types.WithMemoryAgentScope(ctx, "report-agent", 1)
	base := time.Now().Add(-time.Hour)
	messages.set(
		"first",
		[]*types.Message{
			userMessage("first", "Please reply in Chinese and query the report", base),
			executionMessage("first", "answer", base.Add(time.Second)),
		},
	)
	procedure := "1. Check the report API version.\n2. Use ISO dates.\n" + strings.Repeat(
		"Keep the exact date and verify the returned rows.\n",
		12,
	) + "3. Stop if the returned range differs."
	models.response = lessonResponse(t, procedure)
	svc.ScheduleExtraction(ctx, "first", "answer", "model")
	require.Equal(t, 1, drainExtractions(t, svc, queue), "one unified queued task")
	require.Equal(t, 1, models.calls, "user memory and task evidence share one extraction call")
	require.Contains(t, models.lastPrompt, "invalid date format")
	require.Contains(t, models.lastPrompt, "verified date range")
	require.Contains(t, models.lastPrompt, "Assistant outcome report")
	require.NotContains(t, models.lastPrompt, "PRIVATE_THOUGHT")
	require.NotContains(t, models.lastPrompt, "do-not-save")
	items, _, err := svc.ListItems(ctx, types.MemoryStatusActive, 20, 0)
	require.NoError(t, err)
	var lesson *types.MemoryItem
	for _, item := range items {
		if item.Kind == types.MemoryKindExperience {
			lesson = item
		}
	}
	require.NotNil(t, lesson)
	require.Equal(t, procedure, lesson.Content)
	require.Len(t, lesson.Experience.Evidence, 2)
	require.Equal(t, "first", lesson.Experience.Evidence[0].SessionID)
	later := types.WithSessionID(ctx, "second")
	recall := svc.Recall(later, "report_query invalid date format")
	require.Contains(t, recall.Prompt, "Reply in Chinese")
	require.Contains(t, recall.Prompt, "memory://items/"+lesson.ID+".md")
	require.NotContains(t, recall.Prompt, "Stop if the returned range differs", "full procedure is loaded on demand")
	detail, err := svc.ReadMemoryResource(later, "memory://items/"+lesson.ID+".md")
	require.NoError(t, err)
	require.Contains(t, detail, procedure)
	evidence, err := svc.ReadMemoryResource(later, "memory://evidence/"+lesson.ID+".md")
	require.NoError(t, err)
	require.Contains(t, evidence, "invalid date format")
	require.Contains(t, evidence, "verified date range")
	require.NotContains(t, evidence, "do-not-save")
	require.NotContains(t, evidence, "PRIVATE_THOUGHT")
	index, err := svc.ReadMemoryResource(later, "memory://MEMORY.md")
	require.NoError(t, err)
	require.Contains(t, index, lesson.ID)
	for _, bad := range []context.Context{
		types.WithMemoryAgentScope(ctx, "different-agent", 1), types.WithMemoryAgentScope(ctx, "report-agent", 2),
		enabledCtx(t, tenants, 1, "bob"), enabledCtx(t, tenants, 2, "alice"), types.WithMemoryDisabled(ctx),
	} {
		_, err = svc.ReadMemoryResource(bad, "memory://items/"+lesson.ID+".md")
		require.Error(t, err)
		require.NotContains(t, svc.Recall(bad, "report_query invalid date format").Prompt, lesson.ID)
	}
	require.NoError(t, svc.DeleteItem(ctx, lesson.ID))
	_, err = svc.ReadMemoryResource(ctx, "memory://evidence/"+lesson.ID+".md")
	require.Error(t, err)
	// Even a replay after deletion cannot resurrect the same source evidence.
	scope, _ := ResolveScope(ctx)
	segment := transcriptSegment{
		sessionID:    "first",
		observations: messageObservations(executionMessage("first", "answer", base)),
	}
	parsed, err := parseExtractionResponse(models.response)
	require.NoError(t, err)
	require.NoError(t, svc.applyDecisions(ctx, scope, &types.MemoryConfig{}, segment, nil, parsed.Memories))
	active, _, err := svc.ListItems(ctx, types.MemoryStatusActive, 20, 0)
	require.NoError(t, err)
	for _, item := range active {
		require.NotEqual(t, types.MemoryKindExperience, item.Kind)
	}
}

func TestExperienceRequiresActualMatchingEvidence(t *testing.T) {
	segment := transcriptSegment{observations: messageObservations(executionMessage("s", "a", time.Now()))}
	for _, decision := range []*experienceDecision{
		nil,
		{Trigger: "error", Applicability: "v2", Outcome: "success", Evidence: []int{99}},
		{Trigger: "error", Applicability: "v2", Outcome: "success", Evidence: []int{1}},
		{Trigger: "error", Applicability: "v2", Outcome: "failure", Evidence: []int{2}},
	} {
		_, err := resolveExperience(segment, decision)
		require.Error(t, err)
	}
	svc, _, tenants := newMemoryHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	scope, _ := ResolveScope(ctx)
	require.NoError(
		t,
		svc.applyDecisions(
			ctx,
			scope,
			&types.MemoryConfig{},
			segment,
			nil,
			[]extractionDecision{{Action: "add", Kind: "profile", Content: "The user is an administrator"}},
		),
	)
	items, _, err := svc.ListItems(ctx, "", 10, 0)
	require.NoError(t, err)
	require.Empty(t, items)
	e, err := resolveExperience(
		segment,
		&experienceDecision{Trigger: "error", Applicability: "v2", Outcome: "uncertain", Evidence: []int{1}},
	)
	require.NoError(t, err)
	item, err := svc.Remember(
		ctx,
		types.MemoryItem{
			Kind:       types.MemoryKindExperience,
			Content:    "Cause not verified",
			Experience: e,
			Inferred:   true,
		},
	)
	require.NoError(t, err)
	require.Equal(t, types.MemoryStatusPending, item.Status)
	_, err = svc.ReadMemoryResource(types.WithMemoryAgentScope(ctx, "report-agent", 1), "memory://items/"+item.ID+".md")
	require.Error(t, err)
}

func TestConcurrentSessionsAndEqualTimestampPaging(t *testing.T) {
	svc, tenants, messages, models, queue := newExtractionHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	models.response = `{"memories":[]}`
	at := time.Now().Add(-time.Hour)
	var backlog []*types.Message
	for i := 0; i < extractMaxMessagesPerRun*2; i++ {
		m := userMessage("a", fmt.Sprintf("message-%03d", i), at)
		backlog = append(backlog, m)
	}
	messages.set("a", backlog)
	messages.set("b", []*types.Message{userMessage("b", "newer session", at.Add(time.Hour))})
	svc.ScheduleExtraction(ctx, "a", "a", "model")
	svc.ScheduleExtraction(ctx, "b", "b", "model")
	drainExtractions(t, svc, queue)
	for _, m := range backlog {
		require.Contains(t, models.seenTranscripts(), m.Content)
	}
	require.Contains(t, models.seenTranscripts(), "newer session")
}

func TestUnfinishedTurnIsReadWhenItFinishesAfterAnotherSession(t *testing.T) {
	svc, tenants, messages, models, queue := newExtractionHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	models.response = `{"memories":[]}`
	at := time.Now().Add(-time.Hour)
	answer := executionMessage("slow", "answer", at.Add(time.Second))
	answer.IsCompleted = false
	messages.set("slow", []*types.Message{userMessage("slow", "query a report", at), answer})
	svc.ScheduleExtraction(ctx, "slow", "answer", "model")
	drainExtractions(t, svc, queue)
	messages.set("fast", []*types.Message{userMessage("fast", "later request", at.Add(time.Minute))})
	svc.ScheduleExtraction(ctx, "fast", "later", "model")
	drainExtractions(t, svc, queue)
	require.NotContains(t, models.seenTranscripts(), "invalid date format")
	answer.IsCompleted = true
	svc.ScheduleExtraction(ctx, "slow", "answer", "model")
	drainExtractions(t, svc, queue)
	require.Contains(t, models.lastPrompt, "invalid date format")
}

func TestExtractionLeasePreventsDuplicateModelCalls(t *testing.T) {
	svc, tenants, messages, models, queue := newExtractionHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	models.response = `{"memories":[]}`
	messages.set("s", []*types.Message{userMessage("s", "hello", time.Now())})
	svc.ScheduleExtraction(ctx, "s", "m", "model")
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:alice"}
	acquired, err := svc.repo.TryAcquireExtraction(ctx, scope, "other-worker", time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.True(t, acquired)
	task := queue.pop()
	require.NoError(t, svc.Handle(ctx, task))
	require.Zero(t, models.calls)
	require.NoError(t, svc.repo.ReleaseExtraction(ctx, scope, "other-worker"))
	require.NoError(t, svc.Handle(ctx, task))
	require.Equal(t, 1, models.calls)
}

func TestExperienceFindsOldProcedureByBodyAndPreservesManualNotes(t *testing.T) {
	svc, _, tenants := newMemoryHarness(t)
	ctx := types.WithMemoryAgentScope(enabledCtx(t, tenants, 1, "alice"), "report-agent", 1)
	scope, _ := ResolveScope(ctx)
	_, err := svc.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	for i := 0; i < 230; i++ {
		item := &types.MemoryItem{
			ID:            fmt.Sprintf("lesson-%03d", i),
			TenantID:      scope.TenantID,
			SubjectID:     scope.SubjectID,
			Kind:          types.MemoryKindExperience,
			Status:        types.MemoryStatusActive,
			Origin:        types.MemoryOriginExtracted,
			Topic:         fmt.Sprintf("procedure %d", i),
			Content:       "General report procedure",
			NormalizedKey: fmt.Sprintf("key-%d", i),
			ValidFrom:     time.Now().Add(time.Duration(i) * time.Second),
			Experience: &types.MemoryExperience{
				AgentID:       "report-agent",
				AgentTenantID: 1,
				Trigger:       "report task",
				Applicability: "API v2",
				Outcome:       "success",
				Evidence:      []types.MemoryEvidence{{MessageID: "m", ToolName: "report_query"}},
			},
		}
		if i == 0 {
			item.Content = strings.Repeat("Detailed steps. ", 100) + "Repair UNIQUE_ERROR_742 by checking ISO dates."
		}
		require.NoError(t, svc.repo.CreateItem(ctx, item))
	}
	recalled := svc.Recall(ctx, "UNIQUE_ERROR_742")
	require.Contains(t, recalled.Prompt, "lesson-000")
	require.NotContains(t, recalled.Prompt, "Detailed steps.")
	files, err := svc.MemoryResources(ctx)
	require.NoError(t, err)
	require.Contains(t, files["memory://items/lesson-000.md"], "UNIQUE_ERROR_742")
	require.Len(t, files, 230)
	other, err := svc.MemoryResources(types.WithMemoryAgentScope(ctx, "other", 1))
	require.NoError(t, err)
	require.Empty(t, other)
}

func TestExtractionKeepsMiddleFailureAndUsesExitStatus(t *testing.T) {
	msg := executionMessage("s", "m", time.Now())
	var calls []types.ToolCall
	for i := 0; i < 50; i++ {
		calls = append(
			calls,
			types.ToolCall{
				ID:   fmt.Sprintf("call-%d", i),
				Name: "shell_exec",
				Result: &types.ToolResult{
					Success: true,
					Output:  "command done",
					Data:    map[string]interface{}{"exit_code": 0},
				},
			},
		)
	}
	calls[25].Result = &types.ToolResult{
		Success: true,
		Output:  "failed with E_MIDDLE",
		Data:    map[string]interface{}{"exit_code": float64(7), "truncated": true},
	}
	msg.AgentSteps[0].ToolCalls = calls
	observations := messageObservations(msg)
	require.Len(t, observations, extractMaxObservations)
	found := false
	for _, o := range observations {
		if o.evidence.ToolCallID == "call-25" {
			found = true
			require.False(t, o.evidence.Success)
			require.Contains(t, o.content, "E_MIDDLE")
			require.Contains(t, o.content, `"truncated":true`)
		}
	}
	require.True(t, found)
}

func TestLaterTurnIncludesPreviousFailureButCannotReextractOldEvidenceAlone(t *testing.T) {
	svc, tenants, messages, _, _ := newExtractionHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	at := time.Now().Add(-time.Hour)
	before := executionMessage("s", "before", at)
	after := executionMessage("s", "after", at.Add(time.Minute))
	messages.set("s", []*types.Message{before, userMessage("s", "Retry with ISO", at.Add(30*time.Second)), after})
	segments, _, err := svc.collectSegments(
		ctx,
		[]string{"s"},
		map[string]types.MemoryExtractionCursor{"s": {At: at, ID: "before"}},
	)
	require.NoError(t, err)
	require.Len(t, segments, 1)
	require.True(t, segments[0].observations[0].historical)
	_, err = resolveExperience(
		segments[0],
		&experienceDecision{Trigger: "error", Applicability: "API v2", Outcome: "failure", Evidence: []int{1}},
	)
	require.Error(t, err)
	_, err = resolveExperience(
		segments[0],
		&experienceDecision{
			Trigger:       "error",
			Applicability: "API v2",
			Outcome:       "success",
			Evidence:      []int{1, len(segments[0].observations)},
		},
	)
	require.NoError(t, err)
}

func TestDisabledTurnDoesNotLeakThroughLaterEnabledTurn(t *testing.T) {
	svc, tenants, messages, models, queue := newExtractionHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	at := time.Now().Add(-time.Hour)
	disabled := false
	user := userMessage("s", "PRIVATE_DISABLED_USER", at)
	user.ExecutionContext.MemoryEnabled = &disabled
	answer := executionMessage("s", "disabled", at.Add(time.Second))
	answer.ExecutionContext.MemoryEnabled = &disabled
	messages.set("s", []*types.Message{user, answer, userMessage("s", "enabled task", at.Add(time.Minute))})
	models.response = `{"memories":[]}`
	svc.ScheduleExtraction(ctx, "s", "later", "model")
	drainExtractions(t, svc, queue)
	require.NotContains(t, models.lastPrompt, "PRIVATE_DISABLED_USER")
	require.NotContains(t, models.lastPrompt, "invalid date format")
}

func TestConsolidationDoesNotRewriteDeliberateNotesOrMergeDifferentScopes(t *testing.T) {
	svc, _, tenants := newMemoryHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	scope, _ := ResolveScope(ctx)
	a := &types.MemoryItem{
		ID:      "a",
		Kind:    types.MemoryKindPreference,
		Content: "Reply directly with the conclusion",
		Origin:  types.MemoryOriginManual,
	}
	b := &types.MemoryItem{ID: "b", Kind: a.Kind, Content: a.Content, Origin: types.MemoryOriginExtracted}
	require.Empty(t, svc.mergeCandidates(ctx, scope, &types.MemoryConfig{}, []*types.MemoryItem{a, b}, 0.1, 0.9))
}

func TestAutomaticRestatementCannotSupersedeManualContentUnderAnotherTopic(t *testing.T) {
	svc, _, tenants := newMemoryHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	original, err := svc.Remember(
		ctx,
		types.MemoryItem{
			Kind:    types.MemoryKindPreference,
			Topic:   "manual",
			Content: "Reply directly with the conclusion",
			Origin:  types.MemoryOriginManual,
		},
	)
	require.NoError(t, err)
	next, err := svc.Remember(
		ctx,
		types.MemoryItem{
			Kind:    types.MemoryKindPreference,
			Topic:   "different topic",
			Content: "Reply directly with the conclusion and include three paragraphs",
			Origin:  types.MemoryOriginExtracted,
		},
	)
	require.NoError(t, err)
	require.Equal(t, original.ID, next.ID)
	require.Equal(t, original.Content, next.Content)
}

func TestRepeatedExperienceRetainsEvidenceFromBothSessions(t *testing.T) {
	svc, _, tenants := newMemoryHarness(t)
	ctx := types.WithMemoryAgentScope(enabledCtx(t, tenants, 1, "alice"), "report-agent", 1)
	var saved *types.MemoryItem
	for _, session := range []string{"first", "second"} {
		msg := executionMessage(session, session+"-message", time.Now())
		experience, err := resolveExperience(
			transcriptSegment{observations: messageObservations(msg)},
			&experienceDecision{
				Trigger:       "report error",
				Applicability: "API v2",
				Outcome:       "success",
				Evidence:      []int{1, 2},
			},
		)
		require.NoError(t, err)
		saved, err = svc.Remember(
			ctx,
			types.MemoryItem{
				Kind:       types.MemoryKindExperience,
				Topic:      "dates",
				Content:    "Use ISO dates and verify the returned range",
				Origin:     types.MemoryOriginExtracted,
				Experience: experience,
			},
		)
		require.NoError(t, err)
	}
	require.Len(t, saved.Experience.Evidence, 4)
}
