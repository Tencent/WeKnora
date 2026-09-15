package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// This file is about one property: while memory is switched on, every user
// message is eventually read by distillation.
//
// It exists because the first version of the scheduler compared the current
// time against the last run and returned early inside the interval, which
// silently discarded every turn in that window — the feature looked enabled and
// quietly learned nothing. Timers may delay a message; they may not lose it.

func userMessage(sessionID, content string, at time.Time) *types.Message {
	return &types.Message{
		ID:        content,
		SessionID: sessionID,
		Role:      "user",
		Content:   content,
		CreatedAt: at,
	}
}

// accountResponse is what a well-behaved account-writing call returns. Tests
// that are about the pipeline rather than about the account itself use it so
// that a run reaches the end of the write path.
func accountResponse(title, summary string, notes ...string) string {
	body, err := json.Marshal(map[string]any{
		"summary": summary,
		"title":   title,
		"outcome": "success",
		"notes":   notes,
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

// settledConversation builds a conversation an account is due for: enough of
// the user in it to be worth writing about, and quiet for long enough that the
// idle window has passed.
func settledConversation(sessionID string, lines ...string) []*types.Message {
	base := time.Now().Add(-episodeIdleWindow - time.Hour)
	messages := make([]*types.Message, 0, len(lines))
	for i, line := range lines {
		messages = append(messages, userMessage(sessionID, line, base.Add(time.Duration(i)*time.Minute)))
	}
	return messages
}

// drainExtractions runs every task the service queued, plus any follow-ups
// those runs queue, until the queue is empty. Bounded so a scheduling bug
// shows up as a failure rather than a hang.
func drainExtractions(t *testing.T, svc *Service, enqueuer *stubEnqueuer) int {
	t.Helper()
	runs := 0
	for i := 0; i < 50; i++ {
		task := enqueuer.pop()
		if task == nil {
			return runs
		}
		require.NoError(t, svc.Handle(context.Background(), task))
		runs++
	}
	t.Fatal("extraction did not settle: follow-up tasks kept queueing")
	return runs
}

// TestEveryTurnIsEventuallyRead is the headline guarantee. Turns arrive faster
// than the debounce window, so most of them are recorded while a run is already
// in flight; all of them must still reach the model.
func TestEveryTurnIsEventuallyRead(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto,
		ExtractDelaySeconds: 5, ExtractMinIntervalSeconds: 1,
	})
	models.response = accountResponse("十二句话", "用户连着说了十二句话。")

	base := time.Now().Add(-time.Hour)
	var transcript []*types.Message
	for i := 0; i < 12; i++ {
		content := fmt.Sprintf("第 %d 句话", i)
		transcript = append(transcript, userMessage("session-1", content, base.Add(time.Duration(i)*time.Second)))
		messages.set("session-1", transcript)
		svc.ScheduleExtraction(ctx, "session-1", fmt.Sprintf("message-%d", i), "model-1")
	}

	drainExtractions(t, svc, enqueuer)

	seen := models.seenTranscripts()
	for i := 0; i < 12; i++ {
		require.Contains(t, seen, fmt.Sprintf("第 %d 句话", i),
			"turn %d was never read by distillation", i)
	}
}

// TestTurnsDuringARunAreNotLost covers the narrow window that the queue exists
// for: a message that arrives after a run has already taken its work.
func TestTurnsDuringARunAreNotLost(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, ExtractDelaySeconds: 5,
	})
	models.response = accountResponse("两句话", "用户说了两句话。")

	base := time.Now().Add(-time.Hour)
	messages.set("session-1", []*types.Message{userMessage("session-1", "第一句", base)})
	svc.ScheduleExtraction(ctx, "session-1", "message-1", "model-1")

	first := enqueuer.pop()
	require.NotNil(t, first)

	// The second turn lands while the first run is still queued.
	messages.set("session-1", []*types.Message{
		userMessage("session-1", "第一句", base),
		userMessage("session-1", "第二句", base.Add(time.Second)),
	})
	svc.ScheduleExtraction(ctx, "session-1", "message-2", "model-1")

	require.NoError(t, svc.Handle(context.Background(), first))
	drainExtractions(t, svc, enqueuer)

	seen := models.seenTranscripts()
	require.Contains(t, seen, "第一句")
	require.Contains(t, seen, "第二句")
}

