package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// The account is written for a conversation that is over. These tests pin the
// two ways "over" is decided, because both failure directions are silent: gate
// too eagerly and every turn pays for an account that the next turn replaces,
// gate too patiently and a long working session is never remembered at all.

func TestAccountWaitsForTheConversationToGoQuiet(t *testing.T) {
	now := time.Now()
	segment := transcriptSegment{
		lines: []transcriptLine{
			{at: now.Add(-20 * time.Minute), tier: tierUser, content: "第一个问题"},
			{at: now.Add(-2 * time.Minute), tier: tierUser, content: "还有个追问"},
		},
		end: now.Add(-2 * time.Minute),
	}

	wait := episodeDeferral(segment, types.MemoryMessageCursor{}, now)

	require.Positive(t, wait, "a conversation two minutes idle is still being had")
	require.InDelta(t, (episodeIdleWindow - 2*time.Minute).Seconds(), wait.Seconds(), 1,
		"the wait should be the remainder of the idle window, not a fresh one")
}

func TestAccountIsWrittenOnceTheConversationIsIdle(t *testing.T) {
	now := time.Now()
	quiet := now.Add(-episodeIdleWindow - time.Minute)
	segment := transcriptSegment{
		lines: []transcriptLine{{at: quiet, tier: tierUser, content: "问题"}},
		end:   quiet,
	}

	require.Zero(t, episodeDeferral(segment, types.MemoryMessageCursor{}, now))
}

func TestALongRunningConversationStopsPostponingItsAccount(t *testing.T) {
	now := time.Now()
	// Active — last turn a minute ago — but running for longer than a
	// conversation is allowed to defer.
	start := now.Add(-episodeMaxDeferral - time.Hour)
	segment := transcriptSegment{
		lines: []transcriptLine{
			{at: start, tier: tierUser, content: "开始"},
			{at: now.Add(-time.Minute), tier: tierUser, content: "还在聊"},
		},
		end: now.Add(-time.Minute),
	}

	require.Zero(t, episodeDeferral(segment, types.MemoryMessageCursor{}, now),
		"three hours of work should be remembered before the user stops working")
}

func TestDeferralIsMeasuredFromTheLastAccountNotTheFirstTurn(t *testing.T) {
	now := time.Now()
	// The conversation began long ago, but an account already covers
	// everything up to a minute before now, so only that minute is unwritten
	// and the max-deferral valve must not fire.
	segment := transcriptSegment{
		lines: []transcriptLine{
			{at: now.Add(-6 * time.Hour), tier: tierUser, content: "很久以前"},
			{at: now.Add(-time.Minute), tier: tierUser, content: "刚说的"},
		},
		end: now.Add(-time.Minute),
	}
	cursor := types.MemoryMessageCursor{At: now.Add(-2 * time.Minute), ID: "m1"}

	require.Positive(t, episodeDeferral(segment, cursor, now),
		"an old conversation with a current account has nothing overdue")
}

// The idle window is a guess about whether a conversation is over, and a
// conversation the person has left behind does not need to be guessed about.
// Writing it when they leave rather than when its countdown expires is the
// whole point: the next conversation is the one that needed the memory.
func TestALeftConversationIsWrittenWithoutWaitingOutTheWindow(t *testing.T) {
	svc, tenantRepo, messages, models, enqueuer := newExtractionHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto,
	})
	scope, err := ResolveScope(ctx)
	require.NoError(t, err)

	// Both conversations are minutes old, so both are inside the idle window
	// and a countdown alone would write neither.
	now := time.Now()
	messages.set("session-left", []*types.Message{
		userMessage("session-left", "我是 wizard，我是程序员", now.Add(-4*time.Minute)),
	})
	messages.set("session-current", []*types.Message{
		userMessage("session-current", "换个话题问点别的", now.Add(-time.Minute)),
	})
	models.response = accountResponse("自我介绍", "用户说自己是 wizard，是程序员。")

	// Scheduled in that order, so the queue records the left conversation as
	// the older one and the claim hands it back first.
	svc.ScheduleExtraction(ctx, "session-left", "m-left", "model-1")
	svc.ScheduleExtraction(ctx, "session-current", "m-current", "model-1")
	require.NoError(t, svc.Handle(context.Background(), enqueuer.pop()))

	left, err := svc.repo.EpisodeBySession(context.Background(), scope, "session-left")
	require.NoError(t, err)
	require.NotNil(t, left,
		"the person moved on from this one, so there is nothing left to wait for")

	current, err := svc.repo.EpisodeBySession(context.Background(), scope, "session-current")
	require.NoError(t, err)
	require.Nil(t, current,
		"the conversation they are still in keeps its window, or every turn rewrites it")
}

