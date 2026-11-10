package openaichataudio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
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

// withSeconds is a reply carrying the usage.seconds that the reference's
// schema documents ("音频时长（秒）") but its example omits.
const withSeconds = `{"choices":[{"message":{"role":"assistant","content":"x"}}],"usage":{"seconds":3}}`

func newClient(url, model string) *Client {
	return newClientWith(url, model, api.TranscriptionsSettings{})
}

func newClientWith(url, model string, settings api.TranscriptionsSettings) *Client {
	return New(Config{
		Endpoint: api.Endpoint{BaseURL: url, Model: model, Auth: api.BearerAuth("k")},
		Settings: settings,
	})
}

// The request is the one both references show: a single user message whose
// only part is the audio as a data URI, no instruction, no stream flag.
func TestRequestBodyMatchesTheReferences(t *testing.T) {
	body, err := newClient("https://example.invalid/v1", "qwen3-asr-flash").
		BuildRequestBody(api.TranscriptionRequest{Audio: []byte("ID3"), FileName: "clip.MP3"})
	require.NoError(t, err)
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
	uri, err := DataURI([]byte("RIFF"), "a.wav")
	require.NoError(t, err)
	assert.Equal(t, "data:audio/wav;base64,UklGRg==", uri)

	// No audio type can be named for it, and the references show one.
	_, err = DataURI([]byte("x"), "noext")
	assert.Error(t, err)
}

// Both references cap the string as sent, so the data: prefix counts. A file
// sized to exactly the encoded budget without the prefix goes over it.
func TestEncodedCeilingCountsTheWholeDataURI(t *testing.T) {
	const limit = 1024
	c := newClientWith("https://example.invalid", "m", api.TranscriptionsSettings{MaxEncodedBytes: limit})
	audio := make([]byte, limit*3/4) // base64 of this is exactly limit bytes
	_, err := c.BuildRequestBody(api.TranscriptionRequest{Audio: audio, FileName: "a.wav"})
	require.Error(t, err)

	prefix := len("data:audio/wav;base64,")
	fits := make([]byte, (limit-prefix)/4*3)
	_, err = c.BuildRequestBody(api.TranscriptionRequest{Audio: fits, FileName: "a.wav"})
	require.NoError(t, err)
}

func TestLanguageGoesInASROptionsWhereDeclared(t *testing.T) {
	req := api.TranscriptionRequest{Audio: []byte("x"), FileName: "a.wav", Language: "zh"}

	body, err := newClient("https://example.invalid", "m").BuildRequestBody(req)
	require.NoError(t, err)
	assert.NotContains(t, body, "asr_options", "undeclared, so not sent")

	body, err = newClientWith("https://example.invalid", "m",
		api.TranscriptionsSettings{LanguageParam: api.LanguageASROptions}).BuildRequestBody(req)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"language": "zh"}, body["asr_options"])
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
		Transcribe(context.Background(), api.TranscriptionRequest{Audio: []byte("RIFF"), FileName: "a.wav"})
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

	_, err := newClient(server.URL, "m").
		Transcribe(context.Background(), api.TranscriptionRequest{Audio: []byte("x"), FileName: "a.wav"})
	assert.Error(t, err)
}

func TestDurationFromUsageSeconds(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(withSeconds))
	}))
	defer server.Close()

	out, err := newClient(server.URL, "m").
		Transcribe(context.Background(), api.TranscriptionRequest{Audio: []byte("x"), FileName: "a.wav"})
	require.NoError(t, err)
	assert.Equal(t, float64(3), out.Duration)
}

func TestEmptyContentIsSilence(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":""}}]}`))
	}))
	defer server.Close()

	out, err := newClient(server.URL, "m").
		Transcribe(context.Background(), api.TranscriptionRequest{Audio: []byte("x"), FileName: "a.wav"})
	require.NoError(t, err)
	assert.Equal(t, "", out.Text)
}
