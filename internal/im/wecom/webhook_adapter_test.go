package wecom

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

func TestWebhookAdapterDecryptPadding(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	corpID := "ww_corp_id"
	adapter := &WebhookAdapter{aesKey: key, corpID: corpID}

	tests := []struct {
		name       string
		message    string
		wantPadLen int
	}{
		{
			name:       "padding below AES block size",
			message:    "message with seven-byte pad",
			wantPadLen: 7,
		},
		{
			name:       "padding above AES block size",
			message:    "message with 19",
			wantPadLen: 19,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, padLen := encryptWeComTestPayload(t, key, corpID, []byte(tt.message))
			if padLen != tt.wantPadLen {
				t.Fatalf("test fixture padding = %d, want %d", padLen, tt.wantPadLen)
			}

			got, err := adapter.decrypt(encrypted)
			if err != nil {
				t.Fatalf("decrypt() error = %v", err)
			}
			if !bytes.Equal(got, []byte(tt.message)) {
				t.Fatalf("decrypt() = %q, want %q", got, tt.message)
			}
		})
	}
}

func TestWebhookAdapterDecryptRejectsNonBlockAlignedCiphertext(t *testing.T) {
	adapter := &WebhookAdapter{
		aesKey: []byte("0123456789abcdef0123456789abcdef"),
		corpID: "ww_corp_id",
	}
	encrypted := base64.StdEncoding.EncodeToString(make([]byte, aes.BlockSize+1))

	if _, err := adapter.decrypt(encrypted); err == nil {
		t.Fatal("decrypt() succeeded for ciphertext with a non-block-aligned length")
	}
}

func TestWebhookAdapterParseCallbackSupportsFileAndVoice(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	adapter := &WebhookAdapter{aesKey: key, corpID: "ww_corp_id"}

	tests := []struct {
		name            string
		messageXML      string
		wantType        im.MessageType
		wantFileKey     string
		wantFileName    string
		wantFileSize    int64
		wantRecognition string
	}{
		{
			name: "file",
			messageXML: `<xml><FromUserName>alice</FromUserName><MsgType>file</MsgType>` +
				`<MediaId>media-file</MediaId><FileName>report.pdf</FileName><FileSize>1234</FileSize><MsgId>msg-file</MsgId></xml>`,
			wantType:     im.MessageTypeFile,
			wantFileKey:  "media-file",
			wantFileName: "report.pdf",
			wantFileSize: 1234,
		},
		{
			name: "voice",
			messageXML: `<xml><FromUserName>alice</FromUserName><MsgType>voice</MsgType>` +
				`<MediaId>media-voice</MediaId><Format>AMR</Format><Recognition>明天的天气</Recognition><MsgId>msg-voice</MsgId></xml>`,
			wantType:        im.MessageTypeVoice,
			wantFileKey:     "media-voice",
			wantFileName:    "msg-voice.amr",
			wantRecognition: "明天的天气",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseEncryptedWebhookTestMessage(t, adapter, key, tt.messageXML)
			if got.MessageType != tt.wantType || got.FileKey != tt.wantFileKey || got.FileName != tt.wantFileName {
				t.Fatalf("message = %#v", got)
			}
			if got.FileSize != tt.wantFileSize {
				t.Fatalf("FileSize = %d, want %d", got.FileSize, tt.wantFileSize)
			}
			if got.Extra["recognition"] != tt.wantRecognition {
				t.Fatalf("recognition = %q, want %q", got.Extra["recognition"], tt.wantRecognition)
			}
		})
	}
}

func TestWebhookAdapterParseCallbackKeepsMalformedVoiceVisible(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	adapter := &WebhookAdapter{aesKey: key, corpID: "ww_corp_id"}
	got := parseEncryptedWebhookTestMessage(t, adapter, key,
		`<xml><FromUserName>alice</FromUserName><MsgType>voice</MsgType><MsgId>msg-voice</MsgId></xml>`)
	if got.MessageType != im.MessageTypeText || got.Extra["raw_msgtype"] != "audio" {
		t.Fatalf("message = %#v, want visible empty-audio event", got)
	}
}