// A conversation that stayed at one turn is still worth an account. The gate
// used to want two, which meant a durable self-description said once reached
// nothing at all: the digest is rewritten from accounts and notes and from no
// other source, so a turn that never became an account was never read again.
// Whether a chat was trivial is consolidation's call, made across every
// account at once, not a turn count's.
func TestOneTurnIsEnoughForAnAccount(t *testing.T) {
	said := time.Now().Add(-episodeIdleWindow - time.Minute)
	segment := transcriptSegment{
		lines: []transcriptLine{
			{at: said, tier: tierUser, content: "我是 wizard，我是程序员"},
			{at: said, tier: tierAssistant, content: "很高兴认识你"},
		},
		end: said,
	}

	require.GreaterOrEqual(t, segment.userLineCount(), episodeMinUserLines,
		"a single self-description is the profile material the gate must not drop")
	require.Zero(t, episodeDeferral(segment, types.MemoryMessageCursor{}, time.Now()),
		"and once it has gone quiet there is nothing left to wait for")
}

// The agent's tool trace is the record of what was actually tried. These tests
// pin what reaches the model and, more importantly, what does not.

func TestToolRowsCarryTheCallItsArgumentsAndWhatCameBack(t *testing.T) {
	message := &types.Message{
		ID: "m1", Role: "assistant", Content: "找到了",
		AgentSteps: types.AgentSteps{{
			ToolCalls: []types.ToolCall{{
				Name: "search_knowledge",
				Args: map[string]interface{}{"query": "发票抬头", "top_k": 5},
				Result: &types.ToolResult{
					Success: true, Output: "命中《财务手册》第3节",
				},
			}},
		}},
	}

	rows := evidenceRows(message)

	var tool string
	for _, row := range rows {
		if row.tier == tierTool {
			tool = row.content
		}
	}
	require.NotEmpty(t, tool, "the agent's tool call should reach the model")
	require.Contains(t, tool, "search_knowledge")
	require.Contains(t, tool, "query=发票抬头")
	require.Contains(t, tool, "命中《财务手册》第3节")
}

func TestAFailedToolCallSaysThatItFailed(t *testing.T) {
	message := &types.Message{
		ID: "m1", Role: "assistant",
		AgentSteps: types.AgentSteps{{
			ToolCalls: []types.ToolCall{{
				Name:   "read_document",
				Result: &types.ToolResult{Success: false, Error: "permission denied"},
			}},
		}},
	}

	rows := evidenceRows(message)

	require.Len(t, rows, 1)
	require.Equal(t, tierTool, rows[0].tier)
	require.Contains(t, rows[0].content, "failed: permission denied",
		"where the work stopped is the useful part of a failure")
}

func TestMemoryLookupsAreNotRecordedAsEvidence(t *testing.T) {
	message := &types.Message{
		ID: "m1", Role: "assistant",
		AgentSteps: types.AgentSteps{{
			ToolCalls: []types.ToolCall{
				{Name: "search_memory", Args: map[string]interface{}{"query": "发票"},
					Result: &types.ToolResult{Success: true, Output: "过去问过发票"}},
				{Name: "search_knowledge", Result: &types.ToolResult{Success: true, Output: "命中"}},
			},
		}},
	}

	rows := evidenceRows(message)

	require.Len(t, rows, 1, "only the knowledge search should survive")
	require.Contains(t, rows[0].content, "search_knowledge")
	require.NotContains(t, rows[0].content, "search_memory",
		"memory reading itself would make one account the reason for the next")
}

func TestOneAnswerCannotContributeUnboundedToolRows(t *testing.T) {
	var calls []types.ToolCall
	for i := 0; i < extractToolMaxCalls*3; i++ {
		calls = append(calls, types.ToolCall{
			Name:   "search_knowledge",
			Result: &types.ToolResult{Success: true, Output: "命中"},
		})
	}
	message := &types.Message{ID: "m1", Role: "assistant", AgentSteps: types.AgentSteps{{ToolCalls: calls}}}

	rows := evidenceRows(message)

	require.Len(t, rows, extractToolMaxCalls)
}

