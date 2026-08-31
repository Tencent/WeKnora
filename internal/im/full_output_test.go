package im

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type fullOutputOrder struct {
	mu    sync.Mutex
	steps []string
}

func (o *fullOutputOrder) add(step string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.steps = append(o.steps, step)
}

func (o *fullOutputOrder) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.steps...)
}

type fullOutputSessionService struct {
	interfaces.SessionService
	order  *fullOutputOrder
	answer string
}

func (s *fullOutputSessionService) KnowledgeQA(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
	s.order.add("qa")
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

type fullOutputMessageService struct {
	interfaces.MessageService
}

func (s *fullOutputMessageService) CreateMessage(_ context.Context, msg *types.Message) (*types.Message, error) {
	created := *msg
	created.ID = created.Role + "-message"
	return &created, nil
}

func (s *fullOutputMessageService) UpdateMessage(_ context.Context, _ *types.Message) error {
	return nil
}

type fullOutputStreamManager struct {
	interfaces.StreamManager
}

func (s *fullOutputStreamManager) AppendEvent(_ context.Context, _, _ string, _ interfaces.StreamEvent) error {
	return nil
}

func (s *fullOutputStreamManager) GetEvents(
	_ context.Context, _, _ string, from int,
) ([]interfaces.StreamEvent, int, error) {
	return nil, from, nil
}

type fullOutputAdapter struct {
	Adapter
	order        *fullOutputOrder
	finalContent string
	updates      int
	plainReplies int
}

func (a *fullOutputAdapter) SendReply(_ context.Context, _ *IncomingMessage, _ *ReplyMessage) error {
	a.plainReplies++
	a.order.add("plain-reply")
	return nil
}

func (a *fullOutputAdapter) StartStream(_ context.Context, _ *IncomingMessage) (string, error) {
	a.order.add("start")
	return "full-output-stream", nil
}

func (a *fullOutputAdapter) SupportsFullOutputProgress() bool {
	return true
}

func (a *fullOutputAdapter) UpdateStreamContent(_ context.Context, _ *IncomingMessage, _ string, _ string) error {
	a.updates++
	a.order.add("update")
	return nil
}

func (a *fullOutputAdapter) FinalizeStream(_ context.Context, _ *IncomingMessage, _ string, content string) error {
	a.finalContent = content
	a.order.add("finalize")
	return nil
}

func (a *fullOutputAdapter) EndStream(_ context.Context, _ *IncomingMessage, _ string) error {
	a.order.add("end")
	return nil
}

func TestHandleMessageFullOutputShowsPlaceholderWithoutIntermediateUpdates(t *testing.T) {
	order := &fullOutputOrder{}
	adapter := &fullOutputAdapter{order: order}
	service := &Service{
		sessionService: &fullOutputSessionService{order: order, answer: "最终答案"},
		messageService: &fullOutputMessageService{},
		streamManager:  &fullOutputStreamManager{},
	}
	msg := &IncomingMessage{Platform: PlatformFeishu, UserID: "user-1", Content: "问题"}
	session := &types.Session{ID: "session-1"}

	err := service.handleMessageFullOutput(
		context.Background(), msg, session, nil, nil, nil, nil, adapter, adapter, "user-key", nil,
	)
	if err != nil {
		t.Fatalf("handleMessageFullOutput() error = %v", err)
	}

	wantOrder := []string{"start", "qa", "finalize", "end"}
	if got := order.snapshot(); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("lifecycle order = %v, want %v", got, wantOrder)
	}
	if adapter.updates != 0 {
		t.Fatalf("full output sent %d intermediate updates, want 0", adapter.updates)
	}
	if adapter.finalContent != "最终答案" {
		t.Fatalf("final content = %q, want %q", adapter.finalContent, "最终答案")
	}
	if adapter.plainReplies != 0 {
		t.Fatalf("plain fallback replies = %d, want 0", adapter.plainReplies)
	}
}

func TestFormatIMOutboundAnswerOrFallbackAfterCleanup(t *testing.T) {
	got := formatIMOutboundAnswerOrFallback(
		context.Background(), "<think>only reasoning</think>", nil, nil,
	)
	if got != imNoAnswerFallback {
		t.Fatalf("think-only final content = %q, want fallback %q", got, imNoAnswerFallback)
	}
}
