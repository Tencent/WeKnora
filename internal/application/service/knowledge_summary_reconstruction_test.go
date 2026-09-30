package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
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

// fixedResponseSummaryChat replays one provider response so a test can drive
// getSummary with the replies that used to be persisted verbatim.
type fixedResponseSummaryChat struct {
	response *types.ChatResponse
}

func (m *fixedResponseSummaryChat) Chat(
	_ context.Context,
	_ []chat.Message,
	_ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	return m.response, nil
}

func (m *fixedResponseSummaryChat) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m *fixedResponseSummaryChat) GetModelName() string { return "summary-fixed" }
func (m *fixedResponseSummaryChat) GetModelID() string   { return "summary-fixed" }

// getSummary is the single call site both summary entry points share
// (ProcessSummaryGeneration and RegenerateKnowledgeSummary), so the error
// returned here is what routes an unusable reply into their existing
// retry/failed path instead of knowledge.description + SummaryStatusCompleted.
func TestGetSummaryRejectsUnusableModelOutput(t *testing.T) {
	tests := []struct {
		name     string
		response *types.ChatResponse
		wantErr  error
	}{
		{
			name: "reply truncated at the completion budget",
			response: &types.ChatResponse{
				Content: `{"summary": "cut off mid-`, FinishReason: "length",
			},
			wantErr: errSummaryOutputTruncated,
		},
		{
			name: "JSON-shaped reply that does not parse",
			response: &types.ChatResponse{
				Content: "{\"summary\": \"line one\nline two\"}",
			},
			wantErr: errSummaryOutputNotParsable,
		},
		{
			name:     "empty reply",
			response: &types.ChatResponse{Content: "  "},
			wantErr:  errEmptySummaryOutput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &knowledgeService{
				config: &config.Config{Conversation: &config.ConversationConfig{
					GenerateSummaryPrompt: "Summarize the document.",
				}},
				chunkRepo: summaryImageInfoChunkRepo{},
			}
			body := "The leave policy grants twenty days of annual leave to every employee."
			result, err := service.getSummary(
				context.Background(),
				&fixedResponseSummaryChat{response: tt.response},
				&types.Knowledge{ID: "knowledge-1"},
				[]*types.Chunk{{
					ID: "first", Content: body, ChunkIndex: 0,
					StartAt: 0, EndAt: len([]rune(body)),
				}},
			)
			require.ErrorIs(t, err, tt.wantErr)
			require.Nil(t, result, "an unusable reply must not become a summary result")
		})
	}
}

// A plain-text template that hits the completion budget still returned usable
// text, and only a structured reply can be half a JSON object. Treating the
// finish reason alone as failure turned a truncated-but-usable custom-template
// summary into a retry loop ending in SummaryStatusFailed.
func TestGetSummaryKeepsTruncatedPlainTextReply(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "plain text at the budget",
			content: "This document explains the leave policy and the twenty days",
		},
		{
			name:    "bracketed prose at the budget",
			content: "[文档摘要] 本文件说明请假制度与年度额度",
		},
		{
			name:    "numbered prose at the budget",
			content: "[1] 本文件说明请假制度与年度额度",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &knowledgeService{
				config: &config.Config{Conversation: &config.ConversationConfig{
					GenerateSummaryPrompt: "Summarize the document.",
				}},
				chunkRepo: summaryImageInfoChunkRepo{},
			}
			body := "The leave policy grants twenty days of annual leave to every employee."
			result, err := service.getSummary(
				context.Background(),
				&fixedResponseSummaryChat{response: &types.ChatResponse{
					Content: tt.content, FinishReason: "length",
				}},
				&types.Knowledge{ID: "knowledge-1"},
				[]*types.Chunk{{
					ID: "first", Content: body, ChunkIndex: 0,
					StartAt: 0, EndAt: len([]rune(body)),
				}},
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.content, result.Summary)
		})
	}
}
