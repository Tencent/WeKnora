package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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

func TestStreamUsesCallerDeadlineInsteadOfClientTimeout(t *testing.T) {
	client := NewClient(config.LLMConfig{BaseURL: "https://llm.example.test/v1", APIKey: "key", Model: "model", TimeoutSeconds: 1})
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		deadline, ok := request.Context().Deadline()
		if !ok || time.Until(deadline) < 4*time.Second {
			t.Fatalf("request deadline was shortened to the client timeout: deadline=%v ok=%v", deadline, ok)
		}
		stream := "data: " + `{"choices":[{"delta":{"content":"{}"}}]}` + "\n\n" + "data: [DONE]\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	content, err := client.Stream(ctx, "prompt", nil)
	if err != nil || content != "{}" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}

func TestCompleteJSONDisablesThinkingAndRequiresCompletedStream(t *testing.T) {
	client := NewClient(config.LLMConfig{BaseURL: "https://llm.example.test/v1", APIKey: "key", Model: "MiniMax-M3"})
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		for _, expected := range []string{`"stream":true`, `"response_format":{"type":"json_object"}`, `"thinking":{"type":"disabled"}`} {
			if !strings.Contains(string(body), expected) {
				t.Fatalf("request body missing %s: %s", expected, body)
			}
		}
		stream := "data: " + `{"choices":[{"delta":{"content":"{}"},"finish_reason":"stop"}]}` + "\n\n" + "data: [DONE]\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
	})

	content, err := client.CompleteJSON(t.Context(), "prompt")
	if err != nil || content != "{}" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}

func TestCompleteJSONAcceptsFinishReasonWithoutDoneMarker(t *testing.T) {
	client := NewClient(config.LLMConfig{BaseURL: "https://llm.example.test/v1", APIKey: "key", Model: "MiniMax-M3"})
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		stream := "data: " + `{"choices":[{"delta":{"content":"{}"},"finish_reason":"stop"}]}` + "\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
	})

	content, err := client.CompleteJSON(t.Context(), "prompt")
	if err != nil || content != "{}" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}

func TestCompleteJSONNormalizesKnownModelWrappers(t *testing.T) {
	client := NewClient(config.LLMConfig{BaseURL: "https://llm.example.test/v1", APIKey: "key", Model: "MiniMax-M3"})
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		content := "<think>reasoning</think>\n<final>\n```json\n{}\n```\n</final>"
		payload, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"delta":         map[string]any{"content": content},
				"finish_reason": "stop",
			}},
		})
		if err != nil {
			t.Fatalf("encode stream fixture: %v", err)
		}
		stream := "data: " + string(payload) + "\n\ndata: [DONE]\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
	})

	content, err := client.CompleteJSON(t.Context(), "prompt")
	if err != nil || content != "{}" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}

func TestCompleteJSONRejectsInvalidCompletedOutputWithoutRetry(t *testing.T) {
	attempts := 0
	client := NewClient(config.LLMConfig{BaseURL: "https://llm.example.test/v1", APIKey: "key", Model: "MiniMax-M3"})
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		stream := "data: " + `{"choices":[{"delta":{"content":"not json"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
	})

	_, err := client.CompleteJSON(t.Context(), "prompt")
	var invalid *InvalidOutputError
	if !errors.As(err, &invalid) || attempts != 1 {
		t.Fatalf("attempts=%d invalid=%#v err=%v", attempts, invalid, err)
	}
}

func TestCompleteJSONReturnsIncompleteStreamWithoutTransportRetry(t *testing.T) {
	attempts := 0
	client := NewClient(config.LLMConfig{BaseURL: "https://llm.example.test/v1", APIKey: "key", Model: "MiniMax-M3"})
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		stream := "data: " + `{"choices":[{"delta":{"content":"<think>partial"}}]}` + "\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
	})

	_, err := client.CompleteJSON(t.Context(), "prompt")
	var incomplete *IncompleteOutputError
	if !errors.As(err, &incomplete) || incomplete.Attempts != 1 || attempts != 1 {
		t.Fatalf("attempts=%d incomplete=%#v err=%v", attempts, incomplete, err)
	}
}

func TestCompleteJSONReturnsTruncatedJSONWithoutTransportRetry(t *testing.T) {
	attempts := 0
	client := NewClient(config.LLMConfig{BaseURL: "https://llm.example.test/v1", APIKey: "key", Model: "MiniMax-M3"})
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		payload, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"delta":         map[string]any{"content": `{"chapters":[`},
				"finish_reason": "stop",
			}},
		})
		if err != nil {
			t.Fatalf("encode stream fixture: %v", err)
		}
		stream := "data: " + string(payload) + "\n\ndata: [DONE]\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
	})

	_, err := client.CompleteJSON(t.Context(), "prompt")
	var incomplete *IncompleteOutputError
	if !errors.As(err, &incomplete) || incomplete.Attempts != 1 || attempts != 1 {
		t.Fatalf("attempts=%d incomplete=%#v err=%v", attempts, incomplete, err)
	}
}

func TestCompleteJSONReturnsConnectionClosedWithoutTransportRetry(t *testing.T) {
	attempts := 0
	client := NewClient(config.LLMConfig{BaseURL: "https://llm.example.test/v1", APIKey: "key", Model: "MiniMax-M3"})
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		return nil, io.EOF
	})

	_, err := client.CompleteJSON(t.Context(), "prompt")
	var connectionClosed *ConnectionClosedError
	if !errors.As(err, &connectionClosed) || attempts != 1 {
		t.Fatalf("attempts=%d connectionClosed=%#v err=%v", attempts, connectionClosed, err)
	}
}

func TestCompleteJSONDoesNotRetryOutputTokenLimit(t *testing.T) {
	attempts := 0
	client := NewClient(config.LLMConfig{BaseURL: "https://llm.example.test/v1", APIKey: "key", Model: "MiniMax-M3"})
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		stream := "data: " + `{"choices":[{"delta":{"content":"partial"},"finish_reason":"length"}]}` + "\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
	})

	_, err := client.CompleteJSON(t.Context(), "prompt")
	var incomplete *IncompleteOutputError
	if !errors.As(err, &incomplete) || incomplete.FinishReason != "length" || incomplete.Attempts != 1 || attempts != 1 {
		t.Fatalf("attempts=%d incomplete=%#v err=%v", attempts, incomplete, err)
	}
}

func TestCompleteJSONReturnsServerErrorWithoutTransportRetry(t *testing.T) {
	attempts := 0
	client := NewClient(config.LLMConfig{BaseURL: "https://llm.example.test/v1", APIKey: "key", Model: "MiniMax-M3"})
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("temporary"))}, nil
	})

	_, err := client.CompleteJSON(t.Context(), "prompt")
	var temporary *TemporaryError
	if !errors.As(err, &temporary) || attempts != 1 {
		t.Fatalf("attempts=%d temporary=%#v err=%v", attempts, temporary, err)
	}
}
