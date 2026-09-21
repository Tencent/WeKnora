package openaichataudio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// qwenASRResponse is the non-streaming response example from Alibaba's
// Qwen-ASR reference, copied from the page:
// https://help.aliyun.com/zh/model-studio/qwen-asr-api-reference
const qwenASRResponse = `{
    "choices": [{
        "finish_reason": "stop",
        "index": 0,
        "message": {
            "annotations": [{"emotion": "neutral", "language": "zh", "type": "audio_info"}],
            "content": "欢迎使用阿里云。",
            "role": "assistant"
        }
    }],
    "created": 1767683986,
    "id": "chatcmpl-487abe5f-d4f2-9363-a877-xxxxxxx",
    "model": "qwen3-asr-flash",
    "object": "chat.completion",
    "usage": {"completion_tokens": 12, "prompt_tokens": 42, "total_tokens": 54}
}`

func newClient(url, model string) *Client {
	return New(Config{
		Endpoint: api.Endpoint{BaseURL: url, Model: model, Auth: api.BearerAuth("k")},
		Settings: catalog.TranscriptionsSettings{},
	})
}

// The request is the one both references show: a single user message whose
// only part is the audio as a data URI, no instruction, no stream flag.
func TestRequestBodyMatchesTheReferences(t *testing.T) {
	body := newClient("https://example.invalid/v1", "qwen3-asr-flash").
		BuildRequestBody([]byte("ID3"), "clip.MP3")
	assert.Equal(t, map[string]any{
		"model": "qwen3-asr-flash",
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{map[string]any{
				"type":        "input_audio",
				"input_audio": map[string]any{"data": "data:audio/mpeg;base64,SUQz"},
			}},
		}},
	}, body)
}

func TestDataURIMediaTypeFollowsTheExtension(t *testing.T) {
	assert.Equal(t, "data:audio/wav;base64,UklGRg==", DataURI([]byte("RIFF"), "a.wav"))
	assert.Equal(t, "data:application/octet-stream;base64,eA==", DataURI([]byte("x"), "noext"))
}

func TestDecodesTheDocumentedResponse(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var path string
	var sent map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = w.Write([]byte(qwenASRResponse))
	}))
	defer server.Close()

	out, err := newClient(server.URL+"/compatible-mode/v1", "qwen3-asr-flash").
		Transcribe(context.Background(), []byte("RIFF"), "a.wav")
	require.NoError(t, err)
	assert.Equal(t, "/compatible-mode/v1/chat/completions", path)
	assert.Equal(t, &api.Transcription{Text: "欢迎使用阿里云。"}, out)
}

// A reply with no assistant content is not silence.
func TestReplyWithoutContentIsAnError(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	_, err := newClient(server.URL, "m").Transcribe(context.Background(), []byte("x"), "a.wav")
	assert.Error(t, err)
}

func TestEmptyContentIsSilence(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":""}}]}`))
	}))
	defer server.Close()

	out, err := newClient(server.URL, "m").Transcribe(context.Background(), []byte("x"), "a.wav")
	require.NoError(t, err)
	assert.Equal(t, "", out.Text)
}
