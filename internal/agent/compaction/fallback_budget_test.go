package compaction

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFallbackHistorySharesSummaryBudget(t *testing.T) {
	for _, previous := range []string{"", "Remember the project goal.", strings.Repeat("先确认需求，然后执行。", 2000)} {
		t.Run(fmt.Sprintf("previous_bytes_%d", len(previous)), func(t *testing.T) {
			c := New(&stubChat{err: assert.AnError}, newEstimator(t), testSettings())
			messages := []chat.Message{{Role: "user", Content: strings.Repeat("旧的对话内容。", 100)}}
			for range 40 {
				messages = append(messages, chat.Message{Role: "tool", Content: filler(100)})
			}
			messages = append(messages, chat.Message{Role: "user", Content: "最新任务：生成 final_report.csv"})
			text, degraded := c.summarizeHistory(t.Context(), &Preparation{
				PreviousSummary: previous, MessagesToSummarize: messages,
			})
			require.True(t, degraded)
			assert.LessOrEqual(t, c.estimator.EstimateString(text), c.settings.summaryBudget())
			assert.True(t, utf8.ValidString(text))
			assert.Contains(t, text, "最新任务：生成 final_report.csv")
			if previous != "" {
				assert.Contains(t, text, strings.Split(previous, "。")[0])
				assert.Contains(t, text, "context omitted")
			}
		})
	}
}

func TestFallbackHistoryBoundsOversizedNewestMessage(t *testing.T) {
	for _, scale := range []float64{0.5, 1, 1.5} {
		t.Run(fmt.Sprintf("scale_%g", scale), func(t *testing.T) {
			estimator := newEstimator(t)
			estimator.SetScale(scale)
			c := New(&stubChat{err: assert.AnError}, estimator, testSettings())
			message := chat.Message{Role: "assistant", Content: "Latest result: done."}
			for range 500 {
				message.ToolCalls = append(message.ToolCalls, chat.ToolCall{Function: chat.FunctionCall{
					Name: "read_file", Arguments: `{"path":"/workspace/项目资料.csv"}`,
				}})
			}
			text, degraded := c.summarizeHistory(t.Context(), &Preparation{
				MessagesToSummarize: []chat.Message{message},
			})
			require.True(t, degraded)
			assert.LessOrEqual(t, estimator.EstimateString(text), c.settings.summaryBudget())
			assert.Contains(t, text, "Latest result: done.")
			assert.Contains(t, text, "context omitted")
			assert.True(t, utf8.ValidString(text))
		})
	}
}

func TestFallbackHistoryPreservesSmallInputs(t *testing.T) {
	c := New(&stubChat{err: assert.AnError}, newEstimator(t), testSettings())
	p := &Preparation{PreviousSummary: "Original goal.", MessagesToSummarize: []chat.Message{
		{Role: "user", Content: "New requirement."},
	}}
	text, degraded := c.summarizeHistory(t.Context(), p)
	require.True(t, degraded)
	assert.Equal(t, joinNonEmpty(p.PreviousSummary,
		rawArchive(p.MessagesToSummarize, c.settings.summaryBudget(), c.estimator)), text)
}

func TestFallbackCheckpointsStayBoundedAcrossReloads(t *testing.T) {
	model := &stubChat{err: assert.AnError}
	c := New(model, newEstimator(t), testSettings())
	previous := "Original goal: prepare a report."
	for cycle := range 8 {
		messages := []chat.Message{{Role: "system", Content: "You are an agent."}, SummaryMessage(previous)}
		for turn := range 50 {
			messages = append(messages, storedTurn(fmt.Sprintf("cycle-%d-turn-%d", cycle, turn),
				1, fmt.Sprintf("/workspace/report-%d.txt", cycle))...)
		}
		messages = append(messages, chat.Message{Role: "user", Content: "Continue with the next task."})
		result, err := c.Compact(t.Context(), messages, ReasonThreshold)
		require.NoError(t, err)
		require.NotNil(t, result.Checkpoint)
		require.True(t, result.Checkpoint.Degraded)
		prep := Prepare(messages, c.settings, c.estimator)
		metadata := prep.historyFileOps.format()
		previous = result.Checkpoint.Summary
		text := strings.TrimSuffix(previous, metadata)
		assert.LessOrEqual(t, c.estimator.EstimateString(text), c.settings.summaryBudget(), "cycle %d", cycle)
		assert.Contains(t, previous, "Original goal: prepare a report.")
		_, modified := parseFileOpsBlock(previous)
		assert.Contains(t, modified, fmt.Sprintf("/workspace/report-%d.txt", cycle))
		t.Logf("cycle=%d summary_text_tokens=%d checkpoint_tokens=%d", cycle+1,
			c.estimator.EstimateString(text), c.estimator.EstimateString(previous))
	}
	model.err, model.response = nil, "Recovered summary of the report."
	text, degraded := c.summarizeHistory(t.Context(), &Preparation{
		PreviousSummary: previous, MessagesToSummarize: []chat.Message{{Role: "user", Content: "New work."}},
	})
	assert.False(t, degraded)
	assert.Equal(t, model.response, text)
	assert.Contains(t, model.prompts[len(model.prompts)-1], previous)
}
