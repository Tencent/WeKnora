package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type chatArtifactFakeStore struct {
	values       map[types.ProcessingArtifactKey][]byte
	getErr       error
	putErr       error
	putCanonical []byte
	getCalls     int
	putCalls     int
}

func newChatArtifactFakeStore() *chatArtifactFakeStore {
	return &chatArtifactFakeStore{values: make(map[types.ProcessingArtifactKey][]byte)}
}

func (s *chatArtifactFakeStore) Get(
	_ context.Context,
	key types.ProcessingArtifactKey,
) ([]byte, bool, error) {
	s.getCalls++
	if s.getErr != nil {
		return nil, false, s.getErr
	}
	value, ok := s.values[key]
	return append([]byte(nil), value...), ok, nil
}

func (s *chatArtifactFakeStore) PutIfAbsent(
	_ context.Context,
	key types.ProcessingArtifactKey,
	value []byte,
) ([]byte, bool, error) {
	s.putCalls++
	if s.putErr != nil {
		return nil, false, s.putErr
	}
	if s.putCanonical != nil {
		return append([]byte(nil), s.putCanonical...), false, nil
	}
	if canonical, ok := s.values[key]; ok {
		return append([]byte(nil), canonical...), false, nil
	}
	s.values[key] = append([]byte(nil), value...)
	return append([]byte(nil), value...), true, nil
}

type chatArtifactFakeModel struct {
	modelID     string
	modelName   string
	response    *types.ChatResponse
	err         error
	calls       int
	gotMessages []chat.Message
	gotOptions  *chat.ChatOptions
}

func (m *chatArtifactFakeModel) Chat(
	_ context.Context,
	messages []chat.Message,
	opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.calls++
	m.gotMessages = messages
	m.gotOptions = opts
	return m.response, m.err
}

func (m *chatArtifactFakeModel) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *chatArtifactFakeModel) GetModelName() string { return m.modelName }
func (m *chatArtifactFakeModel) GetModelID() string   { return m.modelID }

func TestNewChatArtifactKeyInvalidatesCompleteRequest(t *testing.T) {
	request := testChatArtifactRequest(&chatArtifactFakeModel{modelID: "model-1", modelName: "chat"})

	baseKey, err := newChatArtifactKey(request)
	require.NoError(t, err)
	assert.Equal(t, "chat.summary", baseKey.Stage)
	assert.Equal(t, uint16(1), baseKey.KeyVersion)

	stableKey, err := newChatArtifactKey(request)
	require.NoError(t, err)
	assert.Equal(t, baseKey, stableKey)

	tests := []struct {
		name   string
		mutate func(*chatArtifactRequest)
	}{
		{name: "tenant", mutate: func(r *chatArtifactRequest) { r.tenantID++ }},
		{name: "stage", mutate: func(r *chatArtifactRequest) { r.stage = "chat.questions" }},
		{name: "key version", mutate: func(r *chatArtifactRequest) { r.keyVersion++ }},
		{name: "model ID", mutate: func(r *chatArtifactRequest) {
			r.model = &chatArtifactFakeModel{modelID: "model-2", modelName: "chat"}
		}},
		{name: "model name", mutate: func(r *chatArtifactRequest) {
			r.model = &chatArtifactFakeModel{modelID: "model-1", modelName: "other"}
		}},
		{name: "model revision", mutate: func(r *chatArtifactRequest) { r.modelRevision = "revision-2" }},
		{name: "message ordering", mutate: func(r *chatArtifactRequest) {
			r.messages[0], r.messages[1] = r.messages[1], r.messages[0]
		}},
		{name: "message content", mutate: func(r *chatArtifactRequest) { r.messages[1].Content = "other" }},
		{name: "options", mutate: func(r *chatArtifactRequest) { r.options.Temperature = 0.2 }},
		{name: "prompt version", mutate: func(r *chatArtifactRequest) { r.promptVersion = "summary-v2" }},
		{name: "canonicalizer version", mutate: func(r *chatArtifactRequest) {
			r.canonicalizerVersion = "completion-v2"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := cloneChatArtifactRequest(request)
			tt.mutate(&changed)

			changedKey, err := newChatArtifactKey(changed)
			require.NoError(t, err)
			assert.NotEqual(t, baseKey, changedKey)
		})
	}
}

