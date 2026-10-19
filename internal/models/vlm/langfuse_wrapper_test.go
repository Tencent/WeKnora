package vlm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type tracingTestVLM struct {
	spanID trace.SpanID
	opts   *PredictOptions
	err    error
}

func (m *tracingTestVLM) GetModelName() string { return "test-model" }
func (m *tracingTestVLM) GetModelID() string   { return "test-id" }
func (m *tracingTestVLM) Predict(ctx context.Context, images [][]byte, prompt string) (string, error) {
	return m.PredictWithOptions(ctx, images, prompt, nil)
}

func (m *tracingTestVLM) PredictWithOptions(
	ctx context.Context, _ [][]byte, _ string, opts *PredictOptions,
) (string, error) {
	m.spanID = trace.SpanContextFromContext(ctx).SpanID()
	m.opts = opts
	return "caption text", m.err
}

func TestVLMLangfuseRecordsBothPredictionMethods(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	manager, err := langfuse.Init(langfuse.Config{
		Enabled: true, Host: server.URL, PublicKey: "test", SecretKey: "test",
		SampleRate: 1, FlushAt: 10, QueueSize: 100, FlushInterval: time.Hour, RequestTimeout: time.Second,
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, manager.Shutdown(context.Background()))
		_, err := langfuse.Init(langfuse.Config{})
		require.NoError(t, err)
	}()
	_, parent := manager.Tracer().Start(context.Background(), "fixture")
	provider := parent.TracerProvider().(*sdktrace.TracerProvider)
	recorder := tracetest.NewSpanRecorder()
	provider.RegisterSpanProcessor(recorder)
	parent.End()

	on := true
	for _, opts := range []*PredictOptions{nil, {Thinking: &on}} {
		for _, failure := range []error{nil, errors.New("model unavailable")} {
			inner := &tracingTestVLM{err: failure}
			wrapped, err := wrapVLMLangfuse(inner, nil)
			require.NoError(t, err)
			wrapped, err = wrapVLMConcurrency(wrapped, 0, nil)
			require.NoError(t, err)
			var result string
			if opts == nil {
				result, err = wrapped.Predict(context.Background(), nil, "describe this image")
			} else {
				result, err = wrapped.PredictWithOptions(context.Background(), nil, "describe this image", opts)
			}
			require.Equal(t, failure, err)
			require.Equal(t, "caption text", result)
			require.Same(t, opts, inner.opts)
			require.True(t, inner.spanID.IsValid(), "the model call must receive the generation context")
			var generation sdktrace.ReadOnlySpan
			for _, span := range recorder.Ended() {
				if span.SpanContext().SpanID() == inner.spanID {
					generation = span
				}
			}
			require.NotNil(t, generation, "the generation must end on both success and error")
			require.Equal(t, "vlm.predict", generation.Name())
			attrs := map[string]string{}
			for _, attr := range generation.Attributes() {
				attrs[string(attr.Key)] = attr.Value.AsString()
			}
			require.Equal(t, "generation", attrs["langfuse.observation.type"])
			require.Contains(t, attrs["langfuse.observation.usage_details"], "input")
			if failure != nil {
				require.Equal(t, codes.Error, generation.Status().Code)
			}
		}
	}
}
