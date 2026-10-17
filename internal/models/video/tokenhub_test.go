package video

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/providers"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tokenHubCall is one request the fake TokenHub endpoint observed.
type tokenHubCall struct {
	path    string
	auth    string
	resolve string
	body    map[string]any
}

// TokenHub is Tencent's OpenAI-compatible video endpoint, and it is a
// different host from the Hunyuan chat one. A hunyuan video row therefore has
// to be accepted by NewVideo, send a bearer key, append the documented chat
// path to a /v1 base URL, and carry the bytes as a video_url base64 data URL.
func TestTokenHubVideoCarriesBase64InAChatRequest(t *testing.T) {
	allowLoopback(t)

	var mu sync.Mutex
	var calls []tokenHubCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		calls = append(calls, tokenHubCall{
			path:    r.URL.Path,
			auth:    r.Header.Get("Authorization"),
			resolve: r.Header.Get(ossResolveHeader),
			body:    decoded,
		})
		mu.Unlock()
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"一只猫跳过篱笆。"},` +
			`"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client, err := NewVideo(&Config{
		Provider:    providers.HunyuanID,
		BaseURL:     server.URL + "/v1",
		ModelName:   "hunyuan-turbos-vision-video-20250728",
		APIKey:      "tokenhub-key",
		DirectInput: types.VideoDirectInputBase64,
	})
	require.NoError(t, err, "a hunyuan video row must not be refused as unsupported")

	video := []byte("tokenhub-mp4")
	answer, err := client.Predict(context.Background(), video, "demo.mp4", "视频里做了什么？")
	require.NoError(t, err)
	assert.Equal(t, "一只猫跳过篱笆。", answer)

	mu.Lock()
	recorded := append([]tokenHubCall(nil), calls...)
	mu.Unlock()
	require.Len(t, recorded, 1, "a base64 video makes exactly one model call")
	call := recorded[0]

	assert.Equal(t, "/v1/chat/completions", call.path)
	assert.Equal(t, "Bearer tokenhub-key", call.auth)
	// A data: URL has nothing to resolve, so the DashScope-only header must
	// not leak into a Tencent request.
	assert.Empty(t, call.resolve)

	assert.Equal(t, "hunyuan-turbos-vision-video-20250728", call.body["model"])
	messages, ok := call.body["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 1)
	parts, ok := messages[0].(map[string]any)["content"].([]any)
	require.True(t, ok)
	require.Len(t, parts, 2)

	videoPart := parts[0].(map[string]any)
	require.Equal(t, "video_url", videoPart["type"])
	videoURL, ok := videoPart["video_url"].(map[string]any)["url"].(string)
	require.True(t, ok)
	prefix := "data:video/mp4;base64,"
	require.True(t, strings.HasPrefix(videoURL, prefix), "url = %.40q, want the data URL", videoURL)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(videoURL, prefix))
	require.NoError(t, err, "the data URL must carry standard base64")
	assert.Equal(t, video, decoded, "the inline URL must carry the exact bytes")

	textPart := parts[1].(map[string]any)
	assert.Equal(t, "text", textPart["type"])
	assert.Equal(t, "视频里做了什么？", textPart["text"])
}

// The inline ceiling follows the vendor, not the model: TokenHub documents a
// 100 MB request body, and base64 plus the data-URL prefix have to fit in it.
func TestTokenHubInlineCeilingIsTheBodyLimit(t *testing.T) {
	assert.Equal(t, int64(74_999_976), inlineVideoLimit(providers.HunyuanID))
	assert.Equal(t, inlineVideoLimit(providers.HunyuanID), inlineVideoLimit("tencent"),
		"the legacy tencent spelling must keep the same ceiling")

	client := &dashscopeVideo{provider: providers.HunyuanID}
	limit := inlineVideoLimit(client.provider)
	assert.True(t, client.fitsInline(limit), "a video at the ceiling still fits")
	assert.False(t, client.fitsInline(limit+1), "one byte over must not go inline")
}