func TestNewChatArtifactKeyRejectsIncompleteIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*chatArtifactRequest)
	}{
		{name: "empty stage", mutate: func(r *chatArtifactRequest) { r.stage = " " }},
		{name: "zero key version", mutate: func(r *chatArtifactRequest) { r.keyVersion = 0 }},
		{name: "missing model", mutate: func(r *chatArtifactRequest) { r.model = nil }},
		{name: "missing model revision", mutate: func(r *chatArtifactRequest) { r.modelRevision = " " }},
		{name: "missing options", mutate: func(r *chatArtifactRequest) { r.options = nil }},
		{name: "missing prompt version", mutate: func(r *chatArtifactRequest) { r.promptVersion = " " }},
		{name: "missing canonicalizer version", mutate: func(r *chatArtifactRequest) {
			r.canonicalizerVersion = " "
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := cloneChatArtifactRequest(testChatArtifactRequest(&chatArtifactFakeModel{modelID: "model-1"}))
			tt.mutate(&request)
			_, err := newChatArtifactKey(request)
			assert.Error(t, err)
		})
	}
}

func TestChatArtifactModelRevisionIsSecretFreeAndInvalidatesEffectiveConfig(t *testing.T) {
	model := &types.Model{
		ID:        "model-1",
		Name:      "chat-model",
		Type:      types.ModelTypeKnowledgeQA,
		Source:    types.ModelSourceRemote,
		UpdatedAt: time.Date(2026, 7, 18, 8, 30, 0, 0, time.UTC),
		Parameters: types.ModelParameters{
			BaseURL:        "https://API.example.com/v1/",
			APIKey:         "first-api-key",
			AppID:          "first-app-id",
			AppSecret:      "first-app-secret",
			InterfaceType:  "openai",
			ParameterSize:  "70B",
			Provider:       "generic",
			SupportsVision: true,
			ExtraConfig: map[string]string{
				"api_version":       "2026-01-01",
				"remote_model_name": "deployed-chat",
				"thinking_control":  "enabled",
			},
		},
	}

	base, err := chatArtifactModelRevision(model)
	require.NoError(t, err)

	credentialsChanged := *model
	credentialsChanged.Parameters = model.Parameters
	credentialsChanged.Parameters.APIKey = "second-api-key"
	credentialsChanged.Parameters.AppID = "second-app-id"
	credentialsChanged.Parameters.AppSecret = "second-app-secret"
	credentialsRevision, err := chatArtifactModelRevision(&credentialsChanged)
	require.NoError(t, err)
	assert.Equal(t, base, credentialsRevision)

	endpointNormalized := *model
	endpointNormalized.Parameters = model.Parameters
	endpointNormalized.Parameters.BaseURL = "https://api.example.com/v1"
	normalizedRevision, err := chatArtifactModelRevision(&endpointNormalized)
	require.NoError(t, err)
	assert.Equal(t, base, normalizedRevision)

	for _, tt := range []struct {
		name   string
		mutate func(*types.Model)
	}{
		{name: "model name", mutate: func(m *types.Model) { m.Name = "other" }},
		{name: "provider", mutate: func(m *types.Model) { m.Parameters.Provider = "anthropic" }},
		{name: "endpoint", mutate: func(m *types.Model) { m.Parameters.BaseURL = "https://api.example.com/v2" }},
		{name: "remote model", mutate: func(m *types.Model) { m.Parameters.ExtraConfig["remote_model_name"] = "other" }},
		{name: "updated at", mutate: func(m *types.Model) { m.UpdatedAt = m.UpdatedAt.Add(time.Second) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			changed := cloneChatArtifactModel(model)
			tt.mutate(changed)
			revision, err := chatArtifactModelRevision(changed)
			require.NoError(t, err)
			assert.NotEqual(t, base, revision)
		})
	}
}

