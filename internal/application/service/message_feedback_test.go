package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type feedbackMessageServiceStub struct {
	interfaces.MessageService
	message *types.Message
	err     error
}

func (s *feedbackMessageServiceStub) GetMessage(context.Context, string, string) (*types.Message, error) {
	return s.message, s.err
}

type feedbackRepositoryStub struct {
	interfaces.MessageFeedbackRepository
	synced   bool
	mutation interfaces.MessageFeedbackMutation
}

func (s *feedbackRepositoryStub) SyncMessageChunkReferences(context.Context, uint64, *types.Message) error {
	s.synced = true
	return nil
}

func (s *feedbackRepositoryStub) ApplyFeedback(
	_ context.Context,
	mutation interfaces.MessageFeedbackMutation,
) (*types.MessageFeedbackView, error) {
	s.mutation = mutation
	return &types.MessageFeedbackView{
		Rating:       mutation.Rating,
		ReasonCode:   mutation.ReasonCode,
		ReasonDetail: mutation.ReasonDetail,
	}, nil
}

func TestMessageFeedbackSubmitValidatesAndAttributes(t *testing.T) {
	message := &types.Message{
		ID:          "message-1",
		SessionID:   "session-1",
		Role:        "assistant",
		IsCompleted: true,
		KnowledgeReferences: types.References{{
			ID:              "chunk-1",
			KnowledgeBaseID: "kb-1",
			KnowledgeID:     "knowledge-1",
		}},
	}
	repo := &feedbackRepositoryStub{}
	svc := &messageFeedbackService{
		repo:           repo,
		messageService: &feedbackMessageServiceStub{message: message},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "user-1")

	view, err := svc.Submit(ctx, "session-1", "message-1", -1, "inaccurate", "")

	require.NoError(t, err)
	require.Equal(t, -1, view.Rating)
	require.True(t, repo.synced)
	require.Equal(t, uint64(42), repo.mutation.TenantID)
	require.Equal(t, "user-1", repo.mutation.ActorID)
	require.Equal(t, "inaccurate", repo.mutation.ReasonCode)
}

func TestMessageFeedbackSubmitRejectsInvalidReason(t *testing.T) {
	svc := &messageFeedbackService{
		repo:           &feedbackRepositoryStub{},
		messageService: &feedbackMessageServiceStub{},
	}

	_, err := svc.Submit(context.Background(), "session-1", "message-1", -1, "other", "")

	require.ErrorIs(t, err, ErrInvalidMessageFeedback)
}

func TestMessageFeedbackSubmitRequiresCompletedKnowledgeAnswer(t *testing.T) {
	message := &types.Message{
		ID:          "message-1",
		SessionID:   "session-1",
		Role:        "assistant",
		IsCompleted: true,
	}
	svc := &messageFeedbackService{
		repo:           &feedbackRepositoryStub{},
		messageService: &feedbackMessageServiceStub{message: message},
	}

	_, err := svc.Submit(context.Background(), "session-1", "message-1", 1, "", "")

	require.ErrorIs(t, err, ErrFeedbackNotAvailable)
}
