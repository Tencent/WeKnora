package video

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allowLoopback lets the SSRF guard reach the test server. The video client
// refuses a base URL or upload host that fails the deployment's SSRF policy,
// so a loopback upstream needs the documented whitelist.
func allowLoopback(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

// uploadCall is one recorded upload POST.
type uploadCall struct {
	fields   []string
	values   map[string]string
	fileName string
	fileBody []byte
}

// fakeDashScope implements the three requests one video call makes and records
// what it was sent.
type fakeDashScope struct {
	mu sync.Mutex

	policyStatus int
	uploadStatus int
	chatStatus   int
	chatContent  string

	policyAuth   string
	policyQuery  string
	upload       uploadCall
	chatAuth     string
	chatResolve  string
	chatRequests []map[string]any
}

func (f *fakeDashScope) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/uploads", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.policyAuth = r.Header.Get("Authorization")
		f.policyQuery = r.URL.RawQuery
		if f.policyStatus != 0 && f.policyStatus != http.StatusOK {
			http.Error(w, `{"code":"InvalidApiKey","message":"bad key"}`, f.policyStatus)
			return
		}
		fmt.Fprintf(w, `{"data":{"policy":"policy-value","signature":"sig-value",
			"upload_dir":"dashscope-video/abc123","upload_host":"http://%s/upload",
			"oss_access_key_id":"oss-key","x_oss_object_acl":"private",
			"x_oss_forbid_overwrite":"true","max_file_size_mb":1024,
			"expire_in_seconds":172800}}`, r.Host)
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.uploadStatus != 0 && f.uploadStatus != http.StatusOK {
			http.Error(w, "AccessDenied", f.uploadStatus)
			return
		}
		reader, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		call := uploadCall{values: map[string]string{}}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			body, err := io.ReadAll(part)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			call.fields = append(call.fields, part.FormName())
			if part.FileName() != "" {
				call.fileName = part.FileName()
				call.fileBody = body
				continue
			}
			call.values[part.FormName()] = string(body)
		}
		f.upload = call
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.chatAuth = r.Header.Get("Authorization")
		f.chatResolve = r.Header.Get(ossResolveHeader)
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
		f.chatRequests = append(f.chatRequests, decoded)
		if f.chatStatus != 0 && f.chatStatus != http.StatusOK {
			http.Error(w, `{"error":{"message":"model does not support video"}}`, f.chatStatus)
			return
		}
		reply, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": f.chatContent},
				"finish_reason": "stop",
			}},
		})
		_, _ = w.Write(reply)
	})
	return mux
}

func newTestClient(t *testing.T, server *httptest.Server) Video {
	t.Helper()
	client, err := NewVideo(&Config{
		Provider:      "aliyun",
		BaseURL:       server.URL,
		ModelName:     "qwen3.8-max",
		APIKey:        "test-key",
		UploadBaseURL: server.URL,
	})
	require.NoError(t, err)
	return client
}

