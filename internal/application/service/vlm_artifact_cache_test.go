package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type vlmArtifactFakeModel struct {
	modelID   string
	modelName string
	result    string
	err       error
	calls     int
}

func (m *vlmArtifactFakeModel) Predict(context.Context, [][]byte, string) (string, error) {
	m.calls++
	return m.result, m.err
}

func (m *vlmArtifactFakeModel) GetModelName() string { return m.modelName }
func (m *vlmArtifactFakeModel) GetModelID() string   { return m.modelID }

type vlmArtifactFakeStore struct {
	values       map[types.ProcessingArtifactKey][]byte
	getErr       error
	putErr       error
	putCanonical []byte
	hasCanonical bool
	getCalls     int
	putCalls     int
}

func newVLMArtifactFakeStore() *vlmArtifactFakeStore {
	return &vlmArtifactFakeStore{values: make(map[types.ProcessingArtifactKey][]byte)}
}

func (s *vlmArtifactFakeStore) Get(
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

func (s *vlmArtifactFakeStore) PutIfAbsent(
	_ context.Context,
	key types.ProcessingArtifactKey,
	value []byte,
) ([]byte, bool, error) {
	s.putCalls++
	if s.putErr != nil {
		return nil, false, s.putErr
	}
	if s.hasCanonical {
		return append([]byte(nil), s.putCanonical...), false, nil
	}
	if canonical, ok := s.values[key]; ok {
		return append([]byte(nil), canonical...), false, nil
	}
	s.values[key] = append([]byte(nil), value...)
	return append([]byte(nil), value...), true, nil
}

func TestNewVLMArtifactKeyStabilityAndInvalidation(t *testing.T) {
	base := vlmArtifactRequest{
		tenantID:             7,
		imageBytes:           []byte("image"),
		model:                &vlmArtifactFakeModel{modelID: "model-1", modelName: "vision"},
		prompt:               "read the image",
		promptVersion:        "ocr-default-v1",
		resultKind:           vlmArtifactOCR,
		canonicalizerVersion: "ocr-sanitizer-v1",
		canonicalize:         strings.TrimSpace,
	}

	baseKey, err := newVLMArtifactKey(base)
	require.NoError(t, err)
	stableKey, err := newVLMArtifactKey(base)
	require.NoError(t, err)
	assert.Equal(t, baseKey, stableKey)
	assert.Equal(t, "vlm.ocr", baseKey.Stage)
	assert.Equal(t, uint16(1), baseKey.KeyVersion)

	tests := []struct {
		name   string
		mutate func(*vlmArtifactRequest)
	}{
		{name: "image bytes", mutate: func(r *vlmArtifactRequest) { r.imageBytes = []byte("other-image") }},
		{name: "model identity", mutate: func(r *vlmArtifactRequest) {
			r.model = &vlmArtifactFakeModel{modelID: "model-2", modelName: "vision"}
		}},
		{name: "model revision", mutate: func(r *vlmArtifactRequest) { r.modelRevision = "2026-07-16T10:00:00Z" }},
		{name: "prompt content", mutate: func(r *vlmArtifactRequest) { r.prompt = "describe the image" }},
		{name: "prompt version", mutate: func(r *vlmArtifactRequest) { r.promptVersion = "ocr-default-v2" }},
		{name: "result kind", mutate: func(r *vlmArtifactRequest) { r.resultKind = vlmArtifactCaption }},
		{name: "canonicalizer version", mutate: func(r *vlmArtifactRequest) { r.canonicalizerVersion = "ocr-sanitizer-v2" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := base
			tt.mutate(&changed)
			changedKey, err := newVLMArtifactKey(changed)
			require.NoError(t, err)
			assert.NotEqual(t, baseKey, changedKey)
		})
	}
}

func TestNewVLMArtifactKeyLegacyIdentityExcludesCredentials(t *testing.T) {
	request := vlmArtifactRequest{
		tenantID:   7,
		imageBytes: []byte("image"),
		model:      &vlmArtifactFakeModel{modelName: "legacy-vision"},
		config: types.VLMConfig{
			ModelName:     "legacy-vision",
			BaseURL:       "https://user:password@example.com/v1?api_key=secret#fragment",
			APIKey:        "first-secret",
			InterfaceType: "openai",
		},
		prompt:               "read the image",
		promptVersion:        "ocr-default-v1",
		resultKind:           vlmArtifactOCR,
		canonicalizerVersion: "ocr-sanitizer-v1",
		canonicalize:         strings.TrimSpace,
	}

	baseKey, err := newVLMArtifactKey(request)
	require.NoError(t, err)

	credentialsChanged := request
	credentialsChanged.config.APIKey = "second-secret"
	credentialsChanged.config.BaseURL = "https://other:credentials@example.com/v1?token=other#private"
	credentialsKey, err := newVLMArtifactKey(credentialsChanged)
	require.NoError(t, err)
	assert.Equal(t, baseKey, credentialsKey)

	endpointChanged := request
	endpointChanged.config.BaseURL = "https://example.com/v2"
	endpointKey, err := newVLMArtifactKey(endpointChanged)
	require.NoError(t, err)
	assert.NotEqual(t, baseKey, endpointKey)

	for _, tt := range []struct {
		name   string
		mutate func(*vlmArtifactRequest)
	}{
		{name: "interface type", mutate: func(r *vlmArtifactRequest) { r.config.InterfaceType = "ollama" }},
		{name: "model name", mutate: func(r *vlmArtifactRequest) {
			r.model = &vlmArtifactFakeModel{modelName: "other-vision"}
		}},
		{name: "endpoint host", mutate: func(r *vlmArtifactRequest) {
			r.config.BaseURL = "https://other.example.com/v1"
		}},
		{name: "endpoint scheme", mutate: func(r *vlmArtifactRequest) {
			r.config.BaseURL = "http://example.com/v1"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			changed := request
			tt.mutate(&changed)
			changedKey, err := newVLMArtifactKey(changed)
			require.NoError(t, err)
			assert.NotEqual(t, baseKey, changedKey)
		})
	}
}

func TestNewVLMArtifactKeyRejectsIncompleteCanonicalizationIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*vlmArtifactRequest)
	}{
		{name: "empty prompt version", mutate: func(r *vlmArtifactRequest) { r.promptVersion = " " }},
		{name: "empty canonicalizer version", mutate: func(r *vlmArtifactRequest) { r.canonicalizerVersion = " " }},
		{name: "missing canonicalizer", mutate: func(r *vlmArtifactRequest) { r.canonicalize = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := testVLMArtifactRequest(&vlmArtifactFakeModel{modelID: "model-1"})
			tt.mutate(&request)
			_, err := newVLMArtifactKey(request)
			assert.Error(t, err)
		})
	}
}

