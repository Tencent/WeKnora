package video

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTransportTestClient builds a client against the fake vendor with the
// transport under test and the provider whose ceiling is being exercised.
func newTransportTestClient(
	t *testing.T, server *httptest.Server, provider, directInput string,
) Video {
	t.Helper()
	client, err := NewVideo(&Config{
		Provider:      provider,
		BaseURL:       server.URL,
		ModelName:     "qwen3-vl-plus",
		APIKey:        "test-key",
		UploadBaseURL: server.URL,
		DirectInput:   directInput,
	})
	require.NoError(t, err)
	return client
}

// inlineChatURL returns the video reference the fake vendor received.
func inlineChatURL(t *testing.T, fake *fakeDashScope, index int) string {
	t.Helper()
	require.Greater(t, len(fake.chatRequests), index, "the model call never arrived")
	messages, ok := fake.chatRequests[index]["messages"].([]any)
	require.True(t, ok)
	parts := messages[0].(map[string]any)["content"].([]any)
	videoPart := parts[0].(map[string]any)
	require.Equal(t, "video_url", videoPart["type"])
	return videoPart["video_url"].(map[string]any)["url"].(string)
}

// The three ceilings come from the vendors' own request limits, converted back
// into video bytes. Anything unexpected falls back to the conservative default
// instead of guessing a vendor's body limit.
func TestInlineVideoLimitFollowsTheProviderCeiling(t *testing.T) {
	dashscope := inlineBytesFor(dashscopeInlineStringLimit)
	tencent := inlineBytesFor(tencentInlineBodyLimit)

	assert.Equal(t, int64(20_999_976), dashscope, "DashScope: 28,000,000 characters of base64")
	assert.Equal(t, int64(74_999_976), tencent, "Tencent: three quarters of a 100 MB body")
	assert.Equal(t, int64(20_000_000), defaultInlineVideoBytes)

	cases := []struct {
		provider string
		want     int64
	}{
		{provider: "aliyun", want: dashscope},
		{provider: "dashscope", want: dashscope},
		{provider: " Aliyun ", want: dashscope},
		{provider: "tencent", want: tencent},
		{provider: "hunyuan", want: tencent},
		{provider: "", want: defaultInlineVideoBytes},
		{provider: "openai", want: defaultInlineVideoBytes},
		// Auto-detection resolves a row that named no provider to generic, so
		// an unknown vendor must never inherit a vendor ceiling.
		{provider: "generic", want: defaultInlineVideoBytes},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			assert.Equal(t, tc.want, inlineVideoLimit(tc.provider))
		})
	}

	// The DashScope ceiling is the string length, not the file size: the value
	// must still fit once base64 and the data-URL prefix are added, or every
	// call at the limit would be rejected by the vendor's parser.
	encoded := int64(4 * ((dashscope + 2) / 3))
	assert.LessOrEqual(t, encoded+inlineDataURLOverhead, dashscopeInlineStringLimit,
		"the inline ceiling must leave room for base64 and the data URL prefix")
}

// A file exactly at the ceiling still goes inline; one byte more does not.
// The upload path is the only one that accepts the larger file.
func TestFitsInlineIncludesTheCeilingItself(t *testing.T) {
	client := &dashscopeVideo{provider: "aliyun"}
	limit := inlineVideoLimit("aliyun")
	assert.True(t, client.fitsInline(limit), "a video at the ceiling still fits")
	assert.False(t, client.fitsInline(limit+1))

	// The provider, not the model name, decides: an unknown vendor keeps the
	// conservative default even when its file would fit DashScope's ceiling.
	other := &dashscopeVideo{provider: "generic"}
	assert.Equal(t, defaultInlineVideoBytes, inlineVideoLimit(other.provider))
	assert.False(t, other.fitsInline(limit), "an unknown vendor must not inherit a vendor ceiling")
}

// The data URL names the container the file actually is: the vendor picks its
// decoder from the media type.
func TestInlineVideoMIMETypeFollowsTheFileName(t *testing.T) {
	cases := map[string]string{
		"demo.mp4":          "video/mp4",
		"DEMO.MP4":          "video/mp4",
		"clip.mov":          "video/quicktime",
		"lesson.MOV":        "video/quicktime",
		"lesson.webm":       "video/webm",
		"lesson.mkv":        "video/x-matroska",
		"dir/recording.m4v": "video/x-m4v",
		// Windows-style separators arrive from browser uploads.
		`C:\Videos\lesson.mp4`: "video/mp4",
		// Unknown, missing and empty names are sent as the common container.
		"lesson.unknown": "video/mp4",
		"no-extension":   "video/mp4",
		"":               "video/mp4",
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, want, inlineVideoMIMEType(name))
		})
	}
}

