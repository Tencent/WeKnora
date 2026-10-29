package chat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
)

// Exercise the real Ollama HTTP client and streamChat, not a substitute
// producer. No model is downloaded or executed by this fixture.
func TestOllamaChatStreamAbandonedConsumer(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		consume    int
	}{
		{"answer", `{"message":{"content":"answer"}}`, 200, 0},
		{"reasoning", `{"message":{"thinking":"reasoning"}}`, 200, 0},
		{"thinking handoff", `{"message":{"thinking":"reasoning","content":"answer"}}`, 200, 1},
		{"tool call", `{"message":{"tool_calls":[{"function":{"index":0,"name":"lookup","arguments":{}}}]}}`, 200, 0},
		{"thinking tool", `{"message":{"tool_calls":[{"function":{"index":0,"name":"thinking",` +
			`"arguments":{"thought":"reasoning"}}}]}}`, 200, 1},
		{"completion", `{"message":{},"done":true,"done_reason":"stop","eval_count":2}`, 200, 0},
		{"provider error", `{"error":"fixture failure"}`, 500, 0},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			delivered := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					w.WriteHeader(http.StatusOK)
				case "/api/tags":
					_, _ = fmt.Fprint(w, `{"models":[{"name":"m:latest"}]}`)
				case "/api/chat":
					w.Header().Set("Content-Type", "application/x-ndjson")
					w.WriteHeader(tt.status)
					_, _ = fmt.Fprintln(w, tt.body)
					w.(http.Flusher).Flush()
					close(delivered)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("OLLAMA_BASE_URL", server.URL)
			t.Setenv("OLLAMA_OPTIONAL", "")
			svc, err := ollama.GetOllamaService()
			if err != nil {
				t.Fatal(err)
			}
			client := &OllamaChat{modelName: "m", ollamaService: svc}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream := make(chan types.StreamResponse)
			producerDone := make(chan struct{})
			go func() {
				defer close(producerDone)
				defer close(stream)
				req := client.buildChatRequest([]Message{{Role: "user", Content: "hi"}}, &ChatOptions{}, true)
				client.streamChat(ctx, req, stream)
			}()
			// Drain on failure too so a baseline reproduction does not leak its producer.
			defer func() {
				cancel()
				timer := time.NewTimer(2 * time.Second)
				defer timer.Stop()
				for {
					select {
					case _, ok := <-stream:
						if !ok {
							return
						}
					case <-timer.C:
						t.Error("producer cleanup did not finish")
						return
					}
				}
			}()
			for i := 0; i < tt.consume; i++ {
				select {
				case <-stream:
				case <-time.After(2 * time.Second):
					t.Fatal("missing initial event")
				}
			}
			select {
			case <-delivered:
			case <-time.After(2 * time.Second):
				t.Fatal("fixture not delivered")
			}
			cancel()
			// Do not receive from stream while waiting: that would release a
			// broken blocking send and disguise the abandoned-consumer leak.
			select {
			case <-producerDone:
			case <-time.After(2 * time.Second):
				t.Fatal("HTTP reader remained blocked after consumer cancellation")
			}
			select {
			case event, ok := <-stream:
				if ok {
					t.Fatalf("canceled producer remained blocked until drained; received %+v", event)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("stream did not close after cancellation")
			}
		})
	}
}

func TestOllamaChatStreamLiveConsumerKeepsEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
		case "/api/tags":
			_, _ = fmt.Fprint(w, `{"models":[{"name":"m:latest"}]}`)
		case "/api/chat":
			_, _ = fmt.Fprintln(w, `{"message":{"thinking":"plan"}}`)
			_, _ = fmt.Fprintln(w, `{"message":{"content":"answer"}}`)
			_, _ = fmt.Fprintln(w, `{"message":{"tool_calls":[{"function":{"index":2,"name":"thinking",`+
				`"arguments":{"thought":"inspect"}}}]}}`)
			_, _ = fmt.Fprintln(w, `{"message":{},"done":true,"done_reason":"length",`+
				`"prompt_eval_count":3,"eval_count":5}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("OLLAMA_BASE_URL", server.URL)
	t.Setenv("OLLAMA_OPTIONAL", "")
	svc, err := ollama.GetOllamaService()
	if err != nil {
		t.Fatal(err)
	}
	client := &OllamaChat{modelName: "m", ollamaService: svc}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := client.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}}, &ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var chunks []types.StreamResponse
	for event := range stream {
		chunks = append(chunks, event)
	}
	if len(chunks) != 6 {
		t.Fatalf("got %d events: %+v", len(chunks), chunks)
	}
	wantKinds := []types.ResponseType{
		types.ResponseTypeThinking, types.ResponseTypeThinking, types.ResponseTypeAnswer,
		types.ResponseTypeToolCall, types.ResponseTypeThinking, types.ResponseTypeAnswer,
	}
	for i, event := range chunks {
		if event.ResponseType != wantKinds[i] {
			t.Errorf("event %d kind=%s want=%s", i, event.ResponseType, wantKinds[i])
		}
	}
	if chunks[0].Content != "plan" || chunks[0].Done || !chunks[1].Done || chunks[2].Content != "answer" {
		t.Fatalf("thinking handoff changed: %+v", chunks[:3])
	}
	if len(chunks[3].ToolCalls) != 1 ||
		chunks[3].ToolCalls[0].Function.Name != "thinking" || chunks[3].ToolCalls[0].ID != "2" {
		t.Fatalf("tool call changed: %+v", chunks[3])
	}
	if chunks[4].Content != "inspect" || chunks[4].Data["source"] != "thinking_tool" ||
		chunks[4].Data["tool_call_id"] != "2" {
		t.Fatalf("thinking tool changed: %+v", chunks[4])
	}
	final := chunks[5]
	if !final.Done || final.FinishReason != "length" || final.Usage == nil ||
		final.Usage.PromptTokens != 3 || final.Usage.CompletionTokens != 5 || final.Usage.TotalTokens != 8 {
		t.Fatalf("completion changed: %+v", final)
	}
}
