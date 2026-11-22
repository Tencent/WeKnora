package im

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// fakeReactionSender records reaction calls for tracker tests.
type fakeReactionSender struct {
	mu        sync.Mutex
	calls     []string
	addErr    error
	removeErr error
	nextID    int
}

func (f *fakeReactionSender) AddReaction(_ context.Context, _ *IncomingMessage, emojiType string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "add:"+emojiType)
	if f.addErr != nil {
		return "", f.addErr
	}
	f.nextID++
	return fmt.Sprintf("reaction-%d", f.nextID), nil
}

func (f *fakeReactionSender) RemoveReaction(_ context.Context, _ *IncomingMessage, reactionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "remove:"+reactionID)
	return f.removeErr
}

func (f *fakeReactionSender) recordedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func TestIMReactionTracker_Success(t *testing.T) {
	sender := &fakeReactionSender{}
	tracker := &imReactionTracker{sender: sender, msg: &IncomingMessage{MessageID: "om_123"}}
	ctx := context.Background()
	tracker.Begin(ctx)
	tracker.Finish(ctx, true)
	want := []string{"add:OnIt", "remove:reaction-1", "add:DONE"}
	if got := sender.recordedCalls(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestIMReactionTracker_Failure(t *testing.T) {
	sender := &fakeReactionSender{}
	tracker := &imReactionTracker{sender: sender, msg: &IncomingMessage{MessageID: "om_123"}}
	ctx := context.Background()
	tracker.Begin(ctx)
	tracker.Finish(ctx, false)
	want := []string{"add:OnIt", "remove:reaction-1", "add:ERROR"}
	if got := sender.recordedCalls(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestIMReactionTracker_AddFailureStillFinishes(t *testing.T) {
	sender := &fakeReactionSender{addErr: errors.New("api down")}
	tracker := &imReactionTracker{sender: sender, msg: &IncomingMessage{MessageID: "om_123"}}
	ctx := context.Background()
	// Begin fails: no reaction ID is stored, so Finish must not try to remove
	// anything but must still add the terminal reaction.
	tracker.Begin(ctx)
	tracker.Finish(ctx, true)
	want := []string{"add:OnIt", "add:DONE"}
	if got := sender.recordedCalls(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestIMReactionTracker_RemoveFailureStillAddsTerminal(t *testing.T) {
	sender := &fakeReactionSender{removeErr: errors.New("api down")}
	tracker := &imReactionTracker{sender: sender, msg: &IncomingMessage{MessageID: "om_123"}}
	ctx := context.Background()
	tracker.Begin(ctx)
	tracker.Finish(ctx, false)
	want := []string{"add:OnIt", "remove:reaction-1", "add:ERROR"}
	if got := sender.recordedCalls(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

// stubAdapter implements Adapter without ReactionSender.
type stubAdapter struct {
	Adapter
}

func TestIMReactionTracker_NoSupportIsNoop(t *testing.T) {
	ctx := context.Background()
	// Adapter without ReactionSender must not panic and must not call anything.
	tracker := newIMReactionTracker(&stubAdapter{}, &IncomingMessage{MessageID: "om_123"})
	tracker.Begin(ctx)
	tracker.Finish(ctx, true)
	tracker = newIMReactionTracker(nil, &IncomingMessage{MessageID: "om_123"})
	tracker.Begin(ctx)
	tracker.Finish(ctx, true)

	// Message without an ID must not call anything.
	sender := &fakeReactionSender{}
	tracker = &imReactionTracker{sender: sender, msg: &IncomingMessage{}}
	tracker.Begin(ctx)
	tracker.Finish(ctx, true)
	if got := sender.recordedCalls(); len(got) != 0 {
		t.Errorf("calls = %v, want none", got)
	}

	// Nil tracker must not panic.
	var nilTracker *imReactionTracker
	nilTracker.Begin(ctx)
	nilTracker.Finish(ctx, true)
}