// auto is the default: a video that fits the ceiling goes inline, so the bytes
// never reach the vendor's temporary bucket.
func TestPredictAutoSendsSmallVideosInline(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{chatContent: "视频讲了如何配置模型。"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := newTransportTestClient(t, server, "aliyun", "")

	video := []byte("mp4-bytes")
	text, err := client.Predict(context.Background(), video, "demo.mp4", "这是什么视频？")
	require.NoError(t, err)
	assert.Equal(t, "视频讲了如何配置模型。", text)

	// No policy request and no object upload: the bytes went inside the call.
	assert.Empty(t, fake.policyAuth, "an inline video must not ask for an upload policy")
	assert.Empty(t, fake.upload.fileName, "an inline video must not be uploaded")

	url := inlineChatURL(t, fake, 0)
	prefix := "data:video/mp4;base64,"
	require.True(t, strings.HasPrefix(url, prefix), "url = %.40q, want the %s prefix", url, prefix)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	require.NoError(t, err, "the data URL must carry standard base64")
	assert.Equal(t, video, decoded, "the inline URL must carry the exact bytes")

	// A data: URL has nothing to resolve, so the oss:// header stays off.
	assert.Empty(t, fake.chatResolve,
		"the resolve header belongs to oss:// references only")

	parts := fake.chatRequests[0]["messages"].([]any)[0].(map[string]any)["content"].([]any)
	require.Len(t, parts, 2)
	assert.Equal(t, "这是什么视频？", parts[1].(map[string]any)["text"])
}

// auto on a video above the provider's ceiling goes to the temporary upload:
// the decision is a pure function of the file size, not of a failure.
func TestPredictAutoUploadsVideosAboveTheInlineLimit(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{chatContent: "长视频的总结。"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := newTransportTestClient(t, server, "aliyun", types.VideoDirectInputAuto)

	oversize := make([]byte, inlineVideoLimit("aliyun")+1)
	_, err := client.Predict(context.Background(), oversize, "long.mp4", "prompt")
	require.NoError(t, err)

	assert.Equal(t, len(oversize), len(fake.upload.fileBody),
		"the whole file must reach the temporary storage")
	assert.Equal(t, "long.mp4", fake.upload.fileName)
	assert.Equal(t, "oss://dashscope-video/abc123/long.mp4", inlineChatURL(t, fake, 0))
	assert.NotContains(t, inlineChatURL(t, fake, 0), inlineURLPrefix)
}

// base64 is a promise that the bytes are not staged in the vendor's temporary
// storage: a video over the ceiling has to fail loudly rather than quietly
// upload.
func TestPredictBase64ModeRefusesOversizedVideosWithoutUploading(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{chatContent: "不应到达这里。"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := newTransportTestClient(t, server, "aliyun", types.VideoDirectInputBase64)

	oversize := make([]byte, inlineVideoLimit("aliyun")+1)
	_, err := client.Predict(context.Background(), oversize, "long.mp4", "prompt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inline as base64")
	assert.Contains(t, err.Error(), types.VideoDirectInputUpload,
		"the error must name the way out")

	assert.Empty(t, fake.policyAuth, "base64 must not request an upload policy")
	assert.Empty(t, fake.upload.fileName, "base64 must not upload the fallback")
	assert.Empty(t, fake.chatRequests, "nothing may be sent when the promise cannot be kept")
}

// upload sends even a tiny file through the temporary storage: the operator
// asked for it, and a small file is not a reason to ignore that.
func TestPredictUploadModeAlwaysUploads(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{chatContent: "短视频。"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := newTransportTestClient(t, server, "aliyun", types.VideoDirectInputUpload)

	_, err := client.Predict(context.Background(), []byte("tiny"), "tiny.mp4", "prompt")
	require.NoError(t, err)

	assert.Equal(t, []byte("tiny"), fake.upload.fileBody)
	assert.Equal(t, "oss://dashscope-video/abc123/tiny.mp4", inlineChatURL(t, fake, 0))
	assert.Equal(t, "enable", fake.chatResolve,
		"the oss:// reference still needs the resolve header")
}

// url mode has no local bytes to send: the caller owns the reference, and
// inventing one is not an option.
func TestPredictURLModeRefusesLocalBytes(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{chatContent: "不应到达这里。"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := newTransportTestClient(t, server, "aliyun", types.VideoDirectInputURL)

	_, err := client.Predict(context.Background(), []byte("mp4-bytes"), "demo.mp4", "prompt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PredictURL")
	assert.Empty(t, fake.policyAuth)
	assert.Empty(t, fake.upload.fileName)
	assert.Empty(t, fake.chatRequests)

	// PredictURL is exactly what that mode is for.
	_, err = client.PredictURL(context.Background(), "https://cdn.example/demo.mp4", "prompt")
	require.NoError(t, err)
}

// The media type follows the filename on the wire, not only in the helper.
func TestPredictInlineNamesTheContainerItWasGiven(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{chatContent: "一段录屏。"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := newTransportTestClient(t, server, "aliyun", types.VideoDirectInputBase64)

	_, err := client.Predict(context.Background(), []byte("mov-bytes"), "screen.mov", "prompt")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(inlineChatURL(t, fake, 0), "data:video/quicktime;base64,"),
		"a QuickTime file must not be sent as video/mp4")

	_, err = client.Predict(context.Background(), []byte("mystery"), "recording.bin", "prompt")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(inlineChatURL(t, fake, 1), "data:video/mp4;base64,"),
		"an unknown extension must fall back to the common container")
}

// An unknown transport coalesces to auto, exactly like an unknown mode
// coalesces to audio_only: a typo must not disable the feature or change where
// the bytes go.
func TestWithDirectInputCoalescesUnknownModesToAuto(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{chatContent: "自动。"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := WithDirectInput(
		newTransportTestClient(t, server, "aliyun", types.VideoDirectInputBase64), "typo")

	_, err := client.Predict(context.Background(), []byte("mp4-bytes"), "demo.mp4", "prompt")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(inlineChatURL(t, fake, 0), inlineURLPrefix),
		"auto on a small video goes inline")
	assert.Empty(t, fake.upload.fileName)
}

// The transport is part of the request the caller can inspect: the inline body
// is a single content part whose video_url is the data URL.
func TestInlineRequestIsAnOpenAIChatBody(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{chatContent: "答案。"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := newTransportTestClient(t, server, "aliyun", types.VideoDirectInputBase64)

	_, err := client.Predict(context.Background(), []byte("x"), "a.webm", "讲什么")
	require.NoError(t, err)
	body, err := json.Marshal(fake.chatRequests[0])
	require.NoError(t, err)
	assert.Contains(t, string(body), `"type":"video_url"`)
	assert.Contains(t, string(body), `"url":"data:video/webm;base64,`)
	assert.NotContains(t, string(body), "oss://")
}
