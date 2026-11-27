package dingtalk

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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

const testSessionWebhook = "https://oapi.dingtalk.com/robot/sendBySession?session=test-session"

// stubDingTalk replaces the package HTTP client. The session webhook answers
// with webhookBody, the OpenAPI with openAPIStatus; every request body is
// recorded under its host.
func stubDingTalk(t *testing.T, webhookBody string, openAPIStatus int) (*Adapter, map[string][]string) {
	t.Helper()
	secutils.SetSSRFWhitelistFromRaw("oapi.dingtalk.com")
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	previousClient := httpClient
	t.Cleanup(func() { httpClient = previousClient })
	sent := map[string][]string{}
	httpClient = &http.Client{Transport: sessionReplyTransport(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		sent[req.URL.Host] = append(sent[req.URL.Host], string(body))
		status, reply := http.StatusOK, webhookBody
		if req.URL.Host == "api.dingtalk.com" {
			status, reply = openAPIStatus, "{}"
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(reply))}, nil
	})}
	return &Adapter{token: "token", tokenExpAt: time.Now().Add(time.Hour)}, sent
}

func streamIncoming(messageID string) *im.IncomingMessage {
	return &im.IncomingMessage{
		UserID: "user-1", MessageID: messageID, ChatType: im.ChatTypeDirect,
		Extra: map[string]string{"session_webhook": testSessionWebhook},
	}
}

func TestSendReplyFallsBackToOpenAPIWhenSessionWebhookRefuses(t *testing.T) {
	adapter, sent := stubDingTalk(t, `{"errcode":410100,"errmsg":"send too fast"}`, http.StatusOK)

	err := adapter.SendReply(context.Background(), streamIncoming("m1"), &im.ReplyMessage{Content: "hello"})

	require.NoError(t, err)
	require.Len(t, sent["oapi.dingtalk.com"], 1)
	require.Len(t, sent["api.dingtalk.com"], 1)
	require.Contains(t, sent["api.dingtalk.com"][0], "hello")
}

func TestFinalizeStreamSendsTheAnswerOnce(t *testing.T) {
	adapter, sent := stubDingTalk(t, `{"errcode":0,"errmsg":"ok"}`, http.StatusOK)
	ctx, incoming := context.Background(), streamIncoming("m2")

	streamID, err := adapter.StartStream(ctx, incoming)
	require.NoError(t, err)
	require.NoError(t, adapter.UpdateStreamContent(ctx, incoming, streamID, "partial"))
	require.NoError(t, adapter.FinalizeStream(ctx, incoming, streamID, "final answer"))
	require.NoError(t, adapter.EndStream(ctx, incoming, streamID))

	require.Len(t, sent["oapi.dingtalk.com"], 1)
	require.Contains(t, sent["oapi.dingtalk.com"][0], "final answer")
	require.Empty(t, sent["api.dingtalk.com"])
}

func TestFinalizeStreamReportsAnUndeliveredAnswer(t *testing.T) {
	adapter, sent := stubDingTalk(t, `{"errcode":430102,"errmsg":"refused"}`, http.StatusInternalServerError)
	ctx, incoming := context.Background(), streamIncoming("m3")

	streamID, err := adapter.StartStream(ctx, incoming)
	require.NoError(t, err)
	require.Error(t, adapter.FinalizeStream(ctx, incoming, streamID, "final answer"))
	require.NoError(t, adapter.EndStream(ctx, incoming, streamID))

	// EndStream does not send the answer a second time.
	require.Len(t, sent["oapi.dingtalk.com"], 1)
	require.Len(t, sent["api.dingtalk.com"], 1)
}

func TestEndStreamWithoutFinalizeSendsCollectedContent(t *testing.T) {
	adapter, sent := stubDingTalk(t, `{"errcode":0}`, http.StatusOK)
	ctx, incoming := context.Background(), streamIncoming("m4")

	streamID, err := adapter.StartStream(ctx, incoming)
	require.NoError(t, err)
	require.NoError(t, adapter.UpdateStreamContent(ctx, incoming, streamID, "collected so far"))
	require.NoError(t, adapter.EndStream(ctx, incoming, streamID))

	require.Len(t, sent["oapi.dingtalk.com"], 1)
	require.Contains(t, sent["oapi.dingtalk.com"][0], "collected so far")
}
