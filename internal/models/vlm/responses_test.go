package vlm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const vlmResponsesCompletedBody = `{"id":"resp_v","object":"response","status":"completed","model":"m","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"page text"}]}],"usage":{"input_tokens":50,"output_tokens":5}}`

const vlmResponsesIncompleteBody = `{"id":"resp_v2","object":"response","status":"incomplete","model":"m","output":[],"usage":{"input_tokens":50,"output_tokens":20}}`

func newVLMResponsesTestServer(t *testing.T, lastPath *string, lastRequest *map[string]interface{}, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*lastPath = r.URL.Path
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode VLM responses request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		*lastRequest = req
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func newResponsesVLM(t *testing.T, server *httptest.Server, extra map[string]any) *RemoteAPIVLM {
	t.Helper()
	v, err := NewRemoteAPIVLM(&Config{
		BaseURL:   server.URL,
		ModelName: "muse-spark-1.3-contributor",
		APIKey:    "k",
		Provider:  "responses",
		Extra:     extra,
	})
	if err != nil {
		t.Fatalf("NewRemoteAPIVLM: %v", err)
	}
	return v
}

func TestRemoteAPIVLMResponsesPredict(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")
	var lastPath string
	var lastRequest map[string]interface{}
	server := newVLMResponsesTestServer(t, &lastPath, &lastRequest, vlmResponsesCompletedBody)
	defer server.Close()

	v := newResponsesVLM(t, server, nil)
	text, err := v.Predict(t.Context(), [][]byte{testPNG}, "read this")
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if text != "page text" {
		t.Errorf("text = %q", text)
	}
	if lastPath != "/responses" {
		t.Errorf("path = %q", lastPath)
	}
	reqJSON, _ := json.Marshal(lastRequest)
	if !strings.Contains(string(reqJSON), "input_image") {
		t.Errorf("request lacks input_image: %s", reqJSON)
	}
}

func TestRemoteAPIVLMResponsesEffort(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")
	var lastRequest map[string]interface{}
	var lastPath string
	server := newVLMResponsesTestServer(t, &lastPath, &lastRequest, vlmResponsesCompletedBody)
	defer server.Close()

	v := newResponsesVLM(t, server, nil)
	if _, err := v.Predict(t.Context(), [][]byte{testPNG}, "hi"); err != nil {
		t.Fatal(err)
	}
	reqJSON, _ := json.Marshal(lastRequest)
	if !strings.Contains(string(reqJSON), `"effort":"medium"`) {
		t.Errorf("default effort medium missing: %s", reqJSON)
	}

	v2 := newResponsesVLM(t, server, map[string]any{"reasoning_effort": "low"})
	if _, err := v2.Predict(t.Context(), [][]byte{testPNG}, "hi"); err != nil {
		t.Fatal(err)
	}
	reqJSON, _ = json.Marshal(lastRequest)
	if !strings.Contains(string(reqJSON), `"effort":"low"`) {
		t.Errorf("configured effort low missing: %s", reqJSON)
	}
}

func TestRemoteAPIVLMResponsesIncompleteErrors(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")
	var lastRequest map[string]interface{}
	var lastPath string
	server := newVLMResponsesTestServer(t, &lastPath, &lastRequest, vlmResponsesIncompleteBody)
	defer server.Close()

	v := newResponsesVLM(t, server, nil)
	if _, err := v.Predict(t.Context(), [][]byte{testPNG}, "hi"); err == nil {
		t.Error("incomplete envelope should error (mirrors length-truncation)")
	}
}
