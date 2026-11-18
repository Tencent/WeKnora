package wechat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
)

type typingTransport func(*http.Request) (*http.Response, error)

func (f typingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const typingConfigResponse = `{"ret":0,"typing_ticket":"ticket"}`

func mockTypingAPI(
	t *testing.T, configResponse string, setStatus func(context.Context, int) (string, error),
) func(...int) {
	t.Helper()
	oldClient := ilinkHTTPClient
	t.Cleanup(func() { ilinkHTTPClient = oldClient })
	var mu sync.Mutex
	var calls []int
	ilinkHTTPClient = &http.Client{Transport: typingTransport(func(r *http.Request) (*http.Response, error) {
		var request struct {
			UserID       string   `json:"ilink_user_id"`
			ContextToken string   `json:"context_token"`
			Ticket       string   `json:"typing_ticket"`
			Status       int      `json:"status"`
			BaseInfo     baseInfo `json:"base_info"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return nil, err
		}
		if r.Header.Get("Authorization") != "Bearer bot" || request.BaseInfo.ChannelVersion != channelVersion {
			t.Error("missing iLink authorization or base_info")
		}
		body := `{"ret":0}`
		switch r.URL.Path {
		case "/ilink/bot/getconfig":
			if request.UserID != "peer" || request.ContextToken != "conversation" {
				t.Errorf("getconfig request has wrong user or context: %+v", request)
			}
			body = configResponse
		case "/ilink/bot/sendtyping", "/ilink/bot/sendmessage":
			mu.Lock()
			calls = append(calls, request.Status) // 0 denotes the final sendmessage call.
			mu.Unlock()
			if r.URL.Path == "/ilink/bot/sendtyping" {
				if request.UserID != "peer" || request.Ticket != "ticket" {
					t.Errorf("sendtyping request has wrong user or ticket: %+v", request)
				}
				if setStatus != nil {
					var err error
					body, err = setStatus(r.Context(), request.Status)
					if err != nil {
						return nil, err
					}
				}
			}
		default:
			t.Errorf("unexpected iLink endpoint: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	return func(want ...int) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(calls, want) {
			t.Errorf("iLink calls = %v, want %v", calls, want)
		}
	}
}

func typingIncomingMessage() *im.IncomingMessage {
	return &im.IncomingMessage{UserID: "peer", Extra: map[string]string{"context_token": "conversation"}}
}

func TestTypingRefreshAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		checkCalls := mockTypingAPI(t, `{"ret":0,"typingTicket":"ticket"}`, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stop := NewAdapter("bot", "id").BeginTyping(ctx, typingIncomingMessage())
		defer stop()
		synctest.Wait()
		checkCalls(1)
		time.Sleep(4999 * time.Millisecond)
		synctest.Wait()
		checkCalls(1)
		time.Sleep(time.Millisecond)
		synctest.Wait()
		checkCalls(1, 1)
		cancel()
		synctest.Wait()
		checkCalls(1, 1, 2)
		stop()
		time.Sleep(5 * time.Second)
		checkCalls(1, 1, 2)
	})
}

func TestTypingCleanupStillAllowsFinalReply(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		name := "business error"
		if timeout {
			name = "timeout"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				setStatus := func(ctx context.Context, status int) (string, error) {
					if status == 1 {
						return `{"ret":0}`, nil
					}
					if timeout {
						<-ctx.Done()
						return "", ctx.Err()
					}
					return `{"ret":-2}`, nil
				}
				checkCalls := mockTypingAPI(t, typingConfigResponse, setStatus)
				adapter, msg := NewAdapter("bot", "id"), typingIncomingMessage()
				stop := adapter.BeginTyping(context.Background(), msg)
				synctest.Wait()
				started := time.Now()
				stop()
				if elapsed := time.Since(started); elapsed > 3*time.Second {
					t.Errorf("typing cleanup blocked for %v", elapsed)
				}
				reply := &im.ReplyMessage{Content: "answer", IsFinal: true}
				if err := adapter.SendReply(context.Background(), msg, reply); err != nil {
					t.Fatal(err)
				}
				checkCalls(1, 2, 0)
			})
		})
	}
}

func TestTypingCleanupWaitsForInFlightStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		releaseStart := make(chan struct{})
		checkCalls := mockTypingAPI(t, typingConfigResponse, func(ctx context.Context, status int) (string, error) {
			if status == 1 {
				select {
				case <-releaseStart:
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			return `{"ret":0}`, nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stop := NewAdapter("bot", "id").BeginTyping(ctx, typingIncomingMessage())
		defer stop()
		synctest.Wait()
		checkCalls(1)
		cancel()
		synctest.Wait()
		checkCalls(1)
		close(releaseStart)
		stop()
		checkCalls(1, 2)
	})
}

func TestTypingCleanupCancelsInFlightConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		checkCalls := mockTypingAPI(t, typingConfigResponse, nil)
		transport := ilinkHTTPClient.Transport
		configRequests := 0
		ilinkHTTPClient.Transport = typingTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/ilink/bot/getconfig" {
				configRequests++
				if configRequests == 2 {
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
			}
			return transport.RoundTrip(r)
		})
		adapter, msg := NewAdapter("bot", "id"), typingIncomingMessage()
		stop := adapter.BeginTyping(context.Background(), msg)
		defer stop()
		synctest.Wait()
		time.Sleep(5 * time.Second)
		synctest.Wait()
		started := time.Now()
		stop()
		if elapsed := time.Since(started); elapsed != 0 {
			t.Errorf("pending getconfig delayed cleanup by %v", elapsed)
		}
		checkCalls(1, 2)
	})
}

func TestTypingAcceptsEmptyStatusResponse(t *testing.T) {
	checkCalls := mockTypingAPI(t, typingConfigResponse, func(context.Context, int) (string, error) {
		return "", nil
	})
	adapter, msg := NewAdapter("bot", "id"), typingIncomingMessage()
	if _, err := adapter.setTyping(context.Background(), msg.UserID, msg.Extra["context_token"], 1); err != nil {
		t.Errorf("sendtyping returned an error for an empty HTTP 200 response: %v", err)
	}
	checkCalls(1)
}

func TestTypingNewTurnWaitsForPreviousCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		releaseCleanup := make(chan struct{})
		checkCalls := mockTypingAPI(t, typingConfigResponse, func(ctx context.Context, status int) (string, error) {
			if status == 2 {
				select {
				case <-releaseCleanup:
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			return `{"ret":0}`, nil
		})
		adapter := NewAdapter("bot", "id")
		stopFirst := adapter.BeginTyping(context.Background(), typingIncomingMessage())
		defer stopFirst()
		synctest.Wait()
		stopNext := adapter.BeginTyping(context.Background(), typingIncomingMessage())
		defer stopNext()
		synctest.Wait()
		checkCalls(1, 2)
		close(releaseCleanup)
		synctest.Wait()
		stopFirst()
		checkCalls(1, 2, 1)
		stopNext()
		checkCalls(1, 2, 1, 2)
	})
}
