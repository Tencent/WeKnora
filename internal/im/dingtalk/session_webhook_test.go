package dingtalk

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

type sessionReplyTransport func(*http.Request) (*http.Response, error)

func (f sessionReplyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSendReplyUsesOfficialSessionWebhook(t *testing.T) {
	secutils.SetSSRFWhitelistFromRaw("oapi.dingtalk.com")
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	previousClient := httpClient
	t.Cleanup(func() { httpClient = previousClient })
	called := false
	const target = "https://oapi.dingtalk.com/robot/sendBySession?session=test-session"
	httpClient = &http.Client{Transport: sessionReplyTransport(func(req *http.Request) (*http.Response, error) {
		called = true
		require.Equal(t, target, req.URL.String())
		require.Equal(t, http.MethodPost, req.Method)
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "hello")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"errcode":0}`))}, nil
	})}
	err := (&Adapter{}).SendReply(context.Background(),
		&im.IncomingMessage{Extra: map[string]string{"session_webhook": target}},
		&im.ReplyMessage{Content: "hello"})
	require.NoError(t, err)
	require.True(t, called)
}
