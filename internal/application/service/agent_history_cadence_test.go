package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/compaction"
	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// cadenceChat answers every summarization with a fixed, modest summary, or,
// when failing, errors the way a summarizer that times out on large inputs
// does every time.
type cadenceChat struct{ failing bool }

func (c cadenceChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	if c.failing {
		return nil, errors.New("summarization stalled")
	}
	return &types.ChatResponse{Content: "## Goal\n" + strings.Repeat("summary ", 200), FinishReason: "stop"}, nil
}

func (c cadenceChat) ChatStream(
	ctx context.Context, messages []chat.Message, opts *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	resp, err := c.Chat(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	ch := make(chan types.StreamResponse, 1)
	ch <- types.StreamResponse{
		ResponseType: types.ResponseTypeAnswer, Content: resp.Content, Done: true, FinishReason: resp.FinishReason,
	}
	close(ch)
	return ch, nil
}
func (cadenceChat) GetModelName() string { return "cadence" }
func (cadenceChat) GetModelID() string   { return "cadence" }

// cadenceRepo keeps checkpoints on its rows, the way the messages table does.
type cadenceRepo struct{ historyRepo }

func (r *cadenceRepo) GetLatestContextCheckpoint(context.Context, string) (*types.Message, error) {
	var latest *types.Message
	for _, m := range r.rows {
		if m.ContextCheckpoint != nil && (latest == nil || sortsAtOrBefore(latest, m)) {
			latest = m
		}
	}
	return latest, nil
}

func (r *cadenceRepo) UpdateMessageContextCheckpoint(
	_ context.Context, _, messageID string, checkpoint *types.ContextCheckpoint,
) error {
	for _, m := range r.rows {
		if m.ID == messageID && m.Role == "assistant" {
			m.ContextCheckpoint = checkpoint
			return nil
		}
	}
	return fmt.Errorf("no assistant message %s", messageID)
}

// runSessionCadence plays turns through the real loader, budget and compactor
// the way AgentQA and the engine do: load history, compact at round one when
// the request crosses the threshold, persist the checkpoint compaction offers.
// It fails the test the moment a stored turn after the checkpoint is missing
// from the loaded history, and returns the turns that compacted.
// A positive scale stands for the provider's tokens per estimated token that
// the engine measured and stored on each turn; the loader returns it and the
// engine's estimator, shared with its compactor, runs at it.
func runSessionCadence(t *testing.T, turns, systemTokens, turnTokens int, scale float64) []int {
	t.Helper()
	return runSessionCadenceWith(t, cadenceChat{}, turns, systemTokens, turnTokens, scale)
}

func runSessionCadenceWith(
	t *testing.T, summarizer cadenceChat, turns, systemTokens, turnTokens int, scale float64,
) []int {
	t.Helper()
	const window = 40000
	cfg := &types.AgentConfig{MaxContextTokens: window}
	est, err := agenttoken.NewEstimator()
	require.NoError(t, err)
	settings := compaction.Settings{Enabled: true, MaxContextTokens: window, ReserveTokens: 16384}
	compactor := compaction.New(summarizer, est, settings)
	repo := &cadenceRepo{}
	system := chat.Message{Role: "system", Content: strings.Repeat("rule ", systemTokens)}

	var compacted []int
	for i := 1; i <= turns; i++ {
		ctx := context.Background()
		history, loadedScale, err := LoadAgentHistory(ctx, repo, "s1", agent.HistoryTokenBudget(cfg), false)
		require.NoError(t, err)
		est.SetScale(loadedScale)
		requireNoGapAfterCheckpoint(t, repo, history, i)

		request := append(append([]chat.Message{system}, history...),
			chat.Message{Role: "user", Content: fmt.Sprintf("question %d", i)})
		if settings.ShouldCompact(est.EstimateMessages(request)) {
			result, err := compactor.Compact(ctx, request, compaction.ReasonThreshold)
			require.NoError(t, err)
			compacted = append(compacted, i)
			if cp := result.Checkpoint; cp != nil {
				require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, "s1", cp.TurnID,
					&types.ContextCheckpoint{Summary: cp.Summary}))
			}
		}

		at := historyBase.Add(time.Duration(i) * time.Minute)
		req := fmt.Sprintf("req-%d", i)
		repo.rows = append(repo.rows,
			&types.Message{
				ID: fmt.Sprintf("u%03d", i), RequestID: req, Role: "user",
				Content: fmt.Sprintf("question %d", i), CreatedAt: at,
			},
			&types.Message{
				ID: fmt.Sprintf("a%03d", i), RequestID: req, Role: "assistant", IsCompleted: true,
				Content: strings.Repeat("word ", turnTokens), CreatedAt: at.Add(time.Second),
				Usage: &types.TokenUsage{ContextTokenScale: scale},
			},
		)
	}
	return compacted
}