func TestRemoteImageHostAllowlist(t *testing.T) {
	rules, err := parseRemoteImageHostAllowlist("images.example.com, *.cdn.example.com images.example.com")
	if err != nil {
		t.Fatalf("parseRemoteImageHostAllowlist() error = %v", err)
	}
	adapter := &WebhookAdapter{remoteImageHosts: rules}
	for _, rawURL := range []string{
		"https://images.example.com/a.png",
		"https://a.cdn.example.com/b.png",
		"http://nested.a.cdn.example.com/c.png",
	} {
		if !adapter.isRemoteImageURLAllowed(rawURL) {
			t.Errorf("URL %q should be allowed", rawURL)
		}
	}
	for _, rawURL := range []string{
		"https://example.com/a.png",
		"https://cdn.example.com/a.png",
		"https://cdn.example.com.evil.test/a.png",
		"ftp://images.example.com/a.png",
		"https://user@example.com/a.png",
	} {
		if adapter.isRemoteImageURLAllowed(rawURL) {
			t.Errorf("URL %q should be rejected", rawURL)
		}
	}
	if _, err := parseRemoteImageHostAllowlist("https://images.example.com"); err == nil {
		t.Fatal("URL-shaped allowlist entry was accepted")
	}
}

func TestWebhookAdapterSendReplyUploadsAllowlistedRemoteImage(t *testing.T) {
	allowLocalTestServer(t)
	var sent []map[string]interface{}
	imageHits := 0
	uploadHits := 0
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/image.png":
			imageHits++
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
		case "/cgi-bin/media/upload":
			uploadHits++
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			file, _, err := r.FormFile(wecomRemoteImageFieldName)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			data, _ := io.ReadAll(file)
			_ = file.Close()
			if !bytes.Equal(data, png) {
				http.Error(w, "unexpected image data", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"errcode":0,"media_id":"media-uploaded"}`)
		case "/cgi-bin/message/send":
			var payload map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			sent = append(sent, payload)
			_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	restoreWebhookHTTPClient(t, server.Client())

	adapter := &WebhookAdapter{
		corpAgentID:      1001,
		apiBaseURL:       server.URL,
		remoteImageHosts: []string{"127.0.0.1"},
		tokenCache:       "token",
		tokenExpAt:       time.Now().Add(time.Hour),
	}
	err := adapter.SendReply(context.Background(), &im.IncomingMessage{
		UserID:   "alice",
		ChatType: im.ChatTypeDirect,
	}, &im.ReplyMessage{Content: fmt.Sprintf("答案\n![架构图](%s/image.png)", server.URL), IsFinal: true})
	if err != nil {
		t.Fatalf("SendReply() error = %v", err)
	}
	if imageHits != 1 || uploadHits != 1 {
		t.Fatalf("image hits = %d, upload hits = %d, want 1 each", imageHits, uploadHits)
	}
	if len(sent) != 2 {
		t.Fatalf("sent messages = %d, want markdown + image", len(sent))
	}
	markdown := sent[0]["markdown"].(map[string]interface{})["content"].(string)
	if !strings.Contains(markdown, "[架构图]("+server.URL+"/image.png)") || strings.Contains(markdown, "未配置为可信来源") {
		t.Fatalf("markdown = %q", markdown)
	}
	imagePayload, ok := sent[1]["image"].(map[string]interface{})
	if !ok || imagePayload["media_id"] != "media-uploaded" {
		t.Fatalf("image payload = %#v", sent[1])
	}
}

func TestWebhookAdapterSendReplyExplainsDisallowedAndFailedRemoteImages(t *testing.T) {
	tests := []struct {
		name         string
		allowHost    bool
		imageStatus  int
		wantHint     string
		wantImageHit int
	}{
		{name: "default deny", wantHint: "图片域名未配置为可信来源", wantImageHit: 0},
		{name: "download failure", allowHost: true, imageStatus: http.StatusGone, wantHint: "图片下载或上传失败", wantImageHit: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowLocalTestServer(t)
			imageHits := 0
			var markdown string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/image.png":
					imageHits++
					w.WriteHeader(tt.imageStatus)
				case "/cgi-bin/message/send":
					var payload map[string]interface{}
					_ = json.NewDecoder(r.Body).Decode(&payload)
					markdown = payload["markdown"].(map[string]interface{})["content"].(string)
					_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			restoreWebhookHTTPClient(t, server.Client())

			adapter := &WebhookAdapter{
				corpAgentID: 1001,
				apiBaseURL:  server.URL,
				tokenCache:  "token",
				tokenExpAt:  time.Now().Add(time.Hour),
			}
			if tt.allowHost {
				adapter.remoteImageHosts = []string{"127.0.0.1"}
			}
			err := adapter.SendReply(context.Background(), &im.IncomingMessage{UserID: "alice", ChatType: im.ChatTypeDirect},
				&im.ReplyMessage{Content: fmt.Sprintf("![图](%s/image.png)", server.URL), IsFinal: true})
			if err != nil {
				t.Fatalf("SendReply() error = %v", err)
			}
			if imageHits != tt.wantImageHit {
				t.Fatalf("image hits = %d, want %d", imageHits, tt.wantImageHit)
			}
			if !strings.Contains(markdown, tt.wantHint) || !strings.Contains(markdown, server.URL+"/image.png") {
				t.Fatalf("markdown = %q, want hint %q and original link", markdown, tt.wantHint)
			}
		})
	}
}

func TestWebhookAdapterRemoteImageRedirectCannotEscapeAllowlist(t *testing.T) {
	allowLocalTestServer(t)
	targetHits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits++
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	}))
	defer target.Close()

	sourceHits := 0
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceHits++
		http.Redirect(w, r, target.URL+"/private.png", http.StatusFound)
	}))
	defer source.Close()
	restoreWebhookHTTPClient(t, source.Client())

	// Use localhost for the allowlisted source while the redirect target remains
	// 127.0.0.1. The redirect must be rejected before the target is contacted.
	sourceURL := strings.Replace(source.URL, "127.0.0.1", "localhost", 1)
	adapter := &WebhookAdapter{remoteImageHosts: []string{"localhost"}}
	rendered, mediaIDs := adapter.prepareRemoteReplyImages(context.Background(), "token",
		fmt.Sprintf("![图](%s/image.png)", sourceURL))
	if sourceHits != 1 || targetHits != 0 {
		t.Fatalf("source hits = %d, target hits = %d, want 1 and 0", sourceHits, targetHits)
	}
	if len(mediaIDs) != 0 {
		t.Fatalf("media IDs = %v, want none", mediaIDs)
	}
	if !strings.Contains(rendered, "图片下载或上传失败") || !strings.Contains(rendered, sourceURL+"/image.png") {
		t.Fatalf("rendered = %q, want failure hint and original link", rendered)
	}
}

func TestWebhookAdapterDownloadFileReportsTemporaryMediaAPIError(t *testing.T) {
	allowLocalTestServer(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("access_token"); got != "token&scope=media" {
			t.Errorf("access_token = %q, want encoded token", got)
		}
		if got := r.URL.Query().Get("media_id"); got != "expired&next=1" {
			t.Errorf("media_id = %q, want encoded media ID", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errcode":40007,"errmsg":"invalid media_id"}`)
	}))
	defer server.Close()
	restoreWebhookHTTPClient(t, server.Client())
	adapter := &WebhookAdapter{
		apiBaseURL: server.URL,
		tokenCache: "token&scope=media",
		tokenExpAt: time.Now().Add(time.Hour),
	}
	_, _, err := adapter.DownloadFile(context.Background(), &im.IncomingMessage{FileKey: "expired&next=1", FileName: "file"})
	if err == nil || !strings.Contains(err.Error(), "code=40007") {
		t.Fatalf("DownloadFile() error = %v, want media API error", err)
	}
}

