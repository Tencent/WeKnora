package modelobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recorderStore struct {
	mu            sync.Mutex
	startErr      error
	completeErr   error
	completeCalls int
	starts        []*types.ModelCallRecord
	completions   []types.ModelCallCompletion
	price         *types.ModelPriceVersion
}

func (s *recorderStore) StartModelCall(_ context.Context, record *types.ModelCallRecord) error {
	if s.startErr != nil {
		return s.startErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := *record
	s.starts = append(s.starts, &cloned)
	return nil
}

func (s *recorderStore) CompleteModelCall(_ context.Context, completion types.ModelCallCompletion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completeCalls++
	if s.completeErr != nil {
		return s.completeErr
	}
	s.completions = append(s.completions, completion)
	return nil
}

func (s *recorderStore) EffectiveModelPrice(
	context.Context,
	uint64,
	string,
	time.Time,
) (*types.ModelPriceVersion, error) {
	return s.price, nil
}

func (s *recorderStore) CreateModelPrice(context.Context, *types.ModelPriceVersion) error { return nil }

func (s *recorderStore) ListModelPrices(context.Context, uint64, string) ([]*types.ModelPriceVersion, error) {
	return nil, nil
}

type recorderChat struct {
	calls  int
	stream <-chan types.StreamResponse
}

func (c *recorderChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	c.calls++
	return &types.ChatResponse{Usage: types.TokenUsage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}}, nil
}

func (c *recorderChat) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	c.calls++
	return c.stream, nil
}
func (*recorderChat) GetModelName() string { return "model" }
func (*recorderChat) GetModelID() string   { return "model-1" }

func TestCalculateCostMicrounitsUsesHalfUpRounding(t *testing.T) {
	cost, err := CalculateCostMicrounits(3, 2, 1_500_000, 2_500_000)
	require.NoError(t, err)
	assert.Equal(t, int64(10), cost)
	cost, err = CalculateCostMicrounits(1, 0, 500_000, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cost)
}

func TestStrictRecorderFailurePreventsProviderCall(t *testing.T) {
	store := &recorderStore{startErr: errors.New("database unavailable")}
	recorder := NewRecorder(store)
	provider := &recorderChat{}
	wrapped := recorder.WrapChat(
		&types.Model{ID: "model-1", TenantID: 7, Name: "safe", Type: types.ModelTypeKnowledgeQA},
		provider,
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = WithPurpose(ctx, PurposeEvaluation, true)

	_, err := wrapped.Chat(ctx, nil, nil)
	require.Error(t, err)
	assert.Zero(t, provider.calls)
}

func TestStrictRecorderCompletionFailureFailsProviderCall(t *testing.T) {
	store := &recorderStore{completeErr: errors.New("database unavailable")}
	provider := &recorderChat{}
	wrapped := NewRecorder(store).WrapChat(
		&types.Model{ID: "model-1", TenantID: 7, Name: "safe", Type: types.ModelTypeKnowledgeQA},
		provider,
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = WithPurpose(ctx, PurposeEvaluation, true)

	response, err := wrapped.Chat(ctx, nil, nil)
	require.ErrorContains(t, err, "model call accounting")
	require.NotNil(t, response)
	assert.Equal(t, 1, provider.calls)
	assert.Equal(t, ledgerFinishAttempts, store.completeCalls)
}

func TestStrictStreamingCompletionFailureIsTerminalStreamError(t *testing.T) {
	stream := make(chan types.StreamResponse)
	close(stream)
	store := &recorderStore{completeErr: errors.New("database unavailable")}
	wrapped := NewRecorder(store).WrapChat(
		&types.Model{ID: "model-1", TenantID: 7, Name: "safe", Type: types.ModelTypeKnowledgeQA},
		&recorderChat{stream: stream},
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = WithPurpose(ctx, PurposeEvaluation, true)

	output, err := wrapped.ChatStream(ctx, nil, nil)
	require.NoError(t, err)
	responses := make([]types.StreamResponse, 0, 1)
	for response := range output {
		responses = append(responses, response)
	}
	require.Len(t, responses, 1)
	assert.Equal(t, types.ResponseTypeError, responses[0].ResponseType)
	assert.Equal(t, "model_call_accounting_failed", responses[0].Data["error_code"])
	assert.Equal(t, ledgerFinishAttempts, store.completeCalls)
}

func TestStrictStreamingCompletionFailureReplacesProviderTerminal(t *testing.T) {
	stream := make(chan types.StreamResponse, 2)
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "partial"}
	stream <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: " answer", Done: true}
	close(stream)
	store := &recorderStore{completeErr: errors.New("database unavailable")}
	wrapped := NewRecorder(store).WrapChat(
		&types.Model{ID: "model-1", TenantID: 7, Name: "safe", Type: types.ModelTypeKnowledgeQA},
		&recorderChat{stream: stream},
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = WithPurpose(ctx, PurposeEvaluation, true)

	output, err := wrapped.ChatStream(ctx, nil, nil)
	require.NoError(t, err)
	responses := make([]types.StreamResponse, 0, 2)
	for response := range output {
		responses = append(responses, response)
	}
	require.Len(t, responses, 2)
	assert.Equal(t, "partial", responses[0].Content)
	assert.False(t, responses[0].Done)
	assert.Equal(t, types.ResponseTypeError, responses[1].ResponseType)
	assert.True(t, responses[1].Done)
	assert.Equal(t, "model_call_accounting_failed", responses[1].Data["error_code"])
	assert.Equal(t, ledgerFinishAttempts, store.completeCalls)
}

func TestStreamingCallCompletesLedgerExactlyOnce(t *testing.T) {
	stream := make(chan types.StreamResponse, 2)
	stream <- types.StreamResponse{
		ResponseType: types.ResponseTypeAnswer,
		Done:         true,
		Usage: &types.TokenUsage{
			PromptTokens:     4,
			CompletionTokens: 1,
			TotalTokens:      5,
		},
	}
	close(stream)
	store := &recorderStore{price: &types.ModelPriceVersion{
		ID: "price-1", InputMicrounitsPerMillion: 1_000_000, OutputMicrounitsPerMillion: 2_000_000,
		Currency: "USD",
	}}
	recorder := NewRecorder(store)
	wrapped := recorder.WrapChat(
		&types.Model{ID: "model-1", TenantID: 7, Name: "safe", Type: types.ModelTypeKnowledgeQA},
		&recorderChat{stream: stream},
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = WithPurpose(ctx, PurposeEvaluation, true)

	output, err := wrapped.ChatStream(ctx, nil, nil)
	require.NoError(t, err)
	for response := range output {
		_ = response
	}
	require.Eventually(t, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.completions) == 1
	}, time.Second, time.Millisecond)
	store.mu.Lock()
	defer store.mu.Unlock()
	require.Len(t, store.starts, 1)
	require.Len(t, store.completions, 1)
	assert.Equal(t, types.ModelCallStatusSuccess, store.completions[0].Status)
	assert.Equal(t, int64(6), *store.completions[0].CostMicrounits)
	assert.True(t, store.completions[0].AccountingComplete)
}
