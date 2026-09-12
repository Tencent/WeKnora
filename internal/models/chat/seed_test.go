package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func allowSeedTestLoopback(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

// TestOpenAICompatibleRequestForwardsExplicitSeed proves the request that
// reaches an OpenAI-compatible provider carries the seed.
func TestOpenAICompatibleRequestForwardsExplicitSeed(t *testing.T) {
	allowSeedTestLoopback(t)
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-1", "object": "chat.completion", "created": 1, "model": "fixture",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "ok"},
			}},
		})
	}))
	defer srv.Close()

	instance, err := NewRemoteAPIChat(&ChatConfig{
		ModelName: "fixture", BaseURL: srv.URL, APIKey: "sk-test", Provider: "openai",
	})
	if err != nil {
		t.Fatalf("NewRemoteAPIChat() error = %v", err)
	}
	seed := 0
	_, err = instance.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}},
		&ChatOptions{Temperature: 0.1, Seed: seed, SeedProvided: true})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	got, ok := captured["seed"]
	if !ok {
		t.Fatal("provider request must contain the explicit seed, including seed=0")
	}
	if got != float64(0) {
		t.Fatalf("provider request seed = %v, want 0", got)
	}
}

// TestOpenAICompatibleRequestOmitsUnprovidedSeed keeps the no-seed path
// byte-identical: nothing is sent when the caller did not ask for a seed.
func TestOpenAICompatibleRequestOmitsUnprovidedSeed(t *testing.T) {
	allowSeedTestLoopback(t)
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-1", "object": "chat.completion", "created": 1, "model": "fixture",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "ok"},
			}},
		})
	}))
	defer srv.Close()

	instance, err := NewRemoteAPIChat(&ChatConfig{
		ModelName: "fixture", BaseURL: srv.URL, APIKey: "sk-test", Provider: "openai",
	})
	if err != nil {
		t.Fatalf("NewRemoteAPIChat() error = %v", err)
	}
	if _, err := instance.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}},
		&ChatOptions{Temperature: 0.1}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if _, ok := captured["seed"]; ok {
		t.Fatal("provider request must not contain a seed that was never provided")
	}
}

// TestOllamaRequestForwardsExplicitSeed proves buildChatRequest writes the
// seed into Ollama's options map.
func TestOllamaRequestForwardsExplicitSeed(t *testing.T) {
	instance := &OllamaChat{modelName: "fixture"}

	withSeed := instance.buildChatRequest([]Message{{Role: "user", Content: "hi"}},
		&ChatOptions{Temperature: 0.1, Seed: 42, SeedProvided: true}, false)
	got, ok := withSeed.Options["seed"]
	if !ok {
		t.Fatal("ollama request options must contain the explicit seed")
	}
	if got != 42 {
		t.Fatalf("ollama request seed = %v, want 42", got)
	}

	withoutSeed := instance.buildChatRequest([]Message{{Role: "user", Content: "hi"}},
		&ChatOptions{Temperature: 0.1}, false)
	if _, ok := withoutSeed.Options["seed"]; ok {
		t.Fatal("ollama request options must omit an unprovided seed")
	}

	zeroSeed := instance.buildChatRequest([]Message{{Role: "user", Content: "hi"}},
		&ChatOptions{Seed: 0, SeedProvided: true}, false)
	if got, ok := zeroSeed.Options["seed"]; !ok || got != 0 {
		t.Fatalf("explicit seed=0 must reach ollama options, got %v (present=%v)", got, ok)
	}
}

// TestAnthropicRejectsExplicitSeed proves the unsupported provider fails with
// the typed error before any HTTP request is made.
func TestAnthropicRejectsExplicitSeed(t *testing.T) {
	allowSeedTestLoopback(t)
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()

	instance, err := NewAnthropicChat(&ChatConfig{
		ModelName: "claude-fixture", BaseURL: srv.URL, APIKey: "sk-ant-test",
	})
	if err != nil {
		t.Fatalf("NewAnthropicChat() error = %v", err)
	}
	_, err = instance.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}},
		&ChatOptions{Seed: 7, SeedProvided: true})
	if !errors.Is(err, ErrChatSeedUnsupported) {
		t.Fatalf("Chat() error = %v, want ErrChatSeedUnsupported", err)
	}
	if called {
		t.Fatal("no provider request may be sent for an unsupported seed")
	}

	// Without an explicit seed the request proceeds normally.
	_, streamErr := instance.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		&ChatOptions{Seed: 7, SeedProvided: true})
	if !errors.Is(streamErr, ErrChatSeedUnsupported) {
		t.Fatalf("ChatStream() error = %v, want ErrChatSeedUnsupported", streamErr)
	}
}

func TestSeedSupportStateClassification(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		baseURL  string
		source   types.ModelSource
		want     string
	}{
		{"openai", "openai", "", types.ModelSourceOpenAI, ChatSeedSupportApplied},
		{
			"openai compatible", "generic", "https://api.example.test/v1", types.ModelSourceOpenAI,
			ChatSeedSupportApplied,
		},
		{"ollama", "ollama", "", types.ModelSourceLocal, ChatSeedSupportApplied},
		{"anthropic", "anthropic", "", types.ModelSourceOpenAI, ChatSeedSupportUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SeedSupportState(tc.provider, tc.baseURL, tc.source); got != tc.want {
				t.Fatalf("SeedSupportState() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOptionsSeedProvidedSemantics(t *testing.T) {
	if OptionsSeedProvided(nil) {
		t.Fatal("nil options carry no seed")
	}
	if OptionsSeedProvided(&ChatOptions{}) {
		t.Fatal("zero options carry no explicit seed")
	}
	if !OptionsSeedProvided(&ChatOptions{Seed: 0, SeedProvided: true}) {
		t.Fatal("explicit seed=0 must be detected")
	}
	if !OptionsSeedProvided(&ChatOptions{Seed: 5}) {
		t.Fatal("a configured non-zero seed counts as provided")
	}
}
