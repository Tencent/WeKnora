package line

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func newContext(body []byte, signature string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/im/callback/x", bytes.NewReader(body))
	if signature != "" {
		c.Request.Header.Set("X-Line-Signature", signature)
	}
	return c
}

func TestVerifyCallback(t *testing.T) {
	const secret = "test-channel-secret"
	a := NewAdapter(secret, "token")
	body := []byte(`{"events":[]}`)

	t.Run("valid signature passes and restores body", func(t *testing.T) {
		c := newContext(body, sign(secret, body))
		if err := a.VerifyCallback(c); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
		// ParseCallback reads the body again, so it must still be readable.
		got, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Fatalf("read restored body: %v", err)
		}
		if !bytes.Equal(got, body) {
			t.Fatalf("body not restored: %q", got)
		}
	})

	t.Run("wrong signature fails", func(t *testing.T) {
		c := newContext(body, sign("other-secret", body))
		if err := a.VerifyCallback(c); err == nil {
			t.Fatal("expected signature error")
		}
	})

	t.Run("missing header fails", func(t *testing.T) {
		c := newContext(body, "")
		if err := a.VerifyCallback(c); err == nil {
			t.Fatal("expected missing-header error")
		}
	})

	t.Run("empty secret fails", func(t *testing.T) {
		empty := NewAdapter("", "token")
		c := newContext(body, sign(secret, body))
		if err := empty.VerifyCallback(c); err == nil {
			t.Fatal("expected secret-required error")
		}
	})
}

func TestParseCallback(t *testing.T) {
	a := NewAdapter("secret", "token")

	t.Run("direct text message", func(t *testing.T) {
		body := []byte(`{"events":[{"type":"message","replyToken":"rt-1",
			"source":{"type":"user","userId":"U123"},
			"message":{"id":"m1","type":"text","text":"加班費怎麼算？"}}]}`)
		msg, err := a.ParseCallback(newContext(body, ""))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if msg == nil {
			t.Fatal("expected message")
		}
		if msg.Platform != im.PlatformLine {
			t.Errorf("platform = %q", msg.Platform)
		}
		if msg.Content != "加班費怎麼算？" {
			t.Errorf("content = %q", msg.Content)
		}
		if msg.UserID != "U123" || msg.MessageID != "m1" || msg.ReplyToken != "rt-1" {
			t.Errorf("ids not mapped: %+v", msg)
		}
		if msg.ChatType != im.ChatTypeDirect {
			t.Errorf("chat type = %q", msg.ChatType)
		}
	})

	t.Run("group message keeps group id", func(t *testing.T) {
		body := []byte(`{"events":[{"type":"message","replyToken":"rt-2",
			"source":{"type":"group","groupId":"G999","userId":"U1"},
			"message":{"id":"m2","type":"text","text":"hi"}}]}`)
		msg, err := a.ParseCallback(newContext(body, ""))
		if err != nil || msg == nil {
			t.Fatalf("msg=%v err=%v", msg, err)
		}
		if msg.ChatType != im.ChatTypeGroup || msg.ChatID != "G999" {
			t.Errorf("group mapping wrong: %+v", msg)
		}
	})

	t.Run("non-text message is ignored", func(t *testing.T) {
		body := []byte(`{"events":[{"type":"message",
			"source":{"type":"user","userId":"U1"},
			"message":{"id":"m3","type":"sticker"}}]}`)
		msg, err := a.ParseCallback(newContext(body, ""))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if msg != nil {
			t.Fatalf("expected nil, got %+v", msg)
		}
	})

	t.Run("follow event returns nil", func(t *testing.T) {
		body := []byte(`{"events":[{"type":"follow","source":{"type":"user","userId":"U1"}}]}`)
		msg, err := a.ParseCallback(newContext(body, ""))
		if err != nil || msg != nil {
			t.Fatalf("msg=%v err=%v", msg, err)
		}
	})

	t.Run("invalid json fails", func(t *testing.T) {
		if _, err := a.ParseCallback(newContext([]byte(`{`), "")); err == nil {
			t.Fatal("expected decode error")
		}
	})

	t.Run("image message becomes attachment", func(t *testing.T) {
		body := []byte(`{"events":[{"type":"message","replyToken":"rt-3",
			"source":{"type":"user","userId":"U1"},
			"message":{"id":"m4","type":"image"}}]}`)
		msg, err := a.ParseCallback(newContext(body, ""))
		if err != nil || msg == nil {
			t.Fatalf("msg=%v err=%v", msg, err)
		}
		if msg.MessageType != im.MessageTypeImage {
			t.Errorf("type = %q", msg.MessageType)
		}
		if msg.FileKey != "m4" {
			t.Errorf("file key = %q", msg.FileKey)
		}
	})

	t.Run("file message keeps metadata", func(t *testing.T) {
		body := []byte(`{"events":[{"type":"message","replyToken":"rt-4",
			"source":{"type":"user","userId":"U1"},
			"message":{"id":"m5","type":"file","fileName":"report.pdf","fileSize":12345}}]}`)
		msg, err := a.ParseCallback(newContext(body, ""))
		if err != nil || msg == nil {
			t.Fatalf("msg=%v err=%v", msg, err)
		}
		if msg.MessageType != im.MessageTypeFile {
			t.Errorf("type = %q", msg.MessageType)
		}
		if msg.FileName != "report.pdf" || msg.FileSize != 12345 || msg.FileKey != "m5" {
			t.Errorf("file metadata wrong: %+v", msg)
		}
	})
}