func TestChatArtifactModelRevisionRejectsUnsafeConfigurationWithoutLeakingSecrets(t *testing.T) {
	const secret = "very-secret-password"
	model := &types.Model{
		ID:   "model-1",
		Name: "chat-model",
		Parameters: types.ModelParameters{
			BaseURL:       "https://user:" + secret + "@api.example.com/v1?token=private",
			CustomHeaders: map[string]string{"X-Gateway-Token": secret},
		},
	}

	_, err := chatArtifactModelRevision(model)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
	assert.NotContains(t, err.Error(), "token=private")
}

func TestChatArtifactCompletionCodecRoundTripAndRejectsMalformedPayload(t *testing.T) {
	encoded, err := encodeChatArtifactCompletion("completed text")
	require.NoError(t, err)
	decoded, err := decodeChatArtifactCompletion(encoded)
	require.NoError(t, err)
	assert.Equal(t, "completed text", decoded)

	for _, payload := range [][]byte{nil, {99}, {chatArtifactCodecVersion, 0xff}} {
		_, err := decodeChatArtifactCompletion(payload)
		assert.ErrorIs(t, err, errChatArtifactCodec)
	}
}

func TestCompleteChatArtifactWithoutStoreCallsProviderUnchanged(t *testing.T) {
	model := &chatArtifactFakeModel{
		modelID:  "model-1",
		response: &types.ChatResponse{Content: "fresh completion"},
	}
	request := testChatArtifactRequest(model)

	completion, hit, providerCalled, err := completeChatArtifact(context.Background(), nil, request)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.True(t, providerCalled)
	assert.Equal(t, "fresh completion", completion)
	assert.Equal(t, 1, model.calls)
	assert.Same(t, request.options, model.gotOptions)
	assert.True(t, reflect.DeepEqual(request.messages, model.gotMessages))
}

func TestCompleteChatArtifactHitSkipsProvider(t *testing.T) {
	store := newChatArtifactFakeStore()
	model := &chatArtifactFakeModel{modelID: "model-1", response: &types.ChatResponse{Content: "fresh"}}
	request := testChatArtifactRequest(model)
	key, err := newChatArtifactKey(request)
	require.NoError(t, err)
	store.values[key], err = encodeChatArtifactCompletion("cached")
	require.NoError(t, err)

	completion, hit, providerCalled, err := completeChatArtifact(context.Background(), store, request)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.False(t, providerCalled)
	assert.Equal(t, "cached", completion)
	assert.Zero(t, model.calls)
	assert.Equal(t, 1, store.getCalls)
	assert.Zero(t, store.putCalls)
}

func TestCompleteChatArtifactMissReturnsPutIfAbsentWinner(t *testing.T) {
	store := newChatArtifactFakeStore()
	winner, err := encodeChatArtifactCompletion("canonical winner")
	require.NoError(t, err)
	store.putCanonical = winner
	model := &chatArtifactFakeModel{modelID: "model-1", response: &types.ChatResponse{Content: "losing completion"}}

	completion, hit, providerCalled, err := completeChatArtifact(context.Background(), store, testChatArtifactRequest(model))
	require.NoError(t, err)
	assert.False(t, hit)
	assert.True(t, providerCalled)
	assert.Equal(t, "canonical winner", completion)
	assert.Equal(t, 1, model.calls)
	assert.Equal(t, 1, store.getCalls)
	assert.Equal(t, 1, store.putCalls)
}

func TestCompleteChatArtifactMalformedHitRecomputes(t *testing.T) {
	store := newChatArtifactFakeStore()
	model := &chatArtifactFakeModel{modelID: "model-1", response: &types.ChatResponse{Content: "fresh completion"}}
	request := testChatArtifactRequest(model)
	key, err := newChatArtifactKey(request)
	require.NoError(t, err)
	store.values[key] = []byte{99}
	store.putCanonical, err = encodeChatArtifactCompletion("canonical repair")
	require.NoError(t, err)

	completion, hit, providerCalled, err := completeChatArtifact(context.Background(), store, request)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.True(t, providerCalled)
	assert.Equal(t, "canonical repair", completion)
	assert.Equal(t, 1, model.calls)
	assert.Equal(t, 1, store.putCalls)
}

