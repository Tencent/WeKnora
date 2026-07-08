package chatpipeline

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

type streamTestChatModel struct {
	responses []types.StreamResponse
}

func (m *streamTestChatModel) Chat(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (*types.ChatResponse, error) {
	return nil, nil
}

func (m *streamTestChatModel) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	ch := make(chan types.StreamResponse, len(m.responses))
	for _, response := range m.responses {
		ch <- response
	}
	close(ch)
	return ch, nil
}

func (m *streamTestChatModel) GetModelName() string { return "stream-test" }
func (m *streamTestChatModel) GetModelID() string   { return "stream-test" }

type streamTestModelService struct {
	chatModel chat.Chat
}

func (s *streamTestModelService) CreateModel(context.Context, *types.Model) error { return nil }
func (s *streamTestModelService) GetModelByID(context.Context, string) (*types.Model, error) {
	return nil, nil
}
func (s *streamTestModelService) ListModels(context.Context) ([]*types.Model, error) { return nil, nil }
func (s *streamTestModelService) UpdateModel(context.Context, *types.Model) error    { return nil }
func (s *streamTestModelService) DeleteModel(context.Context, string) error          { return nil }
func (s *streamTestModelService) UpdateModelCredentials(
	context.Context,
	string,
	*string,
	*string,
) (*types.Model, error) {
	return nil, nil
}
func (s *streamTestModelService) ClearModelCredential(context.Context, string, string) error {
	return nil
}
func (s *streamTestModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return nil, nil
}
func (s *streamTestModelService) GetEmbeddingModelForTenant(
	context.Context,
	string,
	uint64,
) (embedding.Embedder, error) {
	return nil, nil
}
func (s *streamTestModelService) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return nil, nil
}
func (s *streamTestModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.chatModel, nil
}
func (s *streamTestModelService) GetVLMModel(context.Context, string) (vlm.VLM, error) {
	return nil, nil
}
func (s *streamTestModelService) GetASRModel(context.Context, string) (asr.ASR, error) {
	return nil, nil
}

func TestChatCompletionStreamEmptyDoneEmitsTerminalError(t *testing.T) {
	errData := runStreamPluginAndWaitForError(t, []types.StreamResponse{
		{
			ResponseType: types.ResponseTypeAnswer,
			Done:         true,
			FinishReason: "stop",
		},
	})

	if errData.ErrorCode != "chat_stream_empty_answer" {
		t.Fatalf("ErrorCode = %q, want chat_stream_empty_answer", errData.ErrorCode)
	}
	if !strings.Contains(errData.Error, "回答生成失败") {
		t.Fatalf("Error = %q, want Chinese failure message", errData.Error)
	}
}

func TestChatCompletionStreamClosedWithoutDoneEmitsTerminalError(t *testing.T) {
	errData := runStreamPluginAndWaitForError(t, []types.StreamResponse{
		{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "部分答案",
			Done:         false,
		},
	})

	if errData.ErrorCode != "chat_stream_closed" {
		t.Fatalf("ErrorCode = %q, want chat_stream_closed", errData.ErrorCode)
	}
	if !strings.Contains(errData.Error, "回答生成中断") {
		t.Fatalf("Error = %q, want Chinese interruption message", errData.Error)
	}
}

func runStreamPluginAndWaitForError(
	t *testing.T,
	responses []types.StreamResponse,
) event.ErrorData {
	t.Helper()

	bus := event.NewEventBus()
	errCh := make(chan event.ErrorData, 1)
	bus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.ErrorData)
		if !ok {
			t.Fatalf("unexpected error event data type %T", evt.Data)
		}
		errCh <- data
		return nil
	})

	plugin := &PluginChatCompletionStream{
		modelService: &streamTestModelService{
			chatModel: &streamTestChatModel{responses: responses},
		},
	}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SessionID:   "session-1",
			ChatModelID: "model-1",
			Language:    "zh-CN",
			SummaryConfig: types.SummaryConfig{
				Prompt: "You are helpful.",
			},
		},
		PipelineState: types.PipelineState{
			UserContent: "你好",
		},
		PipelineContext: types.PipelineContext{
			EventBus: bus.AsEventBusInterface(),
		},
	}

	if err := plugin.OnEvent(context.Background(), types.CHAT_COMPLETION_STREAM, cm, func() *PluginError {
		return nil
	}); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}

	select {
	case data := <-errCh:
		return data
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for terminal error event")
		return event.ErrorData{}
	}
}
