package llm

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestCompleteRetriesServerErrorsWithFreshRequestBody(t *testing.T) {
	attempts := 0
	client := NewClient(config.LLMConfig{
		BaseURL: "https://llm.example.test/v1", APIKey: "key", Model: "model",
		TimeoutSeconds: 10, PromptVersion: "prompt-v1",
	})
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if !strings.Contains(string(body), `"model":"model"`) ||
			!strings.Contains(string(body), `"temperature":0`) ||
			!strings.Contains(string(body), `"response_format":{"type":"json_object"}`) {
			t.Fatalf("request body = %s", body)
		}
		if attempts < 3 {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("temporary")), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"{\"content\":\"ok\"}"}}]}`)), Header: make(http.Header)}, nil
	})

	content, err := client.Complete(t.Context(), "prompt")
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if content != `{"content":"ok"}` || attempts != 3 {
		t.Fatalf("content=%q attempts=%d", content, attempts)
	}
}

func TestClientExposesModelAndPromptVersion(t *testing.T) {
	client := NewClient(config.LLMConfig{Model: "model", PromptVersion: "prompt-v2"})
	if client.Model() != "model" || client.PromptVersion() != "prompt-v2" {
		t.Fatalf("model=%q prompt_version=%q", client.Model(), client.PromptVersion())
	}
}

func TestStreamEmitsOpenAICompatibleDeltas(t *testing.T) {
	client := NewClient(config.LLMConfig{BaseURL: "https://llm.example.test/v1", APIKey: "key", Model: "model"})
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(body), `"stream":true`) {
			t.Fatalf("request body = %s", body)
		}
		stream := "data: " + `{"choices":[{"delta":{"content":"{"}}]}` + "\n\n" + "data: " + `{"choices":[{"delta":{"content":"}"}}]}` + "\n\n" + "data: [DONE]\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
	})
	var deltas []string
	content, err := client.Stream(t.Context(), "prompt", func(delta string) error { deltas = append(deltas, delta); return nil })
	if err != nil || content != "{}" || strings.Join(deltas, "") != "{}" {
		t.Fatalf("content=%q deltas=%v err=%v", content, deltas, err)
	}
}
