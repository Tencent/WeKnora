package im

import (
	"context"
	"errors"
	"reflect"
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
		{name: "expired stream finalize and end both fail", finalizeErr: cardErr, endErr: endErr, wantFallbackAttempts: 1},
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

func TestDeliverIMStreamFinalWeComExpiredStreamFallsBackOnce(t *testing.T) {
	service, adapter, _, order := newFullOutputHarness("final answer")
	adapter.finalizeErr = errors.New("stream expired (errcode 846608)")
	adapter.endErr = errors.New("stream expired (errcode 846608)")

	beforeFinalize := imStreamFinalizeFail.Load()
	beforeEnd := imStreamEndFail.Load()
	beforeOK := imStreamFallbackOK.Load()

	finalizeErr, endErr, fallbackErr := service.deliverIMStreamFinal(
		context.Background(), context.Background(),
		&IncomingMessage{Platform: PlatformWeCom, UserID: "user-1"},
		adapter, adapter, "stream-1", "final answer", "",
	)
	if finalizeErr == nil || endErr == nil {
		t.Fatalf("expected both stream calls to fail, finalize=%v end=%v", finalizeErr, endErr)
	}
	if fallbackErr != nil {
		t.Fatalf("plain-reply fallback error = %v", fallbackErr)
	}
	if !imStreamDelivered(finalizeErr, fallbackErr) {
		t.Fatal("degrade should count as delivered")
	}
	if imStreamDeliveryErr(finalizeErr, endErr, fallbackErr) != nil {
		t.Fatal("successful degrade must not surface a delivery error")
	}
	if adapter.plainReplies != 1 || adapter.plainContent != "final answer" {
		t.Fatalf("plain replies=%d content=%q", adapter.plainReplies, adapter.plainContent)
	}
	if got := order.snapshot(); !reflect.DeepEqual(got, []string{"finalize", "end", "plain-reply"}) {
		t.Fatalf("order = %v", got)
	}
	if imStreamFinalizeFail.Load()-beforeFinalize != 1 {
		t.Fatalf("finalize fail counter delta = %d", imStreamFinalizeFail.Load()-beforeFinalize)
	}
	if imStreamEndFail.Load()-beforeEnd != 1 {
		t.Fatalf("end fail counter delta = %d", imStreamEndFail.Load()-beforeEnd)
	}
	if imStreamFallbackOK.Load()-beforeOK != 1 {
		t.Fatalf("fallback ok counter delta = %d", imStreamFallbackOK.Load()-beforeOK)
	}
}

func TestDeliverIMStreamFinalDoesNotResendAfterSuccessfulFinalize(t *testing.T) {
	service, adapter, _, _ := newFullOutputHarness("final answer")
	adapter.endErr = errors.New("close streaming_mode failed")

	finalizeErr, endErr, fallbackErr := service.deliverIMStreamFinal(
		context.Background(), context.Background(),
		&IncomingMessage{Platform: PlatformFeishu, UserID: "user-1"},
		adapter, adapter, "stream-1", "final answer", "",
	)
	if finalizeErr != nil || fallbackErr != nil {
		t.Fatalf("finalize=%v fallback=%v", finalizeErr, fallbackErr)
	}
	if endErr == nil {
		t.Fatal("expected EndStream error")
	}
	if adapter.plainReplies != 0 {
		t.Fatalf("duplicate plain replies = %d", adapter.plainReplies)
	}
}

func TestDeliverIMStreamFinalTotalFailureIsError(t *testing.T) {
	service, adapter, _, _ := newFullOutputHarness("final answer")
	adapter.finalizeErr = errors.New("stream expired")
	adapter.endErr = errors.New("stream expired")
	adapter.sendErr = errors.New("plain reply rejected")
	beforeFail := imStreamFallbackFail.Load()

	finalizeErr, endErr, fallbackErr := service.deliverIMStreamFinal(
		context.Background(), context.Background(),
		&IncomingMessage{Platform: PlatformWeCom, UserID: "user-1"},
		adapter, adapter, "stream-1", "final answer", "",
	)
	got := imStreamDeliveryErr(finalizeErr, endErr, fallbackErr)
	if !errors.Is(got, adapter.finalizeErr) || !errors.Is(got, adapter.sendErr) {
		t.Fatalf("joined error = %v", got)
	}
	if imStreamFallbackFail.Load()-beforeFail != 1 {
		t.Fatalf("fallback fail counter delta = %d", imStreamFallbackFail.Load()-beforeFail)
	}
}

func TestHandleMessageFullOutputEndStreamFailureDoesNotDuplicate(t *testing.T) {
	service, adapter, _, order := newFullOutputHarness("最终答案")
	adapter.endErr = errors.New("card settings rejected")
	err := service.handleMessageFullOutput(
		context.Background(),
		&IncomingMessage{Platform: PlatformFeishu, UserID: "user-1", Content: "问题"},
		&types.Session{ID: "session-1"}, nil, nil, nil, nil, adapter, adapter, "user-key", nil,
	)
	if err != nil {
		t.Fatalf("full-output should treat a delivered card as success, got %v", err)
	}
	if adapter.plainReplies != 0 {
		t.Fatalf("duplicate plain replies = %d", adapter.plainReplies)
	}
	if got := order.snapshot(); !reflect.DeepEqual(got, []string{"start", "qa", "finalize", "end"}) {
		t.Fatalf("order = %v", got)
	}
}