// Redaction happens on the way in, because the extraction call is the step
// that leaves the building.

func TestSecretsAreStrippedBeforeTheTranscriptIsBuilt(t *testing.T) {
	secret := "sk-abcdefghijklmnopqrstuvwxyz0123456789"
	message := &types.Message{
		ID: "m1", Role: "user",
		Content: "我的 key 是 " + secret + "，帮我配一下",
	}

	rows := evidenceRows(message)

	require.Len(t, rows, 1)
	require.NotContains(t, rows[0].content, secret,
		"a pasted credential must not travel to the extraction model")
}

// What the user attached is part of what the user asked.

func TestWhatTheUserPointedAtTravelsWithTheirQuestion(t *testing.T) {
	message := &types.Message{
		ID: "m1", Role: "user", Content: "这里面怎么写",
		MentionedItems: types.MentionedItems{
			{Name: "财务手册", Type: "kb"},
			{Name: "发票模板.docx", Type: "file"},
		},
		Images: types.MessageImages{{URL: "u", Caption: "一张报错截图，提示字段缺失"}},
	}

	rows := evidenceRows(message)

	require.Len(t, rows, 1)
	require.Equal(t, tierUser, rows[0].tier)
	require.Contains(t, rows[0].content, "财务手册")
	require.Contains(t, rows[0].content, "发票模板.docx")
	require.Contains(t, rows[0].content, "字段缺失",
		"an attached screenshot is often the whole question")
}

func TestATruncatedConversationSaysSoInThePrompt(t *testing.T) {
	segment := transcriptSegment{
		lines:     []transcriptLine{{tier: tierUser, content: "中间某一句"}, {tier: tierUser, content: "又一句"}},
		truncated: true,
	}

	prompt := buildEpisodePrompt(segment, nil, "", 100000)

	require.Contains(t, prompt, "starts partway in",
		"an account written from a beheaded transcript must not claim to cover the whole conversation")
}

func TestAWholeConversationCarriesNoTruncationNote(t *testing.T) {
	segment := transcriptSegment{
		lines: []transcriptLine{{tier: tierUser, content: "第一句"}, {tier: tierUser, content: "第二句"}},
	}

	require.NotContains(t, buildEpisodePrompt(segment, nil, "", 100000), "starts partway in")
}

// The budget is sized from the model that will read the transcript.

func TestEvidenceBudgetFollowsTheModelsWindow(t *testing.T) {
	require.Equal(t, 20000*episodeEvidencePercent/100, episodeEvidenceBudget(20000))
	require.Equal(t, 1_000_000*episodeEvidencePercent/100, episodeEvidenceBudget(1_000_000),
		"the window is the only ceiling; a large one is not clipped")
	require.Equal(t, episodeEvidenceFallbackRunes, episodeEvidenceBudget(0),
		"a model that declares no window gets the conservative budget")
	require.Equal(t, episodeEvidenceMinRunes, episodeEvidenceBudget(100),
		"a tiny window still has to leave something to read")
}

func TestTheTranscriptStaysWithinTheBudgetItWasGiven(t *testing.T) {
	now := time.Now()
	var lines []transcriptLine
	for i := 0; i < 200; i++ {
		lines = append(lines, transcriptLine{
			at:      now.Add(time.Duration(i) * time.Minute),
			tier:    tierUser,
			content: strings.Repeat("问", 200),
		})
	}

	rendered := renderEvidence(lines, 3000, true)

	require.LessOrEqual(t, len([]rune(rendered)), 3000)
	require.Contains(t, rendered, evidenceOmitted,
		"the model has to be able to tell a gap from silence")
}

func TestTheUsersWordsOutrankToolOutputForTheBudget(t *testing.T) {
	now := time.Now()
	lines := []transcriptLine{
		{at: now, tier: tierTool, content: strings.Repeat("工具输出", 200)},
		{at: now.Add(time.Minute), tier: tierUser, content: "这是用户唯一说的话"},
	}

	rendered := renderEvidence(lines, 400, true)

	require.Contains(t, rendered, "这是用户唯一说的话",
		"tool output must never crowd out what the person said")
}