func TestNewVLMArtifactKeyDoesNotExposeInvalidLegacyURL(t *testing.T) {
	const secret = "very-secret-password"
	request := testVLMArtifactRequest(&vlmArtifactFakeModel{modelName: "legacy-vision"})
	request.config.BaseURL = "https://user:" + secret + "%zz@example.com/v1?token=private"

	_, err := newVLMArtifactKey(request)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
	assert.NotContains(t, err.Error(), "token=private")
}

func TestPredictVLMArtifactCachesCanonicalResult(t *testing.T) {
	store := newVLMArtifactFakeStore()
	model := &vlmArtifactFakeModel{modelID: "model-1", modelName: "vision", result: "  generated text  "}
	request := testVLMArtifactRequest(model)
	request.canonicalize = strings.TrimSpace

	first, hit, err := predictVLMArtifact(context.Background(), store, request)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, "generated text", first)

	second, hit, err := predictVLMArtifact(context.Background(), store, request)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, model.calls)
	assert.Equal(t, 1, store.putCalls)
}

func TestPredictVLMArtifactWithoutStoreUsesProviderDirectly(t *testing.T) {
	model := &vlmArtifactFakeModel{modelID: "model-1", result: "  generated text  "}
	request := testVLMArtifactRequest(model)

	got, hit, err := predictVLMArtifact(context.Background(), nil, request)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, "generated text", got)
	assert.Equal(t, 1, model.calls)
}

