package chat

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type privateAssessmentModel struct{}

func (privateAssessmentModel) ChatStream(
	context.Context,
	[]Message,
	*ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("unexpected streaming assessment")
}

func (privateAssessmentModel) GetModelName() string { return "assessment-model" }
func (privateAssessmentModel) GetModelID() string   { return "assessment-id" }

func (privateAssessmentModel) Chat(
	context.Context,
	[]Message,
	*ChatOptions,
) (*types.ChatResponse, error) {
	return &types.ChatResponse{
		Content: "answer-key-private",
		Usage:   types.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}, errors.New("provider-error-private")
}

func TestLearningModelContentRedactedInOTLPAndRequestLogs(t *testing.T) {
	var mu sync.Mutex
	var payload bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			z, err := gzip.NewReader(r.Body)
			if err != nil {
				w.WriteHeader(400)
				return
			}
			defer func() {
				if err := z.Close(); err != nil {
					t.Errorf("close gzip request reader: %v", err)
				}
			}()
			reader = z
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		payload.Write(data)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(200)
	}))
	defer server.Close()
	cfg := langfuse.Config{
		Enabled:        true,
		Host:           server.URL,
		PublicKey:      "test-public",
		SecretKey:      "test-secret",
		FlushAt:        1,
		FlushInterval:  time.Millisecond,
		QueueSize:      32,
		RequestTimeout: time.Second,
		SampleRate:     1,
	}
	mgr, err := langfuse.Init(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = langfuse.Init(langfuse.Config{}) })
	ctx := types.WithLLMContentRedacted(context.Background())
	wrapped := &langfuseChat{inner: privateAssessmentModel{}}
	response, callErr := wrapped.Chat(ctx, []Message{{Role: "user", Content: "source-text-private"}}, nil)
	require.Error(t, callErr)
	require.Equal(t, "answer-key-private", response.Content)
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, mgr.Shutdown(shutdown))
	mu.Lock()
	recorded := payload.String()
	mu.Unlock()
	require.Contains(t, recorded, "content redacted")
	require.Contains(t, recorded, "usage_details")
	for _, secret := range []string{"answer-key-private", "source-text-private", "provider-error-private"} {
		require.NotContains(t, recorded, secret)
	}
	var output bytes.Buffer
	l := logrus.New()
	l.SetOutput(&output)
	ctx = context.WithValue(ctx, types.LoggerContextKey, logrus.NewEntry(l))
	client := &RemoteAPIChat{modelName: "assessment-model"}
	client.logRequest(ctx, map[string]string{"answer": "answer-key-private"}, false)
	require.Empty(t, output.String())
	clone, declared := types.ContextCloneDecision(types.LLMContentRedactedContextKey)
	require.True(t, declared)
	require.True(t, clone)
}
