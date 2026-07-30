package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestUpdateCompletedAssistantMessagePersistsAllTrackableChunkRefs(t *testing.T) {
	messageRepo := &completionMessageRepo{}
	sessionRepo := &completionSessionRepo{}
	chunkRepo := &completionChunkRepo{
		chunks: map[string]*types.Chunk{
			"main-chunk": {ID: "main-chunk", TenantID: 41},
			"sub-chunk":  {ID: "sub-chunk", TenantID: 42},
		},
	}
	qaRefRepo := newCompletionQARefRepo()
	uow := &completionFeedbackUOW{
		repos: interfaces.ChunkFeedbackRepositories{
			QARefRepo:    qaRefRepo,
			MessageRepo:  messageRepo,
			ChunkRepo:    chunkRepo,
			FeedbackRepo: nil,
		},
	}
	feedbackSvc := NewChunkFeedbackServiceWithUnitOfWork(
		qaRefRepo,
		nil,
		messageRepo,
		chunkRepo,
		nil,
		uow,
	)
	messageSvc := NewMessageService(
		messageRepo,
		sessionRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		feedbackSvc,
	)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(99))
	ctx = context.WithValue(ctx, types.SessionTenantIDContextKey, uint64(7))
	message := &types.Message{
		ID:          "assistant-message",
		SessionID:   "session-1",
		Role:        "assistant",
		IsCompleted: true,
		KnowledgeReferences: types.References{
			{
				ID:         "main-chunk",
				SubChunkID: []string{"sub-chunk", "sub-chunk"},
			},
			{ID: "main-chunk"},
			{ID: "web-result", MatchType: types.MatchTypeWebSearch},
			{ID: "history-result", MatchType: types.MatchTypeHistory},
		},
	}

	require.NoError(t, messageSvc.UpdateMessage(ctx, message))
	require.Equal(t, uint64(7), sessionRepo.tenantID)
	require.Equal(t, 1, uow.calls)
	require.Equal(t, []string{"main-chunk", "sub-chunk"}, chunkRepo.requestedIDs)
	require.Equal(t, []string{"update:assistant-message", "refs:assistant-message"}, uow.operations)
	require.Equal(t, map[string]completionRefValue{
		"assistant-message:main-chunk:7:41": {
			messageID:     "assistant-message",
			chunkID:       "main-chunk",
			tenantID:      7,
			chunkTenantID: 41,
		},
		"assistant-message:sub-chunk:7:42": {
			messageID:     "assistant-message",
			chunkID:       "sub-chunk",
			tenantID:      7,
			chunkTenantID: 42,
		},
	}, qaRefRepo.refs)

	// Completion can be retried by the web or IM transport. The repository
	// contract is idempotent, so retrying the common completion hook must not
	// duplicate the logical associations.
	require.NoError(t, messageSvc.UpdateMessage(ctx, message))
	require.Len(t, qaRefRepo.refs, 2)
	require.Equal(t, 2, uow.calls)
}

