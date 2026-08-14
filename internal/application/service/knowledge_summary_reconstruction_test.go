package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type summaryContentCaptureChat struct {
	messages []chat.Message
}

func (m *summaryContentCaptureChat) Chat(
	_ context.Context,
	messages []chat.Message,
	_ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.messages = append([]chat.Message(nil), messages...)
	return &types.ChatResponse{Content: "summary"}, nil
}

func (m *summaryContentCaptureChat) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m *summaryContentCaptureChat) GetModelName() string { return "summary-capture" }
func (m *summaryContentCaptureChat) GetModelID() string   { return "summary-capture" }

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
	model := &summaryContentCaptureChat{}

	_, err := service.getSummary(context.Background(), model, &types.Knowledge{ID: "knowledge-1"}, []*types.Chunk{
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
	if len(model.messages) != 2 {
		t.Fatalf("summary model received %d messages, want 2", len(model.messages))
	}
	if got := model.messages[1].Content; got != want {
		t.Fatalf("summary content mismatch:\n got: %q\nwant: %q", got, want)
	}
}