func parseEncryptedWebhookTestMessage(t *testing.T, adapter *WebhookAdapter, key []byte, messageXML string) *im.IncomingMessage {
	t.Helper()
	encrypted, _ := encryptWeComTestPayload(t, key, adapter.corpID, []byte(messageXML))
	body := fmt.Sprintf("<xml><Encrypt>%s</Encrypt></xml>", encrypted)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(body))
	message, err := adapter.ParseCallback(c)
	if err != nil {
		t.Fatalf("ParseCallback() error = %v", err)
	}
	if message == nil {
		t.Fatal("ParseCallback() returned nil message")
	}
	return message
}

func restoreWebhookHTTPClient(t *testing.T, client *http.Client) {
	t.Helper()
	previous := httpClient
	httpClient = client
	t.Cleanup(func() { httpClient = previous })
}

func allowLocalTestServer(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,::1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func encryptWeComTestPayload(t *testing.T, key []byte, corpID string, message []byte) (string, int) {
	t.Helper()

	plaintext := make([]byte, 16)
	messageLength := make([]byte, 4)
	binary.BigEndian.PutUint32(messageLength, uint32(len(message)))
	plaintext = append(plaintext, messageLength...)
	plaintext = append(plaintext, message...)
	plaintext = append(plaintext, []byte(corpID)...)

	padLen := wecomPKCS7BlockSize - len(plaintext)%wecomPKCS7BlockSize
	plaintext = append(plaintext, bytes.Repeat([]byte{byte(padLen)}, padLen)...)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}

	ciphertext := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(ciphertext, plaintext)
	return base64.StdEncoding.EncodeToString(ciphertext), padLen
}
