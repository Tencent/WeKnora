package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type completionRetryMessageService struct {
	interfaces.MessageService

	mu          sync.Mutex
	failures    int
	updateCalls int
	indexed     chan struct{}
}

func (s *completionRetryMessageService) UpdateMessage(ctx context.Context, _ *types.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.updateCalls++
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.updateCalls <= s.failures {
		return errors.New("transient update failure")
	}
	return nil
}

func (s *completionRetryMessageService) IndexMessageToKB(
	_ context.Context,
	_, _, _, _ string,
) {
	select {
	case s.indexed <- struct{}{}:
	default:
	}
}

func (s *completionRetryMessageService) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateCalls
}

func TestCompleteAssistantMessageRetriesBeforeIndexing(t *testing.T) {
	service := &completionRetryMessageService{
		failures: 2,
		indexed:  make(chan struct{}, 1),
	}
	h := &Handler{messageService: service}
	message := &types.Message{
		ID:        "assistant-1",
		SessionID: "session-1",
		Content:   "answer",
	}

	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	h.completeAssistantMessage(requestCtx, message, "question")

	require.Equal(t, completeMessagePersistMaxAttempts, service.calls())
	assert.True(t, message.IsCompleted)
	select {
	case <-service.indexed:
	case <-time.After(time.Second):
		t.Fatal("message was not indexed after completion persisted")
	}
}

func TestCompleteAssistantMessageSkipsDownstreamWorkAfterPermanentFailure(t *testing.T) {
	service := &completionRetryMessageService{
		failures: completeMessagePersistMaxAttempts,
		indexed:  make(chan struct{}, 1),
	}
	h := &Handler{messageService: service}

	h.completeAssistantMessage(context.Background(), &types.Message{
		ID:        "assistant-1",
		SessionID: "session-1",
		Content:   "answer",
	}, "question")

	assert.Equal(t, completeMessagePersistMaxAttempts, service.calls())
	select {
	case <-service.indexed:
		t.Fatal("message must not be indexed when completion was not persisted")
	case <-time.After(150 * time.Millisecond):
	}
}
