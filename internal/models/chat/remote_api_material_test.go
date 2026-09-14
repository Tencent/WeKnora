package chat

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sashabaranov/go-openai"
)

func TestRequiredImagesCannotDisappearInProviderTransform(t *testing.T) {
	model := newTestRemoteChat(t)
	model.adapter = weKnoraCloudProvider{}
	messages := []Message{{Role: "user", Content: "describe image", Images: []string{"data:image/png;base64,YQ=="}}}
	_, _, _, err := model.buildOutbound(t.Context(), messages, &ChatOptions{RequireImages: true}, false)
	if err == nil || !strings.Contains(err.Error(), "required image") {
		t.Fatalf("text-only provider transform bypassed required images: %v", err)
	}
}

func TestFeishuImageRejectionKeepsActualModelInputTruthful(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, material := range []bool{false, true} {
			for _, requireImages := range []bool{false, true} {
				var calls atomic.Int32
				requests := make(chan string, 2)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					requests <- string(body)
					w.Header().Set("Content-Type", "application/json")
					if calls.Add(1) == 1 {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = io.WriteString(w,
							`{"error":{"message":"image unsupported","type":"invalid_request_error"}}`)
						return
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"id\":\"answer\",\"choices\":[{"+
							"\"delta\":{\"content\":\"answer\"},"+
							"\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						_, _ = io.WriteString(w,
							`{"choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}]}`)
					}
				}))
				model := newTestRemoteChat(t)
				cfg := openai.DefaultConfig("test-key")
				cfg.BaseURL, cfg.HTTPClient = server.URL, server.Client()
				model.client = openai.NewClientWithConfig(cfg)
				content := "compare images"
				if material {
					attachments := types.MessageAttachments{{FileName: "a.png", IsImage: true, ImageIndex: 1}}
					content += attachments.BuildPrompt(1)
				}
				messages := []Message{{Role: "user", Content: content, Images: []string{"data:image/png;base64,YQ=="}}}
				// Agent final synthesis repeats the query in a separate text-only message.
				messages = append(messages, Message{Role: "user", Content: content})
				opts := &ChatOptions{RequireImages: requireImages}
				var answer string
				var err error
				if stream {
					var chunks <-chan types.StreamResponse
					chunks, err = model.ChatStream(t.Context(), messages, opts)
					if err == nil {
						for chunk := range chunks {
							answer += chunk.Content
						}
					}
				} else {
					var response *types.ChatResponse
					response, err = model.Chat(t.Context(), messages, opts)
					if response != nil {
						answer = response.Content
					}
				}
				server.Close()
				if requireImages {
					if err == nil || calls.Load() != 1 {
						t.Fatal("visual understanding accepted a text-only retry")
					}
					continue
				}
				if err != nil || calls.Load() != 2 || !strings.Contains(answer, "answer") {
					t.Fatalf("fallback failed: calls=%d answer=%q err=%v", calls.Load(), answer, err)
				}
				<-requests
				retry := <-requests
				var decoded any
				if json.Unmarshal([]byte(retry), &decoded) != nil || strings.Contains(retry, "image_url") {
					t.Fatal("fallback did not remove images")
				}
				if material {
					if strings.Contains(retry, "The original image is included") ||
						!strings.Contains(retry, "NOT available") || !strings.Contains(answer, "原图") {
						t.Fatalf("fallback retained false visual evidence or hid missing images: retry=%s answer=%s",
							retry, answer)
					}
				} else if answer != "answer" {
					t.Fatal("non-material fallback behavior changed")
				}
			}
		}
	}
}