func TestUpdateMessageOnlyUsesCompletionTransactionForCompletedAssistant(t *testing.T) {
	tests := []struct {
		name    string
		message *types.Message
	}{
		{
			name: "incomplete assistant",
			message: &types.Message{
				ID:          "assistant-message",
				SessionID:   "session-1",
				Role:        "assistant",
				IsCompleted: false,
				KnowledgeReferences: types.References{
					{ID: "main-chunk"},
				},
			},
		},
		{
			name: "completed user",
			message: &types.Message{
				ID:          "user-message",
				SessionID:   "session-1",
				Role:        "user",
				IsCompleted: true,
				KnowledgeReferences: types.References{
					{ID: "main-chunk"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messageRepo := &completionMessageRepo{}
			sessionRepo := &completionSessionRepo{}
			uow := &completionFeedbackUOW{}
			feedbackSvc := NewChunkFeedbackServiceWithUnitOfWork(
				newCompletionQARefRepo(),
				nil,
				messageRepo,
				&completionChunkRepo{},
				nil,
				uow,
			)
			messageSvc := NewMessageService(
				messageRepo,
				sessionRepo,
				nil,
				nil,
				nil,
				nil,
				nil,
				feedbackSvc,
			)
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

			require.NoError(t, messageSvc.UpdateMessage(ctx, tt.message))
			require.Equal(t, 0, uow.calls)
			require.Equal(t, []string{tt.message.ID}, messageRepo.updatedMessageIDs)
		})
	}
}

func TestPersistCompletedReplyDoesNotWriteRefsWhenMessageUpdateFails(t *testing.T) {
	messageErr := errors.New("message update failed")
	messageRepo := &completionMessageRepo{updateErr: messageErr}
	qaRefRepo := newCompletionQARefRepo()
	chunkRepo := &completionChunkRepo{
		chunks: map[string]*types.Chunk{
			"main-chunk": {ID: "main-chunk", TenantID: 7},
		},
	}
	uow := &completionFeedbackUOW{
		repos: interfaces.ChunkFeedbackRepositories{
			QARefRepo:   qaRefRepo,
			MessageRepo: messageRepo,
			ChunkRepo:   chunkRepo,
		},
	}
	feedbackSvc := NewChunkFeedbackServiceWithUnitOfWork(
		qaRefRepo,
		nil,
		messageRepo,
		chunkRepo,
		nil,
		uow,
	)
	messageSvc := NewMessageService(
		messageRepo,
		&completionSessionRepo{},
		nil,
		nil,
		nil,
		nil,
		nil,
		feedbackSvc,
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	err := messageSvc.UpdateMessage(ctx, &types.Message{
		ID:                  "assistant-message",
		SessionID:           "session-1",
		Role:                "assistant",
		IsCompleted:         true,
		KnowledgeReferences: types.References{{ID: "main-chunk"}},
	})

	require.ErrorIs(t, err, messageErr)
	require.Empty(t, qaRefRepo.refs)
	require.Empty(t, chunkRepo.requestedIDs)
	require.Equal(t, []string{"update:assistant-message"}, uow.operations)
}

func TestPersistCompletedReplySkipsDeletedChunksWithoutDroppingExistingRefs(t *testing.T) {
	messageRepo := &completionMessageRepo{}
	qaRefRepo := newCompletionQARefRepo()
	chunkRepo := &completionChunkRepo{
		chunks: map[string]*types.Chunk{
			"existing-chunk": {ID: "existing-chunk", TenantID: 41},
		},
	}
	uow := &completionFeedbackUOW{
		repos: interfaces.ChunkFeedbackRepositories{
			QARefRepo:   qaRefRepo,
			MessageRepo: messageRepo,
			ChunkRepo:   chunkRepo,
		},
	}
	feedbackSvc := NewChunkFeedbackServiceWithUnitOfWork(
		qaRefRepo,
		nil,
		messageRepo,
		chunkRepo,
		nil,
		uow,
	)

	err := feedbackSvc.PersistCompletedReply(context.Background(), 7, &types.Message{
		ID:        "assistant-message",
		SessionID: "session-1",
		Role:      "assistant",
		KnowledgeReferences: types.References{{
			ID:         "existing-chunk",
			SubChunkID: []string{"deleted-sub-chunk"},
		}},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"existing-chunk", "deleted-sub-chunk"}, chunkRepo.requestedIDs)
	require.Equal(t, map[string]completionRefValue{
		"assistant-message:existing-chunk:7:41": {
			messageID:     "assistant-message",
			chunkID:       "existing-chunk",
			tenantID:      7,
			chunkTenantID: 41,
		},
	}, qaRefRepo.refs)
}

func TestPersistCompletedReplyReturnsChunkTenantLookupFailure(t *testing.T) {
	lookupErr := errors.New("chunk lookup failed")
	messageRepo := &completionMessageRepo{}
	qaRefRepo := newCompletionQARefRepo()
	chunkRepo := &completionChunkRepo{listErr: lookupErr}
	uow := &completionFeedbackUOW{
		repos: interfaces.ChunkFeedbackRepositories{
			QARefRepo:   qaRefRepo,
			MessageRepo: messageRepo,
			ChunkRepo:   chunkRepo,
		},
	}
	feedbackSvc := NewChunkFeedbackServiceWithUnitOfWork(
		qaRefRepo,
		nil,
		messageRepo,
		chunkRepo,
		nil,
		uow,
	)

	err := feedbackSvc.PersistCompletedReply(context.Background(), 7, &types.Message{
		ID:                  "assistant-message",
		SessionID:           "session-1",
		Role:                "assistant",
		KnowledgeReferences: types.References{{ID: "shared-chunk"}},
	})

	require.ErrorIs(t, err, lookupErr)
	require.Empty(t, qaRefRepo.refs)
}

type completionMessageRepo struct {
	interfaces.MessageRepository
	updateErr         error
	updatedMessageIDs []string
	operations        *[]string
}

func (r *completionMessageRepo) UpdateMessage(_ context.Context, message *types.Message) error {
	r.updatedMessageIDs = append(r.updatedMessageIDs, message.ID)
	if r.operations != nil {
		*r.operations = append(*r.operations, "update:"+message.ID)
	}
	return r.updateErr
}

type completionSessionRepo struct {
	interfaces.SessionRepository
	tenantID uint64
}

func (r *completionSessionRepo) Get(
	_ context.Context,
	tenantID uint64,
	_ string,
	id string,
) (*types.Session, error) {
	r.tenantID = tenantID
	return &types.Session{ID: id, TenantID: tenantID}, nil
}

type completionChunkRepo struct {
	interfaces.ChunkRepository
	chunks       map[string]*types.Chunk
	requestedIDs []string
	listErr      error
}

func (r *completionChunkRepo) ListChunksByIDOnly(
	_ context.Context,
	ids []string,
) ([]*types.Chunk, error) {
	r.requestedIDs = append([]string(nil), ids...)
	if r.listErr != nil {
		return nil, r.listErr
	}
	result := make([]*types.Chunk, 0, len(ids))
	for _, id := range ids {
		if chunk := r.chunks[id]; chunk != nil {
			copy := *chunk
			result = append(result, &copy)
		}
	}
	return result, nil
}

type completionRefValue struct {
	messageID     string
	chunkID       string
	tenantID      uint64
	chunkTenantID uint64
}

type completionQARefRepo struct {
	interfaces.QAReplyChunkRefRepository
	refs       map[string]completionRefValue
	operations *[]string
}

func newCompletionQARefRepo() *completionQARefRepo {
	return &completionQARefRepo{refs: make(map[string]completionRefValue)}
}

func (r *completionQARefRepo) CreateBatch(_ context.Context, refs []*types.QAReplyChunkRef) error {
	for _, ref := range refs {
		key := fmt.Sprintf("%s:%s:%d:%d", ref.MessageID, ref.ChunkID, ref.TenantID, ref.ChunkTenantID)
		r.refs[key] = completionRefValue{
			messageID:     ref.MessageID,
			chunkID:       ref.ChunkID,
			tenantID:      ref.TenantID,
			chunkTenantID: ref.ChunkTenantID,
		}
	}
	if r.operations != nil && len(refs) > 0 {
		*r.operations = append(*r.operations, "refs:"+refs[0].MessageID)
	}
	return nil
}

type completionFeedbackUOW struct {
	repos      interfaces.ChunkFeedbackRepositories
	calls      int
	operations []string
}

func (u *completionFeedbackUOW) Do(
	ctx context.Context,
	fn func(context.Context, interfaces.ChunkFeedbackRepositories) error,
) error {
	u.calls++
	u.repos.MessageRepo.(*completionMessageRepo).operations = &u.operations
	u.repos.QARefRepo.(*completionQARefRepo).operations = &u.operations
	return fn(ctx, u.repos)
}
