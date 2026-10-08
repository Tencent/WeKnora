package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/im"
)

func registerTestStream(t *testing.T, id string, state *feishuStreamState) {
	t.Helper()
	feishuStreamsMu.Lock()
	feishuStreams[id] = state
	feishuStreamsMu.Unlock()
	t.Cleanup(func() {
		feishuStreamsMu.Lock()
		delete(feishuStreams, id)
		feishuStreamsMu.Unlock()
	})
}

func hasTestStream(id string) bool {
	feishuStreamsMu.Lock()
	defer feishuStreamsMu.Unlock()
	_, ok := feishuStreams[id]
	return ok
}

func TestStreamReaperPreservesLiveOwnerAndReclaimsAbandonedState(t *testing.T) {
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registerTestStream(t, "live-silent", &feishuStreamState{
		lastActivityAt: now.Add(-2 * streamOrphanTTL), ownerDone: ctx.Done(),
	})
	registerTestStream(t, "abandoned", &feishuStreamState{lastActivityAt: now.Add(-2 * streamOrphanTTL)})
	registerTestStream(t, "recent", &feishuStreamState{lastActivityAt: now})

	reapOrphanStreams(now)
	if !hasTestStream("live-silent") {
		t.Fatal("active full-output task was reaped without intermediate updates")
	}
	if hasTestStream("abandoned") {
		t.Fatal("abandoned stream was not reaped")
	}
	if !hasTestStream("recent") {
		t.Fatal("recent stream was reaped")
	}

	cancel()
	reapOrphanStreams(now.Add(streamReaperInterval))
	if !hasTestStream("live-silent") {
		t.Fatal("cancelled task lost its final-delivery grace period")
	}
	reapOrphanStreams(now.Add(streamOrphanTTL + time.Second))
	if hasTestStream("live-silent") {
		t.Fatal("cancelled, abandoned stream was not reclaimed")
	}
}

func TestStreamReaperKeepsTouchedStateWithoutOwner(t *testing.T) {
	registerTestStream(t, "touched", &feishuStreamState{lastActivityAt: time.Now().Add(-2 * streamOrphanTTL)})
	if _, ok := lookupStream("touched"); !ok {
		t.Fatal("stream not found")
	}
	reapOrphanStreams(time.Now())
	if !hasTestStream("touched") {
		t.Fatal("recently used stream was reaped")
	}
}