// The whole point of the direct mode: local bytes become an oss:// URL through
// the documented three requests, and the model call references that URL with
// the resolve header switched on.
func TestPredictUploadsThenCallsTheModelWithTheOSSURL(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{chatContent: "视频讲了如何配置模型。"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := newTestClient(t, server)

	text, err := client.Predict(context.Background(), []byte("mp4-bytes"), "demo.mp4", "这是什么视频？")
	require.NoError(t, err)
	assert.Equal(t, "视频讲了如何配置模型。", text)

	// Step 1: the policy request authenticates with the model's own key and
	// names the same model the call will use.
	assert.Equal(t, "Bearer test-key", fake.policyAuth)
	assert.Contains(t, fake.policyQuery, "action=getPolicy")
	assert.Contains(t, fake.policyQuery, "model=qwen3.8-max")

	// Step 2: the form carries every policy field, the object key lives under
	// the policy's upload_dir, and the file is the last part.
	assert.Equal(t, []string{
		"OSSAccessKeyId", "policy", "Signature", "key",
		"x-oss-object-acl", "x-oss-forbid-overwrite", "success_action_status", "file",
	}, fake.upload.fields)
	assert.Equal(t, "oss-key", fake.upload.values["OSSAccessKeyId"])
	assert.Equal(t, "policy-value", fake.upload.values["policy"])
	assert.Equal(t, "sig-value", fake.upload.values["Signature"])
	assert.Equal(t, "dashscope-video/abc123/demo.mp4", fake.upload.values["key"])
	assert.Equal(t, "private", fake.upload.values["x-oss-object-acl"])
	assert.Equal(t, "true", fake.upload.values["x-oss-forbid-overwrite"])
	assert.Equal(t, "200", fake.upload.values["success_action_status"])
	assert.Equal(t, "demo.mp4", fake.upload.fileName)
	assert.Equal(t, []byte("mp4-bytes"), fake.upload.fileBody)

	// Step 3: the call references the object, opts into oss:// and carries
	// the prompt.
	require.Len(t, fake.chatRequests, 1)
	assert.Equal(t, "enable", fake.chatResolve,
		"the resolve header is what makes oss:// addressable")
	assert.Equal(t, "Bearer test-key", fake.chatAuth)
	request := fake.chatRequests[0]
	assert.Equal(t, "qwen3.8-max", request["model"])
	messages, ok := request["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 1)
	message := messages[0].(map[string]any)
	assert.Equal(t, "user", message["role"])
	parts := message["content"].([]any)
	require.Len(t, parts, 2)
	videoPart := parts[0].(map[string]any)
	assert.Equal(t, "video_url", videoPart["type"])
	assert.Equal(t, "oss://dashscope-video/abc123/demo.mp4",
		videoPart["video_url"].(map[string]any)["url"])
	textPart := parts[1].(map[string]any)
	assert.Equal(t, "text", textPart["type"])
	assert.Equal(t, "这是什么视频？", textPart["text"])
}

// A blank prompt asks the model to describe the video: the text part is simply
// omitted, which is what the model test's video tab relies on.
func TestPredictURLWithoutPromptSendsOnlyTheVideo(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{chatContent: "一段演示视频。"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := newTestClient(t, server)

	_, err := client.PredictURL(context.Background(), "oss://dashscope-video/a.mp4", "   ")
	require.NoError(t, err)
	require.Len(t, fake.chatRequests, 1)
	messages := fake.chatRequests[0]["messages"].([]any)
	parts := messages[0].(map[string]any)["content"].([]any)
	require.Len(t, parts, 1)
	assert.Equal(t, "video_url", parts[0].(map[string]any)["type"])
}

// A rejected policy has to surface as an error: the caller degrades on it, and
// a silent empty answer would be indexed as the video's content.
func TestPredictFailsWhenThePolicyIsRejected(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{policyStatus: http.StatusUnauthorized}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := newTestClient(t, server)

	_, err := client.Predict(context.Background(), []byte("mp4-bytes"), "demo.mp4", "prompt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upload policy")
	assert.Contains(t, err.Error(), "401")
	assert.Empty(t, fake.chatRequests, "no model call may follow a failed upload")
}

// A failed object upload never becomes a model call.
func TestPredictFailsWhenTheObjectUploadIsRejected(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{uploadStatus: http.StatusForbidden}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := newTestClient(t, server)

	_, err := client.Predict(context.Background(), []byte("mp4-bytes"), "demo.mp4", "prompt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "send upload")
	assert.Contains(t, err.Error(), "403")
	assert.Empty(t, fake.chatRequests)
}

// An upstream that rejects the video (wrong model, unsupported modality) is an
// error, not an empty answer.
func TestPredictFailsWhenTheModelRejectsTheVideo(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{chatStatus: http.StatusBadRequest}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := newTestClient(t, server)

	_, err := client.Predict(context.Background(), []byte("mp4-bytes"), "demo.mp4", "prompt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "video model call")
	assert.Contains(t, err.Error(), "does not support video")
}

// An answer without choices or with an empty message is not a description of
// the video, so it must not be indexed as one.
func TestPredictRejectsEmptyAnswers(t *testing.T) {
	allowLoopback(t)
	fake := &fakeDashScope{chatContent: ""}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := newTestClient(t, server)

	_, err := client.PredictURL(context.Background(), "oss://dashscope-video/a.mp4", "prompt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty answer")

	_, err = client.Predict(context.Background(), nil, "demo.mp4", "prompt")
	require.Error(t, err, "empty bytes are a configuration error, not an upload")
}

// The client refuses a vendor that does not serve video rather than sending a
// chat request that the endpoint would reject with a less useful message.
func TestNewVideoRejectsAVendorWithoutVideoSupport(t *testing.T) {
	_, err := NewVideo(&Config{
		Provider:  "openai",
		BaseURL:   "https://api.openai.com/v1",
		ModelName: "gpt-4o",
		APIKey:    "k",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedVendor)
	assert.Contains(t, err.Error(), "OpenAI")
}

// The object name a stored video turns into must not leave the policy's upload
// directory, and two videos whose names only differ in characters the key
// cannot carry must not collide.
func TestSanitizeFileNameKeepsTheExtensionAndAvoidsCollisions(t *testing.T) {
	assert.Equal(t, "demo.mp4", sanitizeFileName("demo.mp4"))
	assert.Equal(t, "passwd", sanitizeFileName("../../etc/passwd"))
	assert.Equal(t, "video", sanitizeFileName("   "))
	assert.Equal(t, "no-extension", sanitizeFileName("no-extension"))

	first := sanitizeFileName("第一讲 录屏.mp4")
	second := sanitizeFileName("第二讲 录屏.mp4")
	assert.True(t, strings.HasSuffix(first, ".mp4"), first)
	assert.True(t, strings.HasSuffix(second, ".mp4"), second)
	assert.NotEqual(t, first, second, "different source names must not share an object key")
	assert.Equal(t, "dashscope-video/abc/"+sanitizeFileName("录屏.mp4"),
		objectKey("dashscope-video/abc", "录屏.mp4"))
}

// The multipart body must carry the policy fields and the bytes in the order
// OSS expects: it stops reading at the file part.
func TestEncodeUploadFormPutsTheFileLast(t *testing.T) {
	body, contentType, err := encodeUploadForm(&uploadPolicy{
		Policy: "p", Signature: "s", OSSAccessKeyID: "k",
		OSSObjectACL: "private", OSSForbidOverwrite: "true",
	}, "dir/name.mp4", []byte("bytes"), "name.mp4")
	require.NoError(t, err)
	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	assert.Equal(t, "multipart/form-data", mediaType)
	require.NotEmpty(t, params["boundary"])

	reader := multipart.NewReader(strings.NewReader(string(body)), params["boundary"])
	var names []string
	var file []byte
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names = append(names, part.FormName())
		if part.FormName() == "file" {
			file, err = io.ReadAll(part)
			require.NoError(t, err)
		}
	}
	assert.Equal(t, []string{
		"OSSAccessKeyId", "policy", "Signature", "key",
		"x-oss-object-acl", "x-oss-forbid-overwrite", "success_action_status", "file",
	}, names)
	assert.Equal(t, []byte("bytes"), file)
}

// The upload service root follows the model's own DashScope host, so an
// international account uploads to the domain it calls; anything else falls
// back to the public endpoint.
func TestUploadBaseURLFollowsTheDashScopeHost(t *testing.T) {
	assert.Equal(t, "https://dashscope-intl.aliyuncs.com",
		uploadBaseURL("", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"))
	assert.Equal(t, defaultUploadBaseURL, uploadBaseURL("", "https://api.openai.com/v1"))
	assert.Equal(t, defaultUploadBaseURL, uploadBaseURL("", ""))
	assert.Equal(t, "https://relay.internal/upload",
		uploadBaseURL("https://relay.internal/upload/", "https://dashscope.aliyuncs.com/compatible-mode/v1"))
}
