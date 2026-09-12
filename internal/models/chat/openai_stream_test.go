package chat

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func collectRawOpenAIStream(ctx context.Context, t *testing.T, body string) []types.StreamResponse {
	t.Helper()
	stream := make(chan types.StreamResponse)
	chat := &RemoteAPIChat{modelName: "fixture"}
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	go chat.processRawHTTPStream(ctx, resp, stream, nil)
	result := make([]types.StreamResponse, 0)
	for response := range stream {
		result = append(result, response)
	}
	return result
}

func openAIStreamChunk(content, finishReason string) string {
	finish := "null"
	if finishReason != "" {
		finish = fmt.Sprintf("%q", finishReason)
	}
	return fmt.Sprintf(
		"data: {\"id\":\"chunk-1\",\"object\":\"chat.completion.chunk\",\"created\":1,"+
			"\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q},"+
			"\"finish_reason\":%s}]}\n\n",
		content,
		finish,
	)
}

func TestRawOpenAIStreamRequiresProtocolCompletion(t *testing.T) {
	t.Run("explicit done succeeds", func(t *testing.T) {
		responses := collectRawOpenAIStream(
			context.Background(), t, openAIStreamChunk("partial", "")+"data: [DONE]\n\n",
		)
		if len(responses) != 2 {
			t.Fatalf("responses = %#v, want content and terminal events", responses)
		}
		if responses[1].ResponseType != types.ResponseTypeAnswer || !responses[1].Done {
			t.Fatalf("terminal response = %+v, want successful [DONE]", responses[1])
		}
	})

	t.Run("valid finish reason succeeds at EOF", func(t *testing.T) {
		responses := collectRawOpenAIStream(
			context.Background(), t, openAIStreamChunk("complete", "stop"),
		)
		for _, response := range responses {
			if response.ResponseType == types.ResponseTypeError {
				t.Fatalf("valid finish_reason produced error: %+v", response)
			}
		}
		if terminal := responses[len(responses)-1]; !terminal.Done || terminal.FinishReason != "stop" {
			t.Fatalf("terminal response = %+v, want finish_reason=stop", terminal)
		}
	})

	t.Run("partial EOF fails", func(t *testing.T) {
		responses := collectRawOpenAIStream(
			context.Background(), t, openAIStreamChunk("partial", ""),
		)
		terminal := responses[len(responses)-1]
		if terminal.ResponseType != types.ResponseTypeError || !terminal.Done ||
			!strings.Contains(terminal.Content, errOpenAIStreamIncomplete.Error()) {
			t.Fatalf("terminal response = %+v, want incomplete-stream error", terminal)
		}
	})

	t.Run("unknown finish reason does not authorize EOF", func(t *testing.T) {
		responses := collectRawOpenAIStream(
			context.Background(), t, openAIStreamChunk("partial", "provider_specific"),
		)
		terminal := responses[len(responses)-1]
		if terminal.ResponseType != types.ResponseTypeError || !terminal.Done {
			t.Fatalf("terminal response = %+v, want error", terminal)
		}
	})

	t.Run("malformed event cannot be hidden by done", func(t *testing.T) {
		responses := collectRawOpenAIStream(
			context.Background(),
			t,
			openAIStreamChunk("complete", "stop")+"data: {malformed-json}\n\n"+"data: [DONE]\n\n",
		)
		terminal := responses[len(responses)-1]
		if terminal.ResponseType != types.ResponseTypeError || !terminal.Done ||
			!strings.Contains(terminal.Content, errOpenAIStreamProtocol.Error()) {
			t.Fatalf("terminal response = %+v, want invalid-event error", terminal)
		}
		for _, response := range responses {
			if response.Done && response.ResponseType == types.ResponseTypeAnswer {
				t.Fatalf("malformed event produced successful completion: %+v", responses)
			}
		}
	})

	t.Run("provider error cannot be hidden by done", func(t *testing.T) {
		responses := collectRawOpenAIStream(
			context.Background(),
			t,
			openAIStreamChunk("complete", "stop")+
				"data: {\"error\":{\"message\":\"rate limited\",\"type\":\"rate_limit\"}}\n\n"+
				"data: [DONE]\n\n",
		)
		terminal := responses[len(responses)-1]
		if terminal.ResponseType != types.ResponseTypeError || !terminal.Done ||
			!strings.Contains(terminal.Content, errOpenAIStreamProvider.Error()) ||
			!strings.Contains(terminal.Content, "rate limited") {
			t.Fatalf("terminal response = %+v, want provider error", terminal)
		}
		for _, response := range responses {
			if response.Done && response.ResponseType == types.ResponseTypeAnswer {
				t.Fatalf("provider error produced successful completion: %+v", responses)
			}
		}
	})

	t.Run("cancellation remains cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		responses := collectRawOpenAIStream(ctx, t, openAIStreamChunk("partial", ""))
		if len(responses) == 0 {
			return
		} // A canceled consumer may receive channel closure directly.
		terminal := responses[len(responses)-1]
		if terminal.ResponseType != types.ResponseTypeError || terminal.Content != context.Canceled.Error() {
			t.Fatalf("terminal response = %+v, want context canceled", terminal)
		}
	})
}

func TestSDKOpenAIStreamRejectsPartialEOF(t *testing.T) {
	allowSeedTestLoopback(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, openAIStreamChunk("partial", ""))
	}))
	defer server.Close()

	chat, err := NewRemoteAPIChat(&ChatConfig{
		ModelName: "fixture", BaseURL: server.URL, APIKey: "sk-test", Provider: "openai",
	})
	if err != nil {
		t.Fatalf("NewRemoteAPIChat() error = %v", err)
	}
	stream, err := chat.ChatStream(context.Background(), []Message{{Role: "user", Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	var terminal types.StreamResponse
	for response := range stream {
		terminal = response
	}
	if terminal.ResponseType != types.ResponseTypeError ||
		!strings.Contains(terminal.Content, errOpenAIStreamIncomplete.Error()) {
		t.Fatalf("terminal response = %+v, want incomplete-stream error", terminal)
	}
}

func TestValidOpenAIStreamFinishReasons(t *testing.T) {
	for _, reason := range []string{"stop", "length", "tool_calls", "function_call", "content_filter"} {
		if !validOpenAIStreamFinishReason(reason) {
			t.Errorf("validOpenAIStreamFinishReason(%q) = false", reason)
		}
	}
	for _, reason := range []string{"", "provider_specific", "Stop"} {
		if validOpenAIStreamFinishReason(reason) {
			t.Errorf("validOpenAIStreamFinishReason(%q) = true", reason)
		}
	}
}