// requireNoGapAfterCheckpoint checks the loaded history replays every stored
// turn after the newest checkpoint, in order: a turn neither summarized nor
// replayed is gone for good.
func requireNoGapAfterCheckpoint(t *testing.T, repo *cadenceRepo, history []chat.Message, turn int) {
	t.Helper()
	first := 1
	if cp, _ := repo.GetLatestContextCheckpoint(context.Background(), "s1"); cp != nil {
		_, err := fmt.Sscanf(cp.ID, "a%03d", &first)
		require.NoError(t, err)
		first++
	}
	var want, got []string
	for n := first; n < turn; n++ {
		want = append(want, fmt.Sprintf("a%03d", n))
	}
	for _, m := range history {
		if m.TurnID != "" && (len(got) == 0 || got[len(got)-1] != m.TurnID) {
			got = append(got, m.TurnID)
		}
	}
	require.Equal(t, want, got, "turn %d: stored turns after the checkpoint must all be replayed", turn)
}

// The regression this guards: with a loading budget equal to the compaction
// threshold, the loader trimmed the oldest turns first, so whenever one turn
// was larger than the system prompt plus the new question the request never
// crossed the threshold. Nothing was summarized, no checkpoint was written,
// and the dropped turns were simply gone. Loading up to the window leaves the
// overflow to compaction: turns are either replayed or summarized, compaction
// recurs as the session refills, and the turn after one does not compact again.
func TestAgentHistoryCompactsInsteadOfDroppingTurns(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		turns, systemTokens, turnTokens int
		scale                           float64
	}{
		{"turns larger than the system prompt", 60, 300, 1000, 0},
		{"turns larger than the keep-recent budget", 60, 300, 9000, 0},
		{"system prompt larger than a turn", 60, 2000, 1000, 0},
		// Calibrated sessions: loading and the compaction trigger must scale
		// together, or the loader drops turns under a trigger that never fires.
		// Cheaper turns take longer to refill the window.
		{"provider counts fewer tokens than the estimate", 90, 300, 1000, 0.6},
		{"provider counts more tokens than the estimate", 60, 300, 1000, 1.4},
		{"large turns, provider counts fewer tokens", 60, 300, 9000, 0.6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compacted := runSessionCadence(t, tc.turns, tc.systemTokens, tc.turnTokens, tc.scale)
			t.Logf("compacted at turns %v", compacted)

			require.GreaterOrEqual(t, len(compacted), 2, "a session that keeps growing must keep compacting")
			for i := 1; i < len(compacted); i++ {
				require.Greater(t, compacted[i]-compacted[i-1], 1,
					"turn %d compacted right after turn %d: the checkpoint did not take", compacted[i], compacted[i-1])
			}
		})
	}
}

// Calibration is what makes the window fill: with the provider spending 0.6
// tokens per estimated one, the first compaction comes well after the one an
// uncalibrated session reaches, because the estimate no longer overstates it.
func TestCalibratedSessionFillsMoreOfTheWindow(t *testing.T) {
	uncalibrated := runSessionCadence(t, 60, 300, 1000, 0)
	calibrated := runSessionCadence(t, 60, 300, 1000, 0.6)
	require.NotEmpty(t, uncalibrated)
	require.NotEmpty(t, calibrated)
	require.Greater(t, calibrated[0], uncalibrated[0]*3/2)
}

// A summarizer that fails every time (the large request that always times out)
// used to leave no checkpoint, so every later turn reloaded the same history
// and compacted it again. The raw archive is now kept as a degraded
// checkpoint: compaction still recurs only as the session refills, and no
// stored turn goes missing.
func TestAgentHistoryDoesNotRecompactAfterTheSummarizerFails(t *testing.T) {
	compacted := runSessionCadenceWith(t, cadenceChat{failing: true}, 60, 300, 1000, 0)
	t.Logf("compacted at turns %v", compacted)

	require.GreaterOrEqual(t, len(compacted), 2)
	for i := 1; i < len(compacted); i++ {
		require.Greater(t, compacted[i]-compacted[i-1], 1,
			"turn %d compacted right after turn %d: the failed compaction left no checkpoint",
			compacted[i], compacted[i-1])
	}
}