func TestLongRunningStreamFinalizesAfterReaper(t *testing.T) {
	useTestHTTPClient(t)
	const cardID = "long-running-card"
	var sequences []int
	var finalContent, finalSettings string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/cardkit/v1/cards":
			_, _ = w.Write([]byte(`{"code":0,"data":{"card_id":"long-running-card"}}`))
		case "/open-apis/im/v1/messages":
			_, _ = w.Write([]byte(`{"code":0}`))
		default:
			var body struct {
				Sequence int    `json:"sequence"`
				Content  string `json:"content"`
				Settings string `json:"settings"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode request: %v", err)
			}
			sequences = append(sequences, body.Sequence)
			if r.Method == http.MethodPut {
				finalContent = body.Content
			} else {
				finalSettings = body.Settings
			}
			_, _ = w.Write([]byte(`{"code":0}`))
		}
	}))
	defer srv.Close()
	adapter := &Adapter{region: testRegion(srv.URL), apiBaseURL: srv.URL, tokenCache: "test-token", tokenExpAt: time.Now().Add(time.Hour)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	streamID, err := adapter.StartStream(ctx, &im.IncomingMessage{UserID: "test-user"})
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	t.Cleanup(func() {
		feishuStreamsMu.Lock()
		delete(feishuStreams, cardID)
		feishuStreamsMu.Unlock()
	})

	// Simulate a seven-minute QA run, including a full-output task that sends
	// no progress frames. The real StartStream must retain the owner's lifetime.
	feishuStreamsMu.Lock()
	feishuStreams[streamID].lastActivityAt = time.Now().Add(-7 * time.Minute)
	feishuStreamsMu.Unlock()
	reapOrphanStreams(time.Now())
	if err := adapter.UpdateStreamContent(ctx, nil, streamID, "progress"); err != nil {
		t.Fatalf("UpdateStreamContent after reaper: %v", err)
	}
	cancel()
	reapOrphanStreams(time.Now())
	if err := adapter.FinalizeStream(context.Background(), nil, streamID, "final answer"); err != nil {
		t.Fatalf("FinalizeStream after cancellation: %v", err)
	}
	if err := adapter.EndStream(context.Background(), nil, streamID); err != nil {
		t.Fatalf("EndStream: %v", err)
	}
	if finalContent != "final answer" {
		t.Fatalf("final content = %q", finalContent)
	}
	assertStreamingClosed(t, finalSettings, "final answer")
	if len(sequences) != 3 || sequences[0] != 1 || sequences[1] != 2 || sequences[2] != 3 {
		t.Fatalf("unexpected CardKit sequences: %v", sequences)
	}
	if hasTestStream(streamID) {
		t.Fatal("completed stream was not removed")
	}
}

func TestEndStreamFailurePreservesSequenceForRetry(t *testing.T) {
	useTestHTTPClient(t)
	const cardID = "retry-end-card"
	var sequences []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Sequence int `json:"sequence"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		sequences = append(sequences, body.Sequence)
		if len(sequences) == 1 {
			_, _ = w.Write([]byte(`{"code":300317,"msg":"sequence rejected"}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer srv.Close()
	adapter := &Adapter{region: testRegion(srv.URL), apiBaseURL: srv.URL, tokenCache: "test-token", tokenExpAt: time.Now().Add(time.Hour)}
	registerTestStream(t, cardID, &feishuStreamState{seq: 42})
	if err := adapter.EndStream(context.Background(), nil, cardID); err == nil {
		t.Fatal("failed CardKit finalization reported success")
	}
	if !hasTestStream(cardID) {
		t.Fatal("failed finalization discarded sequence state")
	}
	if err := adapter.EndStream(context.Background(), nil, cardID); err != nil {
		t.Fatalf("retry EndStream: %v", err)
	}
	if len(sequences) != 2 || sequences[0] != 43 || sequences[1] != 44 {
		t.Fatalf("retry lost the sequence: %v", sequences)
	}
	if hasTestStream(cardID) {
		t.Fatal("successful retry retained stream state")
	}
}

func TestStreamReaperConcurrentActivity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registerTestStream(t, "concurrent", &feishuStreamState{ownerDone: ctx.Done()})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				lookupStream("concurrent")
				reapOrphanStreams(time.Now())
			}
		})
	}
	wg.Wait()
	if !hasTestStream("concurrent") {
		t.Fatal("concurrent activity lost live stream")
	}
}

func TestSendReplySplitsLongTextWithoutLoss(t *testing.T) {
	useTestHTTPClient(t)
	var texts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/im/v1/messages/original-message/reply" {
			t.Errorf("reply lost original message: %s", r.URL.Path)
		}
		var payload struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		var content struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(payload.Content), &content); err != nil {
			t.Errorf("decode content: %v", err)
		}
		texts = append(texts, content.Text)
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer srv.Close()
	adapter := &Adapter{region: testRegion(srv.URL), apiBaseURL: srv.URL, tokenCache: "test-token", tokenExpAt: time.Now().Add(time.Hour)}
	answer := strings.Repeat("中文答案 ✅\n", 1500)
	if err := adapter.SendReply(context.Background(), &im.IncomingMessage{MessageID: "original-message"},
		&im.ReplyMessage{Content: answer, IsFinal: true}); err != nil {
		t.Fatalf("SendReply: %v", err)
	}
	if len(texts) < 2 || strings.Join(texts, "") != answer {
		t.Fatal("split reply lost or duplicated answer text")
	}
	for _, text := range texts {
		if len(text) > textReplyChunkBytes || !utf8.ValidString(text) {
			t.Fatal("invalid or oversized UTF-8 reply")
		}
	}
}

func TestSplitTextReplyPreservesBoundaries(t *testing.T) {
	for _, text := range []string{"", "short", strings.Repeat("x", textReplyChunkBytes),
		strings.Repeat("图", textReplyChunkBytes), strings.Repeat("\x00", textReplyChunkBytes+1)} {
		parts := splitTextReply(text)
		if strings.Join(parts, "") != text {
			t.Fatal("text changed when splitting")
		}
		for _, part := range parts {
			if !utf8.ValidString(part) || len(part) > textReplyChunkBytes {
				t.Fatal("invalid text chunk")
			}
		}
	}
}
