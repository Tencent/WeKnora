package vlm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
)

func TestProviderCompletionStatus(t *testing.T) {
	for _, provider := range []string{"ollama", "cloud", "remote"} {
		for _, reason := range []string{"stop", "end_turn", "length", "max_tokens", "incomplete", "", "unknown"} {
			t.Run(provider+"/"+reason, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if provider != "ollama" || r.URL.Path == "/api/chat" {
						calls++
					}
					w.Header().Set("Content-Type", "application/json")
					if provider == "ollama" {
						if _, err := fmt.Fprintf(
							w,
							`{"message":{"content":"nonempty caption"},"done":true,"done_reason":%q}`,
							reason,
						); err != nil {
							t.Error(err)
						}
					} else {
						if _, err := fmt.Fprintf(
							w,
							`{"choices":[{"message":{"content":"nonempty caption"},"finish_reason":%q}]}`,
							reason,
						); err != nil {
							t.Error(err)
						}
					}
				}))
				defer server.Close()
				var model VLM
				var err error
				switch provider {
				case "ollama":
					t.Setenv("OLLAMA_BASE_URL", server.URL)
					svc, e := ollama.GetOllamaService()
					if e != nil {
						t.Fatal(e)
					}
					model, err = NewOllamaVLM(&Config{ModelName: "vision"}, svc)
				case "cloud":
					withVLMSSRFWhitelist(t, "127.0.0.1")
					model, err = NewWeKnoraCloudVLM(
						&Config{BaseURL: server.URL, ModelName: "vision", AppID: "app", AppSecret: "secret"},
					)
				default:
					withVLMSSRFWhitelist(t, "127.0.0.1")
					model, err = NewRemoteAPIVLM(
						&Config{BaseURL: server.URL + "/v1", ModelName: "gpt-4o", Provider: "openai"},
					)
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, opts := range []*PredictOptions{nil, {}, {RequireComplete: true}} {
					required := opts != nil && opts.RequireComplete
					var result string
					if opts == nil {
						result, err = model.Predict(context.Background(), nil, "caption")
					} else {
						result, err = model.PredictWithOptions(context.Background(), nil, "caption", opts)
					}
					legacyTruncated := provider != "cloud" && reason == "length"
					wantErr := legacyTruncated || (required && reason != "stop" && reason != "end_turn")
					if (err != nil) != wantErr {
						t.Fatalf("required=%v: result=%q err=%v wantErr=%v", required, result, err, wantErr)
					}
					if !wantErr && result != "nonempty caption" {
						t.Fatalf("changed legacy content: %q", result)
					}
					truncated := reason == "length" || reason == "max_tokens" || reason == "incomplete"
					if wantErr && truncated && !errors.Is(err, ErrTruncatedCompletion) {
						t.Fatalf("truncation contract: %v", err)
					}
				}
				if calls != 3 {
					t.Fatalf("requests=%d", calls)
				}
			})
		}
	}
}

func TestRemoteCacheIdentityTracksEffectiveBudget(t *testing.T) {
	t.Setenv("VLM_MAX_TOKENS", "5000")
	model, err := NewRemoteAPIVLM(
		&Config{BaseURL: "https://api.openai.com/v1", ModelName: "gpt-4o", Provider: "openai"},
	)
	if err != nil {
		t.Fatal(err)
	}
	first := identityOf(model)
	t.Setenv("VLM_MAX_TOKENS", "12000")
	if identityOf(model) == first {
		t.Fatal("effective token budget omitted from identity")
	}
	t.Setenv("VLM_MAX_TOKENS", "invalid")
	if identityOf(model) != first {
		t.Fatal("invalid budget must use default identity")
	}
}
