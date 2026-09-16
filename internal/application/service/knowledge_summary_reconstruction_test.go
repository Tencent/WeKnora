package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/models/invoke/invoketest"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type summaryContentCapture struct {
	fake *invoketest.Fake
}

func newSummaryContentCapture(t *testing.T) *summaryContentCapture {
	f := invoketest.New(t)
	f.EnqueueResponse(invoke.ChatResponse{Content: "summary", FinishReason: "stop"})
	return &summaryContentCapture{fake: f}
}

type summaryImageInfoChunkRepo struct {
	interfaces.ChunkRepository
}

func (summaryImageInfoChunkRepo) ListChunksByParentIDs(
	context.Context, uint64, []string,
) ([]*types.Chunk, error) {
	return nil, nil
}

func TestGetSummaryReconstructsTableChunksWithSyntheticHeaders(t *testing.T) {
	header := "| Country | Capital |\n| --- | --- |\n"
	rowOne := "| Alpha Republic | North City |\n"
	rowTwo := "| Beta Federation | East Harbor |\n"
	rowThree := "| Gamma State | South Port |\n"
	want := header + rowOne + rowTwo + rowThree

	firstContent := header + rowOne + rowTwo
	service := &knowledgeService{
		config: &config.Config{Conversation: &config.ConversationConfig{
			GenerateSummaryPrompt: "Summarize the document.",
		}},
		chunkRepo: summaryImageInfoChunkRepo{},
	}
	model := newSummaryContentCapture(t)

	_, err := service.getSummary(context.Background(), model.fake.Config(),
		&types.Knowledge{ID: "knowledge-1"}, []*types.Chunk{
			{
				ID: "first", Content: firstContent, ChunkIndex: 0,
				StartAt: 0, EndAt: len([]rune(firstContent)),
			},
			{
				// The repeated header is synthetic: StartAt points at row two in the source.
				ID: "second", Content: header + rowTwo + rowThree, ChunkIndex: 1,
				StartAt: len([]rune(header + rowOne)), EndAt: len([]rune(want)),
			},
		})
	if err != nil {
		t.Fatalf("getSummary() error = %v", err)
	}
	calls := model.fake.Calls()
	if len(calls) != 1 || len(calls[0].Opts.Messages) != 2 {
		t.Fatalf("summary model received %d calls, first with %d messages, want 1 call with 2 messages",
			len(calls), len(calls[0].Opts.Messages))
	}
	if got := calls[0].Opts.Messages[1].Text(); got != want {
		t.Fatalf("summary content mismatch:\n got: %q\nwant: %q", got, want)
	}
}
