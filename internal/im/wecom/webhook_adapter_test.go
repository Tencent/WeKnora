package wecom

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/im"
)

type wecomSend struct {
	path     string // "/cgi-bin/appchat/send" or "/cgi-bin/message/send"
	target   string // chatid or touser
	content  string
	accepted bool
}

type wecomTransport func(*http.Request) (*http.Response, error)

func (f wecomTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stubWeComAPI replaces the package HTTP client with one that answers like the
// WeCom send APIs: markdown content over 2048 bytes, or blank, is refused with
// an errcode. rejectPath, when set, refuses every call to that path.
func stubWeComAPI(t *testing.T, rejectPath string) (*WebhookAdapter, *[]wecomSend) {
	t.Helper()
	var sends []wecomSend
	previous := httpClient
	t.Cleanup(func() { httpClient = previous })
	httpClient = &http.Client{Transport: wecomTransport(func(req *http.Request) (*http.Response, error) {
		var payload struct {
			ChatID   string `json:"chatid"`
			ToUser   string `json:"touser"`
			Markdown struct {
				Content string `json:"content"`
			} `json:"markdown"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		content := payload.Markdown.Content
		send := wecomSend{path: req.URL.Path, target: payload.ChatID + payload.ToUser, content: content}
		send.accepted = req.URL.Path != rejectPath &&
			len(content) <= 2048 && strings.TrimSpace(content) != ""
		sends = append(sends, send)
		body := `{"errcode":0,"errmsg":"ok"}`
		if !send.accepted {
			body = `{"errcode":40058,"errmsg":"invalid Request Parameter"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	adapter := &WebhookAdapter{
		apiBaseURL: defaultAPIBaseURL,
		tokenCache: "token",
		tokenExpAt: time.Now().Add(time.Hour),
	}
	return adapter, &sends
}

func TestSplitMarkdown(t *testing.T) {
	line := strings.Repeat("知识库回答。", 20) + "\n"
	tests := map[string]string{
		"empty":            "",
		"short":            "hello",
		"at the limit":     strings.Repeat("a", maxMarkdownBytes),
		"one byte over":    strings.Repeat("a", maxMarkdownBytes+1),
		"chinese":          strings.Repeat("企业微信", 800),
		"emoji":            strings.Repeat("👍", 1500),
		"multi-line":       strings.Repeat(line, 40),
		"newline at limit": strings.Repeat("a", maxMarkdownBytes) + "\n",
	}
	for name, text := range tests {
		t.Run(name, func(t *testing.T) {
			parts := splitMarkdown(text)
			if got := strings.Join(parts, ""); got != text {
				t.Fatalf("parts do not join back to the input: %d bytes, want %d", len(got), len(text))
			}
			for i, part := range parts {
				if len(part) > maxMarkdownBytes {
					t.Errorf("part %d is %d bytes, over the %d limit", i, len(part), maxMarkdownBytes)
				}
				if !utf8.ValidString(part) {
					t.Errorf("part %d is cut inside a character", i)
				}
			}
			if len(text) <= maxMarkdownBytes && len(parts) != 1 {
				t.Errorf("text within the limit was split into %d parts", len(parts))
			}
		})
	}

	for i, part := range splitMarkdown(strings.Repeat(line, 40)) {
		if !strings.HasSuffix(part, "\n") {
			t.Errorf("multi-line part %d does not end at a line break", i)
		}
	}
}

func TestWebhookSendReplySplitsLongAnswer(t *testing.T) {
	answer := strings.Repeat("这是一段较长的知识库回答，用来验证超过两千零四十八字节的内容。\n", 40)
	if len(answer) <= maxMarkdownBytes {
		t.Fatalf("fixture is only %d bytes", len(answer))
	}

	tests := []struct {
		name       string
		incoming   *im.IncomingMessage
		rejectPath string
		wantPath   string
		wantTarget string
	}{
		{
			name:       "direct message",
			incoming:   &im.IncomingMessage{ChatType: im.ChatTypeDirect, UserID: "zhangsan"},
			wantPath:   "/cgi-bin/message/send",
			wantTarget: "zhangsan",
		},
		{
			name:       "group chat",
			incoming:   &im.IncomingMessage{ChatType: im.ChatTypeGroup, ChatID: "chat-1", UserID: "zhangsan"},
			wantPath:   "/cgi-bin/appchat/send",
			wantTarget: "chat-1",
		},
		{
			name:       "group chat the app cannot post to",
			incoming:   &im.IncomingMessage{ChatType: im.ChatTypeGroup, ChatID: "chat-1", UserID: "zhangsan"},
			rejectPath: "/cgi-bin/appchat/send",
			wantPath:   "/cgi-bin/message/send",
			wantTarget: "zhangsan",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, sends := stubWeComAPI(t, tt.rejectPath)

			reply := &im.ReplyMessage{Content: answer, IsFinal: true}
			if err := adapter.SendReply(context.Background(), tt.incoming, reply); err != nil {
				t.Fatalf("SendReply() error = %v", err)
			}

			var delivered strings.Builder
			for _, send := range *sends {
				if send.path == tt.rejectPath {
					continue
				}
				if !send.accepted {
					t.Errorf("WeCom refused a %d-byte message", len(send.content))
				}
				if send.path != tt.wantPath || send.target != tt.wantTarget {
					t.Errorf("sent to %s %q, want %s %q", send.path, send.target, tt.wantPath, tt.wantTarget)
				}
				delivered.WriteString(send.content)
			}
			if delivered.String() != answer {
				t.Errorf("delivered %d bytes, want the whole %d-byte answer", delivered.Len(), len(answer))
			}
		})
	}
}

func TestWebhookSendReplyKeepsShortAnswerInOneMessage(t *testing.T) {
	adapter, sends := stubWeComAPI(t, "")

	err := adapter.SendReply(context.Background(),
		&im.IncomingMessage{ChatType: im.ChatTypeDirect, UserID: "zhangsan"},
		&im.ReplyMessage{Content: "简短回答", IsFinal: true})
	if err != nil {
		t.Fatalf("SendReply() error = %v", err)
	}
	if len(*sends) != 1 || (*sends)[0].content != "简短回答" {
		t.Fatalf("sends = %+v, want one message with the answer", *sends)
	}
}

func TestWebhookSendReplyReportsRejectedMessage(t *testing.T) {
	adapter, _ := stubWeComAPI(t, "/cgi-bin/message/send")

	err := adapter.SendReply(context.Background(),
		&im.IncomingMessage{ChatType: im.ChatTypeDirect, UserID: "zhangsan"},
		&im.ReplyMessage{Content: "回答", IsFinal: true})
	if err == nil {
		t.Fatal("SendReply() succeeded although WeCom refused the message")
	}
}

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
