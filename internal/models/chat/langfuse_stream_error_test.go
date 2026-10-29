package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

type langfuseStreamChat struct {
	*fakeChat
	chunks []types.StreamResponse
}

func (f *langfuseStreamChat) ChatStream(context.Context, []Message, *ChatOptions) (<-chan types.StreamResponse, error) {
	ch := make(chan types.StreamResponse, len(f.chunks))
	for _, chunk := range f.chunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func TestLangfuseChatStreamRecordsProviderError(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "provider error"
		}
		t.Run(name, func(t *testing.T) {
			exported := make(chan *tracepb.Span, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				var request collectortrace.ExportTraceServiceRequest
				if err := proto.Unmarshal(body, &request); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				for _, resource := range request.ResourceSpans {
					for _, scope := range resource.ScopeSpans {
						for _, span := range scope.Spans {
							for _, attr := range span.Attributes {
								if attr.Key == "langfuse.observation.type" &&
									attr.Value.GetStringValue() == "generation" {
									exported <- span
								}
							}
						}
					}
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			manager, err := langfuse.Init(langfuse.Config{
				Enabled: true, Host: server.URL, PublicKey: "pk-test", SecretKey: "sk-test",
				FlushAt: 8, FlushInterval: time.Hour, QueueSize: 32, RequestTimeout: time.Second, SampleRate: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = manager.Shutdown(context.Background())
				_, _ = langfuse.Init(langfuse.Config{Enabled: false})
			})
			chunks := []types.StreamResponse{{ResponseType: types.ResponseTypeAnswer, Content: "partial"}}
			if fail {
				chunks = append(chunks, types.StreamResponse{
					ResponseType: types.ResponseTypeError, Content: "provider failed",
				})
			}
			chunks = append(chunks, types.StreamResponse{
				ResponseType: types.ResponseTypeAnswer, Done: true,
				Usage: &types.TokenUsage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3},
			})
			wrapper := &langfuseChat{inner: &langfuseStreamChat{fakeChat: &fakeChat{id: "test-model"}, chunks: chunks}}
			out, err := wrapper.ChatStream(context.Background(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var got []types.StreamResponse
			for chunk := range out {
				got = append(got, chunk)
			}
			if !reflect.DeepEqual(got, chunks) {
				t.Fatalf("forwarded chunks = %+v, want %+v", got, chunks)
			}
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := manager.Shutdown(shutdownCtx); err != nil {
				t.Fatal(err)
			}
			select {
			case span := <-exported:
				attrs := make(map[string]string)
				for _, attr := range span.Attributes {
					attrs[attr.Key] = attr.Value.GetStringValue()
				}
				var output struct{ Content string }
				if err := json.Unmarshal([]byte(attrs["langfuse.observation.output"]), &output); err != nil ||
					output.Content != "partial" {
					t.Fatalf("generation lost partial output: %+v, err=%v", output, err)
				}
				var usage langfuse.TokenUsage
				if err := json.Unmarshal([]byte(attrs["langfuse.observation.usage_details"]), &usage); err != nil ||
					usage.Input != 2 || usage.Output != 1 || usage.Total != 3 {
					t.Fatalf("generation lost usage: %+v, err=%v", usage, err)
				}
				if fail {
					if span.GetStatus().GetCode() != tracepb.Status_STATUS_CODE_ERROR ||
						span.GetStatus().GetMessage() != "provider failed" {
						t.Fatalf("failed stream exported status = %v", span.GetStatus())
					}
				} else if span.GetStatus().GetCode() == tracepb.Status_STATUS_CODE_ERROR {
					t.Fatalf("successful stream exported error = %v", span.GetStatus())
				}
			case <-time.After(time.Second):
				t.Fatal("stream generation was not exported")
			}
		})
	}
}