// TestParallelSessionsAreAllRead: a person talking in two conversations must
// not have one of them ignored because the other triggered the run.
func TestParallelSessionsAreAllRead(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, ExtractDelaySeconds: 5,
	})
	models.response = accountResponse("两个会话", "用户同时开了两个会话。")

	messages.set("session-a", settledConversation("session-a", "会话A说的话", "会话A的后一句"))
	messages.set("session-b", settledConversation("session-b", "会话B说的话", "会话B的后一句"))

	svc.ScheduleExtraction(ctx, "session-a", "message-a", "model-1")
	svc.ScheduleExtraction(ctx, "session-b", "message-b", "model-1")
	drainExtractions(t, svc, enqueuer)

	seen := models.seenTranscripts()
	require.Contains(t, seen, "会话A说的话")
	require.Contains(t, seen, "会话B说的话")
}

// A conversation has one account, revised as the conversation grows. The
// revision is shown what it concluded last time, because otherwise it re-reads
// the early turns cold and its judgement of them wanders between runs — the
// same afternoon would be described differently every time somebody spoke.
func TestARewriteIsShownItsPreviousAccount(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, ExtractDelaySeconds: 5,
	})
	scope, err := ResolveScope(ctx)
	require.NoError(t, err)

	transcript := settledConversation("session-1", "先问入库失败", "再问批量大小")
	messages.set("session-1", transcript)
	models.response = accountResponse("入库失败", "用户在排查入库失败，先怀疑是批量太大。")
	svc.ScheduleExtraction(ctx, "session-1", "message-2", "model-1")
	drainExtractions(t, svc, enqueuer)

	transcript = append(transcript,
		userMessage("session-1", "调到 20 就过了", transcript[1].CreatedAt.Add(time.Minute)))
	messages.set("session-1", transcript)
	models.response = accountResponse("入库失败", "用户把批量调到 20 后入库通过。")
	svc.ScheduleExtraction(ctx, "session-1", "message-3", "model-1")
	drainExtractions(t, svc, enqueuer)

	require.Contains(t, models.lastPromptContaining(episodeTranscriptHeading), "用户在排查入库失败",
		"the rewrite has to see what it concluded about the earlier turns")

	stored, err := svc.repo.EpisodeBySession(context.Background(), scope, "session-1")
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Equal(t, "用户把批量调到 20 后入库通过。", stored.Summary,
		"the account is revised in place, not appended to")

	_, total, err := svc.repo.ListEpisodes(context.Background(), scope, 10, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), total, "one conversation, one account")
}

// TestFailedRunLeavesMessagesUnread: a model error must not consume the
// messages it failed on, or a transient outage would silently erase them.
func TestFailedRunLeavesMessagesUnread(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, ExtractDelaySeconds: 5,
	})

	messages.set("session-1", settledConversation("session-1", "重要的一句", "还有一句"))
	models.failNext = true
	svc.ScheduleExtraction(ctx, "session-1", "message-2", "model-1")

	task := enqueuer.pop()
	require.NotNil(t, task)
	require.Error(t, svc.Handle(context.Background(), task))

	// The next turn schedules a fresh run, which must see the message again.
	models.response = accountResponse("重要的话", "用户说了两句要紧的话。")
	svc.ScheduleExtraction(ctx, "session-1", "message-3", "model-1")
	drainExtractions(t, svc, enqueuer)
	require.Contains(t, models.seenTranscripts(), "重要的一句")
}

