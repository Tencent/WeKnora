package userinput

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func pendingRequest(bus *event.EventBus) PendingRequest {
	return PendingRequest{
		TenantID:           10000,
		UserID:             "user-1",
		SessionID:          "session-1",
		AssistantMessageID: "message-1",
		RequestID:          "request-1",
		ToolCallID:         "tool-1",
		EventBus:           bus,
		Question:           validQuestion(ModeSingle),
	}
}

func TestGatePersistsPendingQuestionForAnotherInstance(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	owner := &Gate{pending: make(map[string]*waiter), timeout: time.Second, rdb: client}
	reader := &Gate{pending: make(map[string]*waiter), timeout: time.Second, rdb: client}
	bus := event.NewEventBus()
	required := make(chan event.UserInputRequiredData, 1)
	bus.On(event.EventUserInputRequired, func(_ context.Context, evt event.Event) error {
		required <- evt.Data.(event.UserInputRequiredData)
		return nil
	})
	done := make(chan struct{})
	go func() {
		_, _ = owner.RequestAndWait(context.Background(), pendingRequest(bus))
		close(done)
	}()
	eventData := <-required
	snapshot, err := reader.GetPending(context.Background(), 10000, "user-1", "session-1")
	if err != nil || snapshot.PendingID != eventData.PendingID {
		t.Fatalf("GetPending() = %+v, %v", snapshot, err)
	}
	if err := owner.Resolve(10000, "user-1", eventData.PendingID, Answer{Skipped: true}); err != nil {
		t.Fatal(err)
	}
	<-done
	if _, err := reader.GetPending(context.Background(), 10000, "user-1", "session-1"); !errors.Is(err, ErrPendingNotFound) {
		t.Fatalf("GetPending() after resolve error = %v", err)
	}
}

func TestGateRequestAndWaitResolvesAnswer(t *testing.T) {
	bus := event.NewEventBus()
	gate := newGate(time.Second, nil)
	pendingIDs := make(chan string, 1)
	resolved := make(chan event.UserInputResolvedData, 1)
	bus.On(event.EventUserInputRequired, func(_ context.Context, evt event.Event) error {
		data := evt.Data.(event.UserInputRequiredData)
		pendingIDs <- data.PendingID
		return nil
	})
	bus.On(event.EventUserInputResolved, func(_ context.Context, evt event.Event) error {
		resolved <- evt.Data.(event.UserInputResolvedData)
		return nil
	})

	resultCh := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := gate.RequestAndWait(context.Background(), pendingRequest(bus))
		resultCh <- result
		errCh <- err
	}()

	pendingID := <-pendingIDs
	answer := Answer{SelectedOptionIDs: []string{"written"}}
	if err := gate.Resolve(10000, "user-1", pendingID, answer); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("RequestAndWait() error = %v", err)
	}
	result := <-resultCh
	if result.Status != StatusAnswered || len(result.SelectedOptions) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if result.SelectedOptions[0].ID != "written" || result.OtherText != "" {
		t.Fatalf("result answer = %+v", result)
	}
	resolvedData := <-resolved
	if resolvedData.PendingID != pendingID || resolvedData.Status != string(StatusAnswered) {
		t.Fatalf("resolved event = %+v", resolvedData)
	}
}

func TestGateRequestAndWaitResolvesSkip(t *testing.T) {
	bus := event.NewEventBus()
	gate := newGate(time.Second, nil)
	pendingIDs := make(chan string, 1)
	bus.On(event.EventUserInputRequired, func(_ context.Context, evt event.Event) error {
		pendingIDs <- evt.Data.(event.UserInputRequiredData).PendingID
		return nil
	})

	resultCh := make(chan Result, 1)
	go func() {
		result, _ := gate.RequestAndWait(context.Background(), pendingRequest(bus))
		resultCh <- result
	}()
	pendingID := <-pendingIDs
	if err := gate.Resolve(10000, "user-1", pendingID, Answer{Skipped: true}); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result := <-resultCh; result.Status != StatusSkipped {
		t.Fatalf("status = %s", result.Status)
	}
}

func TestGateRequestAndWaitTimesOut(t *testing.T) {
	bus := event.NewEventBus()
	gate := newGate(10*time.Millisecond, nil)
	pendingIDs := make(chan string, 1)
	bus.On(event.EventUserInputRequired, func(_ context.Context, evt event.Event) error {
		pendingIDs <- evt.Data.(event.UserInputRequiredData).PendingID
		return nil
	})
	result, err := gate.RequestAndWait(context.Background(), pendingRequest(bus))
	if err != nil {
		t.Fatalf("RequestAndWait() error = %v", err)
	}
	if result.Status != StatusTimedOut {
		t.Fatalf("status = %s", result.Status)
	}
	if err := gate.Resolve(10000, "user-1", <-pendingIDs, Answer{Skipped: true}); !errors.Is(err, ErrPendingNotFound) {
		t.Fatalf("Resolve() after timeout error = %v", err)
	}
}

