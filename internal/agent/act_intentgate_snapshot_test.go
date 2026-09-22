package agent

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
)

// TestNewGateIntentSnapshotFirstUserPrompt：意图基准的 user prompt 取
// 全量历史中第一条 user message（不是当前轮 query，也不是窗口内第一条）。
func TestNewGateIntentSnapshotFirstUserPrompt(t *testing.T) {
	messages := []chat.Message{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: "最早的原始请求"},
		{Role: "assistant", Content: "收到"},
		{Role: "user", Content: "后来的追问"},
	}
	snap := newGateIntentSnapshot("当前轮 query", messages)
	if snap.userPrompt != "最早的原始请求" {
		t.Fatalf("userPrompt = %q, want 第一条 user message", snap.userPrompt)
	}
}

// TestNewGateIntentSnapshotFallsBackToQuery：历史中找不到 user message
// 时回退当前轮 query（如首轮即触发工具调用且 history 未含 user 消息）。
func TestNewGateIntentSnapshotFallsBackToQuery(t *testing.T) {
	snap := newGateIntentSnapshot("当前轮 query", []chat.Message{
		{Role: "system", Content: "你是助手"},
	})
	if snap.userPrompt != "当前轮 query" {
		t.Fatalf("userPrompt = %q, want query 回退", snap.userPrompt)
	}
}

// TestNewGateIntentSnapshotHistoryWindow：历史窗口只取最近
// intentGateHistoryWindow 条（旧→新），且不含被审对象——Act 前快照时
// 当前轮 assistant 输出尚未 append，构造器收到的 messages 即全部历史。
func TestNewGateIntentSnapshotHistoryWindow(t *testing.T) {
	var messages []chat.Message
	for i := 0; i < intentGateHistoryWindow+5; i++ {
		messages = append(messages, chat.Message{
			Role: "user", Content: strings.Repeat("m", 3) + string(rune('a'+i%26)),
		})
	}
	snap := newGateIntentSnapshot("q", messages)
	if len(snap.history) != intentGateHistoryWindow {
		t.Fatalf("history len = %d, want %d", len(snap.history), intentGateHistoryWindow)
	}
	if snap.history[0].Content != messages[len(messages)-intentGateHistoryWindow].Content {
		t.Fatal("窗口应从倒数第 N 条开始（旧→新）")
	}
	if snap.history[len(snap.history)-1].Content != messages[len(messages)-1].Content {
		t.Fatal("窗口最后一条应是最近一条消息")
	}
}