// TestScheduleUsesTheConfiguredDelay pins that the timers are configuration,
// not constants.
func TestScheduleUsesTheConfiguredDelay(t *testing.T) {
	svc, tenantRepo, _, _, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, ExtractDelaySeconds: 7,
	})

	svc.ScheduleExtraction(ctx, "session-1", "message-1", "model-1")
	require.Len(t, enqueuer.options, 1)
	require.Equal(t, 7*time.Second, enqueuer.options[0].processIn)
}

// TestMinIntervalDefersInsteadOfDropping is the exact behaviour change: a turn
// arriving soon after a run is queued further out, not discarded.
func TestMinIntervalDefersInsteadOfDropping(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto,
		ExtractDelaySeconds: 5, ExtractMinIntervalSeconds: 600,
	})
	models.response = accountResponse("两句话", "用户说了两句话。")

	base := time.Now().Add(-time.Hour)
	messages.set("session-1", []*types.Message{userMessage("session-1", "第一句", base)})
	svc.ScheduleExtraction(ctx, "session-1", "message-1", "model-1")
	first := enqueuer.pop()
	require.NotNil(t, first)
	require.NoError(t, svc.Handle(context.Background(), first))

	// Immediately after a run: well inside the ten-minute floor.
	messages.set("session-1", []*types.Message{
		userMessage("session-1", "第一句", base),
		userMessage("session-1", "第二句", base.Add(time.Second)),
	})
	svc.ScheduleExtraction(ctx, "session-1", "message-2", "model-1")

	require.Len(t, enqueuer.tasks, 1, "the turn must still be scheduled, not dropped")
	last := enqueuer.options[len(enqueuer.options)-1]
	require.Greater(t, last.processIn, 5*time.Second,
		"the minimum interval must push the run out rather than discard the turn")

	require.NoError(t, svc.Handle(context.Background(), enqueuer.pop()))
	require.Contains(t, models.seenTranscripts(), "第二句")
}

// TestNothingIsScheduledWhileMemoryIsOff is the other half of the promise: the
// guarantee applies while the switch is on, and costs nothing while it is off.
func TestNothingIsScheduledWhileMemoryIsOff(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	tenantRepo.set(1, &types.MemoryConfig{Enabled: false})
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "alice"})

	messages.set("session-1", []*types.Message{userMessage("session-1", "一句话", time.Now())})
	svc.ScheduleExtraction(ctx, "session-1", "message-1", "model-1")

	require.Empty(t, enqueuer.tasks)
	require.Zero(t, models.calls)
}

// transcriptBlock returns just the conversation the model is asked to describe,
// so a test can distinguish what was shown as instruction from what was shown
// as data.
func transcriptBlock(prompt string) string {
	start := strings.LastIndex(prompt, episodeTranscriptHeading)
	if start < 0 {
		return ""
	}
	return prompt[start:]
}

var (
	_ = json.Marshal
	_ asynq.Task
)

// A model that returns nothing must not be mistaken for a conversation with
// nothing in it.
//
// This is the failure the token ceiling actually produces in the field: a
// reasoning model spends the whole completion budget on its own deliberation
// and returns an empty string with finish_reason=length. Treating that as
// "nothing worth recording" advanced the watermark over messages no model had
// ever read, so the run reported success, the coverage guarantee held on paper,
// and the feature learned nothing — silently, forever.
func TestATruncatedRunDoesNotSwallowTheMessages(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, ExtractDelaySeconds: 1,
	})
	// Truncate every attempt, including the retry with more room.
	models.truncateUntilCall = 99

	messages.set("session-1", settledConversation("session-1", "我在做医疗影像的后端", "主要是分割模型"))
	svc.ScheduleExtraction(ctx, "session-1", "message-2", "model-1")

	task := enqueuer.pop()
	require.NotNil(t, task)
	require.NoError(t, svc.Handle(context.Background(), task))
	require.NotEmpty(t, enqueuer.tasks, "invalid output must schedule another attempt")

	scope, err := ResolveScope(ctx)
	require.NoError(t, err)
	pending, err := svc.repo.HasPendingExtraction(context.Background(), scope)
	require.NoError(t, err)
	require.True(t, pending,
		"the watermark must not advance over messages the model never read")

	// The same conversation is still there to be read once the model can answer.
	models.truncateUntilCall = 0
	models.response = accountResponse("医疗影像后端", "用户在做医疗影像的后端，主要是分割模型。")
	svc.ScheduleExtraction(ctx, "session-1", "message-2", "model-1")
	drainExtractions(t, svc, enqueuer)

	stored, err := svc.repo.EpisodeBySession(context.Background(), scope, "session-1")
	require.NoError(t, err)
	require.NotNil(t, stored, "the conversation must still be distilled after the model recovers")
}