func TestGateRequestAndWaitCancelsWithContext(t *testing.T) {
	bus := event.NewEventBus()
	gate := newGate(time.Second, nil)
	ctx, cancel := context.WithCancel(context.Background())
	bus.On(event.EventUserInputRequired, func(_ context.Context, _ event.Event) error {
		cancel()
		return nil
	})
	result, err := gate.RequestAndWait(ctx, pendingRequest(bus))
	if err != nil {
		t.Fatalf("RequestAndWait() error = %v", err)
	}
	if result.Status != StatusCanceled {
		t.Fatalf("status = %s", result.Status)
	}
}

func TestGateResolveEnforcesAuthorizationAndAnswerValidation(t *testing.T) {
	tests := []struct {
		name     string
		tenantID uint64
		userID   string
		answer   Answer
		want     error
	}{
		{name: "tenant mismatch", tenantID: 20000, userID: "user-1", answer: Answer{Skipped: true}, want: ErrTenantMismatch},
		{name: "user mismatch", tenantID: 10000, userID: "user-2", answer: Answer{Skipped: true}, want: ErrUserMismatch},
		{name: "invalid answer", tenantID: 10000, userID: "user-1", answer: Answer{SelectedOptionIDs: []string{"missing"}}, want: ErrInvalidAnswer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := event.NewEventBus()
			gate := newGate(time.Second, nil)
			pendingIDs := make(chan string, 1)
			bus.On(event.EventUserInputRequired, func(_ context.Context, evt event.Event) error {
				pendingIDs <- evt.Data.(event.UserInputRequiredData).PendingID
				return nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				_, _ = gate.RequestAndWait(ctx, pendingRequest(bus))
				close(done)
			}()
			pendingID := <-pendingIDs
			err := gate.Resolve(tt.tenantID, tt.userID, pendingID, tt.answer)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, tt.want)
			}
			cancel()
			<-done
		})
	}
}

func TestGateResolveRejectsCompletedPendingRequest(t *testing.T) {
	bus := event.NewEventBus()
	gate := newGate(time.Second, nil)
	pendingIDs := make(chan string, 1)
	bus.On(event.EventUserInputRequired, func(_ context.Context, evt event.Event) error {
		pendingIDs <- evt.Data.(event.UserInputRequiredData).PendingID
		return nil
	})
	done := make(chan struct{})
	go func() {
		_, _ = gate.RequestAndWait(context.Background(), pendingRequest(bus))
		close(done)
	}()
	pendingID := <-pendingIDs
	if err := gate.Resolve(10000, "user-1", pendingID, Answer{Skipped: true}); err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}
	<-done
	err := gate.Resolve(10000, "user-1", pendingID, Answer{Skipped: true})
	if !errors.Is(err, ErrPendingNotFound) && !errors.Is(err, ErrAlreadyResolved) {
		t.Fatalf("second Resolve() error = %v", err)
	}
}

func TestGateTypedAnswerCarriesProgressAndCanBeRestored(t *testing.T) {
	bus := event.NewEventBus()
	gate := newGate(time.Second, nil)
	required := make(chan event.UserInputRequiredData, 1)
	bus.On(event.EventUserInputRequired, func(_ context.Context, evt event.Event) error {
		required <- evt.Data.(event.UserInputRequiredData)
		return nil
	})
	req := pendingRequest(bus)
	req.Question = collectionQuestion(ModeDate)
	resultCh := make(chan Result, 1)
	go func() {
		result, _ := gate.RequestAndWait(context.Background(), req)
		resultCh <- result
	}()

	eventData := <-required
	if eventData.FieldKey != "dismissal_date" || eventData.RemainingCount != 2 {
		t.Fatalf("required event = %+v", eventData)
	}
	snapshot, err := gate.GetPending(context.Background(), 10000, "user-1", "session-1")
	if err != nil || snapshot.PendingID != eventData.PendingID {
		t.Fatalf("GetPending() = %+v, %v", snapshot, err)
	}
	answer := Answer{FieldKey: "dismissal_date", SchemaVersion: 3, Value: "2026-07-22"}
	if err := gate.Resolve(10000, "user-1", eventData.PendingID, answer); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	result := <-resultCh
	if result.FieldKey != "dismissal_date" || result.Value != "2026-07-22" {
		t.Fatalf("result = %+v", result)
	}
}