func TestClampText(t *testing.T) {
	short := "ok"
	if got := clampText(short); got != short {
		t.Errorf("short text changed: %q", got)
	}

	long := make([]rune, maxTextLength+10)
	for i := range long {
		long[i] = '字'
	}
	got := []rune(clampText(string(long)))
	if len(got) != maxTextLength {
		t.Errorf("len = %d, want %d", len(got), maxTextLength)
	}
	if got[len(got)-1] != '…' {
		t.Errorf("expected ellipsis, got %q", got[len(got)-1])
	}
}

func TestDownloadFile(t *testing.T) {
	const token = "test-access-token"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/v2/bot/message/m1/content" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("JPEGDATA"))
	}))
	defer srv.Close()

	old := contentAPIBaseURL
	contentAPIBaseURL = srv.URL
	t.Cleanup(func() { contentAPIBaseURL = old })

	// The production client enforces an SSRF policy that blocks 127.0.0.1,
	// so tests use a plain client against the local test server.
	oldClient := httpClient
	httpClient = &http.Client{}
	t.Cleanup(func() { httpClient = oldClient })

	a := NewAdapter("secret", token)

	t.Run("image without name gets extension from content type", func(t *testing.T) {
		rc, name, err := a.DownloadFile(context.Background(), &im.IncomingMessage{FileKey: "m1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer func() { _ = rc.Close() }()

		data, _ := io.ReadAll(rc)
		if string(data) != "JPEGDATA" {
			t.Errorf("content = %q", data)
		}
		if name != "m1.jpg" {
			t.Errorf("name = %q, want m1.jpg", name)
		}
	})

	t.Run("existing file name is preserved", func(t *testing.T) {
		rc, name, err := a.DownloadFile(context.Background(),
			&im.IncomingMessage{FileKey: "m1", FileName: "report.pdf"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_ = rc.Close()

		if name != "report.pdf" {
			t.Errorf("name = %q", name)
		}
	})

	t.Run("missing file key fails", func(t *testing.T) {
		if _, _, err := a.DownloadFile(context.Background(), &im.IncomingMessage{}); err == nil {
			t.Fatal("expected error for empty file key")
		}
	})

	t.Run("api error is surfaced", func(t *testing.T) {
		if _, _, err := a.DownloadFile(context.Background(),
			&im.IncomingMessage{FileKey: "unknown"}); err == nil {
			t.Fatal("expected 404 error")
		}
	})
}

func TestExtensionFromContentType(t *testing.T) {
	cases := map[string]string{
		"image/jpeg":      ".jpg",
		"image/png":       ".png",
		"image/gif":       ".gif",
		"image/webp":      ".webp",
		"application/pdf": ".pdf",
		"text/plain":      "",
	}
	for ct, want := range cases {
		if got := extensionFromContentType(ct); got != want {
			t.Errorf("%s -> %q, want %q", ct, got, want)
		}
	}
}