// A model that only needs more room gets it, without the caller ever seeing a
// failure.
func TestTruncationIsRetriedWithMoreRoom(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, ExtractDelaySeconds: 1,
	})
	models.truncateUntilCall = 1
	models.response = accountResponse("医疗影像后端", "用户在做医疗影像的后端，主要是分割模型。")

	messages.set("session-1", settledConversation("session-1", "我在做医疗影像的后端", "主要是分割模型"))
	svc.ScheduleExtraction(ctx, "session-1", "message-2", "model-1")
	drainExtractions(t, svc, enqueuer)

	require.Greater(t, models.lastCallContaining(episodeTranscriptHeading).budget, episodeBudgetTokens,
		"the retry has to offer more room than the attempt that ran out of it")

	scope, err := ResolveScope(ctx)
	require.NoError(t, err)
	stored, err := svc.repo.EpisodeBySession(context.Background(), scope, "session-1")
	require.NoError(t, err)
	require.NotNil(t, stored)
}

// Every other structured-output call in this codebase disables thinking. The
// memory calls are a classification job with a fixed schema, so reasoning buys
// nothing and on a model that reasons by default it eats the whole budget.
func TestExtractionDoesNotAskTheModelToThink(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, ExtractDelaySeconds: 1,
	})
	models.response = accountResponse("随便聊聊", "用户随便聊了两句。")

	messages.set("session-1", settledConversation("session-1", "随便说点什么", "再随便说一句"))
	svc.ScheduleExtraction(ctx, "session-1", "message-2", "model-1")
	drainExtractions(t, svc, enqueuer)

	thinking := models.lastThinkingAsked()
	require.NotNil(t, thinking, "leaving it unset defers to the model, which is how this broke")
	require.False(t, *thinking)
}

// "Blank extraction model" is the default and the settings UI promises it means
// "use the model the conversation used". When nothing can be resolved, the run
// used to log and return success, which advanced the watermark over messages no
// model had read. A workspace on defaults therefore had memory enabled, tasks
// succeeding, and nothing whatsoever learned.
func TestNoAvailableModelDoesNotConsumeTheMessages(t *testing.T) {
	svc, tenantRepo, messages, _, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto,
		ExtractModelID: "", ExtractDelaySeconds: 1,
	})

	messages.set("session-1", settledConversation("session-1", "我在做医疗影像的后端", "主要是分割模型"))
	// Schedule with no conversation model either, which is what the QA path
	// actually passes: the effective model is resolved inside the pipeline and
	// never written back onto the message.
	svc.ScheduleExtraction(ctx, "session-1", "message-2", "")

	task := enqueuer.pop()
	require.NotNil(t, task)
	require.Error(t, svc.Handle(context.Background(), task))

	scope, err := ResolveScope(ctx)
	require.NoError(t, err)
	pending, err := svc.repo.HasPendingExtraction(context.Background(), scope)
	require.NoError(t, err)
	require.True(t, pending, "messages no model ever read must not be marked as read")
}