func TestCompleteChatArtifactWrapsProviderAndStoreErrorsDistinctly(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	model := &chatArtifactFakeModel{modelID: "model-1", err: providerErr}
	_, _, providerCalled, err := completeChatArtifact(context.Background(), nil, testChatArtifactRequest(model))
	assert.ErrorIs(t, err, errChatArtifactProvider)
	assert.ErrorIs(t, err, providerErr)
	assert.True(t, providerCalled)

	t.Run("get", func(t *testing.T) {
		storeErr := errors.New("get failed")
		store := newChatArtifactFakeStore()
		store.getErr = storeErr
		model := &chatArtifactFakeModel{modelID: "model-1", response: &types.ChatResponse{Content: "fresh"}}

		_, _, providerCalled, err := completeChatArtifact(context.Background(), store, testChatArtifactRequest(model))
		assert.ErrorIs(t, err, errChatArtifactStore)
		assert.ErrorIs(t, err, storeErr)
		assert.False(t, providerCalled)
		assert.Zero(t, model.calls)
	})

	t.Run("put", func(t *testing.T) {
		storeErr := errors.New("put failed")
		store := newChatArtifactFakeStore()
		store.putErr = storeErr
		model := &chatArtifactFakeModel{modelID: "model-1", response: &types.ChatResponse{Content: "fresh"}}

		_, _, providerCalled, err := completeChatArtifact(context.Background(), store, testChatArtifactRequest(model))
		assert.ErrorIs(t, err, errChatArtifactStore)
		assert.ErrorIs(t, err, storeErr)
		assert.True(t, providerCalled)
		assert.Equal(t, 1, model.calls)
	})
}

func TestIsChatArtifactPipelineError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "store", err: fmt.Errorf("wrapped: %w", errChatArtifactStore), want: true},
		{name: "codec", err: fmt.Errorf("wrapped: %w", errChatArtifactCodec), want: true},
		{name: "provider", err: fmt.Errorf("wrapped: %w", errChatArtifactProvider), want: false},
		{name: "other", err: errors.New("other"), want: false},
		{name: "nil", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isChatArtifactPipelineError(tt.err))
		})
	}
}

func testChatArtifactRequest(model chat.Chat) chatArtifactRequest {
	thinking := true
	parallel := false
	return chatArtifactRequest{
		tenantID:      7,
		stage:         "chat.summary",
		keyVersion:    1,
		model:         model,
		modelRevision: "model-revision-1",
		messages: []chat.Message{
			{Role: "system", Content: "summarize"},
			{Role: "user", Content: "document"},
		},
		options: &chat.ChatOptions{
			Temperature:         0.1,
			TopP:                0.9,
			Seed:                3,
			MaxTokens:           100,
			MaxCompletionTokens: 80,
			FrequencyPenalty:    0.2,
			PresencePenalty:     0.3,
			Thinking:            &thinking,
			Tools: []chat.Tool{{
				Type: "function",
				Function: chat.FunctionDef{
					Name:       "lookup",
					Parameters: json.RawMessage(`{"type":"object"}`),
				},
			}},
			ToolChoice:        "auto",
			ParallelToolCalls: &parallel,
			Format:            json.RawMessage(`{"type":"json_object"}`),
		},
		promptVersion:        "summary-v1",
		canonicalizerVersion: "completion-v1",
	}
}

func cloneChatArtifactRequest(request chatArtifactRequest) chatArtifactRequest {
	cloned := request
	cloned.messages = append([]chat.Message(nil), request.messages...)
	if request.options != nil {
		options := *request.options
		cloned.options = &options
	}
	return cloned
}

func cloneChatArtifactModel(model *types.Model) *types.Model {
	cloned := *model
	cloned.Parameters = model.Parameters
	cloned.Parameters.ExtraConfig = make(map[string]string, len(model.Parameters.ExtraConfig))
	for key, value := range model.Parameters.ExtraConfig {
		cloned.Parameters.ExtraConfig[key] = value
	}
	return &cloned
}
