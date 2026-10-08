package im

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type deliveryMessageService struct {
	fullOutputMessageService
	saved *types.Message
}

func (s *deliveryMessageService) UpdateMessage(ctx context.Context, msg *types.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	saved := *msg
	s.saved = &saved
	return nil
}

func TestHandleMessageStreamFinalDelivery(t *testing.T) {
	cardErr := errors.New("card replacement rejected")
	sendErr := errors.New("fallback rejected")
	endErr := errors.New("card settings rejected")
	for _, tt := range []struct {
		name                 string
		finalizeErr, sendErr error
		endErr               error
		wantFallbackAttempts int
		wantErr              error
	}{
		{name: "successful card"},
		{name: "fallback delivers answer", finalizeErr: cardErr, wantFallbackAttempts: 1},
		{name: "both deliveries fail", finalizeErr: cardErr, sendErr: sendErr, wantFallbackAttempts: 1, wantErr: sendErr},
		{name: "end failure is reported without duplicate answer", endErr: endErr, wantErr: endErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service, adapter, _, order := newFullOutputHarness("complete final answer")
			messages := &deliveryMessageService{}
			service.messageService = messages
			adapter.finalizeErr, adapter.sendErr, adapter.endErr = tt.finalizeErr, tt.sendErr, tt.endErr
			err := service.handleMessageStream(context.Background(),
				&IncomingMessage{Platform: PlatformFeishu, UserID: "test-user", Content: "question"},
				&types.Session{ID: "test-session"}, nil, nil, nil, nil, adapter, adapter, "test-key", nil)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("delivery error = %v, want %v", err, tt.wantErr)
			}
			attempts := 0
			for _, step := range order.snapshot() {
				if step == "plain-reply" {
					attempts++
				}
			}
			if attempts != tt.wantFallbackAttempts {
				t.Fatalf("fallback attempts = %d, want %d", attempts, tt.wantFallbackAttempts)
			}
			if tt.finalizeErr != nil && tt.sendErr == nil && adapter.plainContent != "complete final answer" {
				t.Fatalf("fallback content = %q", adapter.plainContent)
			}
			if messages.saved == nil || !messages.saved.IsCompleted || messages.saved.Content != "complete final answer" {
				t.Fatalf("final answer not persisted despite delivery outcome: %+v", messages.saved)
			}
			if strings.Contains(adapter.finalContent, "思考") {
				t.Fatalf("final display still contains progress: %q", adapter.finalContent)
			}
		})
	}
}
