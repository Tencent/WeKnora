package invoketest

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

func TestSmokeStream(t *testing.T) {
	f := New(t)
	f.EnqueueStream(
		invoke.StreamEvent{Kind: invoke.StreamKindAnswer, Delta: &invoke.ContentDelta{Text: "hi"}},
		invoke.StreamEvent{Kind: invoke.StreamKindAnswer, Done: &invoke.FinishInfo{FinishReason: "stop"}},
	)
	ch, err := invoke.ChatStream(context.Background(), f.Config(), &invoke.ChatOptions{
		Messages: []invoke.Message{TextMessageForTest("user", "q")},
		Stream:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for c := range ch {
		t.Logf("chunk: %+v", c)
	}
}

func TextMessageForTest(role invoke.Role, text string) invoke.Message {
	return invoke.TextMessage(role, text)
}