func TestPredictVLMArtifactCachesSuccessfulEmptyResult(t *testing.T) {
	store := newVLMArtifactFakeStore()
	model := &vlmArtifactFakeModel{modelID: "model-1", result: "no text"}
	request := testVLMArtifactRequest(model)
	request.canonicalize = func(string) string { return "" }

	first, hit, err := predictVLMArtifact(context.Background(), store, request)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Empty(t, first)

	second, hit, err := predictVLMArtifact(context.Background(), store, request)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Empty(t, second)
	assert.Equal(t, 1, model.calls)
	assert.Equal(t, 1, store.putCalls)
}

func TestPredictVLMArtifactDoesNotCacheProviderError(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	store := newVLMArtifactFakeStore()
	model := &vlmArtifactFakeModel{modelID: "model-1", err: providerErr}

	_, _, err := predictVLMArtifact(context.Background(), store, testVLMArtifactRequest(model))
	assert.ErrorIs(t, err, errVLMArtifactProvider)
	assert.ErrorContains(t, err, providerErr.Error())
	assert.Equal(t, 1, model.calls)
	assert.Zero(t, store.putCalls)
}

func TestPredictVLMArtifactPropagatesStoreErrors(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		storeErr := errors.New("get failed")
		store := newVLMArtifactFakeStore()
		store.getErr = storeErr
		model := &vlmArtifactFakeModel{modelID: "model-1", result: "text"}

		_, _, err := predictVLMArtifact(context.Background(), store, testVLMArtifactRequest(model))
		assert.ErrorIs(t, err, errVLMArtifactStore)
		assert.ErrorContains(t, err, storeErr.Error())
		assert.Zero(t, model.calls)
		assert.Zero(t, store.putCalls)
	})

	t.Run("put", func(t *testing.T) {
		storeErr := errors.New("put failed")
		store := newVLMArtifactFakeStore()
		store.putErr = storeErr
		model := &vlmArtifactFakeModel{modelID: "model-1", result: "text"}

		_, _, err := predictVLMArtifact(context.Background(), store, testVLMArtifactRequest(model))
		assert.ErrorIs(t, err, errVLMArtifactStore)
		assert.ErrorContains(t, err, storeErr.Error())
		assert.Equal(t, 1, model.calls)
		assert.Equal(t, 1, store.putCalls)
	})
}

func TestPredictVLMArtifactUsesPutIfAbsentWinner(t *testing.T) {
	store := newVLMArtifactFakeStore()
	store.putCanonical = []byte("canonical winner")
	store.hasCanonical = true
	model := &vlmArtifactFakeModel{modelID: "model-1", result: "losing result"}

	got, hit, err := predictVLMArtifact(context.Background(), store, testVLMArtifactRequest(model))
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, "canonical winner", got)
	assert.Equal(t, 1, model.calls)
	assert.Equal(t, 1, store.putCalls)
}

func TestPredictVLMArtifactUsesEmptyPutIfAbsentWinner(t *testing.T) {
	store := newVLMArtifactFakeStore()
	store.hasCanonical = true
	model := &vlmArtifactFakeModel{modelID: "model-1", result: "losing result"}

	got, hit, err := predictVLMArtifact(context.Background(), store, testVLMArtifactRequest(model))
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Empty(t, got)
}

func testVLMArtifactRequest(model vlm.VLM) vlmArtifactRequest {
	return vlmArtifactRequest{
		tenantID:             7,
		imageBytes:           []byte("image"),
		model:                model,
		prompt:               "read the image",
		promptVersion:        "ocr-default-v1",
		resultKind:           vlmArtifactOCR,
		canonicalizerVersion: "ocr-sanitizer-v1",
		canonicalize:         strings.TrimSpace,
	}
}
