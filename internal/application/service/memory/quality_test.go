package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// This file covers the distillation-quality work: what the model is shown, how
// its answer is interpreted, and what is refused. Each test names the concrete
// failure it exists to prevent, because most of them were found by probing the
// running system rather than by reading the code.

// ---------------------------------------------------------------------------
// Provenance
// ---------------------------------------------------------------------------

// TestProvenancePointsAtTheRightMessage covers the regression that arrived with
// multi-session runs: everything a run wrote used to be attributed to the turn
// that triggered it, which could be a different conversation entirely.
func TestProvenancePointsAtTheRightMessage(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, ExtractDelaySeconds: 5,
	})
	scope, err := ResolveScope(ctx)
	require.NoError(t, err)

	messages.set("session-a", settledConversation("session-a", "我在做医疗影像", "分割模型怎么调参"))
	messages.set("session-b", settledConversation("session-b", "顺便问下天气", "周末会下雨吗"))
	models.responseFor = map[string]string{
		"我在做医疗影像": accountResponse("医疗影像", "用户在做医疗影像，正在调分割模型。"),
		"顺便问下天气":  accountResponse("天气", "用户顺便问了周末的天气。"),
	}

	svc.ScheduleExtraction(ctx, "session-a", "trigger-msg", "model-1")
	svc.ScheduleExtraction(ctx, "session-b", "trigger-msg", "model-1")
	drainExtractions(t, svc, enqueuer)

	medical, err := svc.repo.EpisodeBySession(context.Background(), scope, "session-a")
	require.NoError(t, err)
	require.NotNil(t, medical)
	require.Contains(t, medical.Summary, "医疗影像")

	weather, err := svc.repo.EpisodeBySession(context.Background(), scope, "session-b")
	require.NoError(t, err)
	require.NotNil(t, weather)
	require.NotContains(t, weather.Summary, "医疗影像",
		"an account belongs to the conversation it describes")
}

// ---------------------------------------------------------------------------
// One conversation, one account
// ---------------------------------------------------------------------------

func TestSeparateSessionsAreSeparateAccounts(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, ExtractDelaySeconds: 5,
	})
	models.response = accountResponse("两个会话", "用户同时开了两个会话。")

	messages.set("session-a", settledConversation("session-a", "会话A的话", "会话A的后一句"))
	messages.set("session-b", settledConversation("session-b", "会话B的话", "会话B的后一句"))
	svc.ScheduleExtraction(ctx, "session-a", "a1", "model-1")
	svc.ScheduleExtraction(ctx, "session-b", "b1", "model-1")
	drainExtractions(t, svc, enqueuer)

	accounts := 0
	for _, prompt := range models.promptsSeen() {
		if !strings.Contains(prompt, episodeTranscriptHeading) {
			continue
		}
		accounts++
		block := transcriptBlock(prompt)
		require.False(t, strings.Contains(block, "会话A的话") && strings.Contains(block, "会话B的话"),
			"two conversations must not be merged into one call")
	}
	require.Equal(t, 2, accounts, "two conversations, two accounts")
}

// ---------------------------------------------------------------------------
// Workspace instructions
// ---------------------------------------------------------------------------

func TestWorkspaceInstructionsReachThePrompt(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, ExtractDelaySeconds: 5,
		ExtractInstructions: "永远不要记录客户的姓名",
	})
	models.response = accountResponse("随便聊聊", "用户随便聊了两句。")
	messages.set("session-1", settledConversation("session-1", "随便聊", "再聊一句"))

	svc.ScheduleExtraction(ctx, "session-1", "m2", "model-1")
	drainExtractions(t, svc, enqueuer)
	require.Contains(t, models.lastPromptContaining(episodeTranscriptHeading), "永远不要记录客户的姓名")
}

// ---------------------------------------------------------------------------
// Structured output
// ---------------------------------------------------------------------------

func TestExtractionRequestsStructuredOutput(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, ExtractDelaySeconds: 5,
	})
	models.response = accountResponse("随便聊聊", "用户随便聊了两句。")
	messages.set("session-1", settledConversation("session-1", "随便聊", "再聊一句"))

	svc.ScheduleExtraction(ctx, "session-1", "m2", "model-1")
	drainExtractions(t, svc, enqueuer)

	schema := models.lastCallContaining(episodeTranscriptHeading).format
	require.NotEmpty(t, schema,
		"the response schema must be sent, not just described in prose")
	require.Contains(t, string(schema), "summary")
}

var _ = context.Background
