package im

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// streamFinalizeSessionService answers KnowledgeQA synchronously with a completed
// final answer, mirroring a quick-QA stream.
type streamFinalizeSessionService struct {
	interfaces.SessionService
	answer string
}

func (s *streamFinalizeSessionService) KnowledgeQA(
	ctx context.Context, req *types.QARequest, bus *event.EventBus,
) error {
	return bus.Emit(ctx, event.Event{
		ID:        "answer-1",
		Type:      event.EventAgentFinalAnswer,
		SessionID: req.Session.ID,
		Data: event.AgentFinalAnswerData{
			Content: s.answer,
			Done:    true,
		},
	})
}

type streamFinalizeMessageService struct {
	interfaces.MessageService
}

func (s *streamFinalizeMessageService) CreateMessage(
	_ context.Context, msg *types.Message,
) (*types.Message, error) {
	created := *msg
	created.ID = created.Role + "-message"
	return &created, nil
}

func (s *streamFinalizeMessageService) UpdateMessage(_ context.Context, _ *types.Message) error {
	return nil
}

type streamFinalizeStreamManager struct {
	interfaces.StreamManager
}

func (s *streamFinalizeStreamManager) GetEvents(
	_ context.Context, _, _ string, from int,
) ([]interfaces.StreamEvent, int, error) {
	return nil, from, nil
}

// streamFinalizeAdapter implements Adapter and StreamSender. finalizeErr
// simulates an expired platform streaming channel (e.g. WeCom errcode 846608).
type streamFinalizeAdapter struct {
	Adapter
	finalContent string
	finalizeErr  error
	sendErr      error
	plainReplies int
	plainContent string
}

func (a *streamFinalizeAdapter) StartStream(_ context.Context, _ *IncomingMessage) (string, error) {
	return "stream-1", nil
}

func (a *streamFinalizeAdapter) UpdateStreamContent(
	_ context.Context, _ *IncomingMessage, _ string, _ string,
) error {
	return nil
}

func (a *streamFinalizeAdapter) FinalizeStream(
	_ context.Context, _ *IncomingMessage, _ string, content string,
) error {
	a.finalContent = content
	return a.finalizeErr
}

func (a *streamFinalizeAdapter) EndStream(_ context.Context, _ *IncomingMessage, _ string) error {
	return nil
}

func (a *streamFinalizeAdapter) SendReply(
	_ context.Context, _ *IncomingMessage, reply *ReplyMessage,
) error {
	if a.sendErr != nil {
		return a.sendErr
	}
	a.plainReplies++
	if reply != nil {
		a.plainContent = reply.Content
	}
	return nil
}

func runStreamFinalizeHandler(
	t *testing.T, adapter *streamFinalizeAdapter, answer string,
) {
	t.Helper()
	service := &Service{
		sessionService: &streamFinalizeSessionService{answer: answer},
		messageService: &streamFinalizeMessageService{},
		streamManager:  &streamFinalizeStreamManager{},
	}
	err := service.handleMessageStream(
		context.Background(),
		&IncomingMessage{Platform: PlatformFeishu, UserID: "user-1", Content: "问题"},
		&types.Session{ID: "session-1"}, nil, nil, nil, nil,
		adapter, adapter, "user-key", nil,
	)
	if err != nil {
		t.Fatalf("handleMessageStream() error = %v", err)
	}
}

func TestHandleMessageStreamFinalizeSuccessSkipsPlainReply(t *testing.T) {
	adapter := &streamFinalizeAdapter{}
	runStreamFinalizeHandler(t, adapter, "最终答案")

	if adapter.finalContent != "最终答案" {
		t.Fatalf("final content = %q, want %q", adapter.finalContent, "最终答案")
	}
	if adapter.plainReplies != 0 {
		t.Fatalf("plain fallback replies = %d, want 0", adapter.plainReplies)
	}
}

// TestHandleMessageStreamFinalizeFailureSendsPlainReply covers an expired
// platform streaming channel: the placeholder can no longer be replaced, so the
// final answer must be re-sent as a plain message instead of being dropped.
func TestHandleMessageStreamFinalizeFailureSendsPlainReply(t *testing.T) {
	adapter := &streamFinalizeAdapter{finalizeErr: errors.New("stream expired (errcode 846608)")}
	runStreamFinalizeHandler(t, adapter, "最终答案")

	if adapter.finalContent != "最终答案" {
		t.Fatalf("final content = %q, want %q", adapter.finalContent, "最终答案")
	}
	if adapter.plainReplies != 1 {
		t.Fatalf("plain fallback replies = %d, want 1", adapter.plainReplies)
	}
	if adapter.plainContent != "最终答案" {
		t.Fatalf("plain reply = %q, want %q", adapter.plainContent, "最终答案")
	}
}

func TestHandleMessageStreamTotalDeliveryFailureStillReturnsCleanly(t *testing.T) {
	adapter := &streamFinalizeAdapter{
		finalizeErr: errors.New("stream expired"),
		sendErr:     errors.New("plain reply rejected"),
	}
	runStreamFinalizeHandler(t, adapter, "最终答案")

	if adapter.plainReplies != 0 {
		t.Fatalf("plain fallback replies = %d, want 0", adapter.plainReplies)
	}
}
