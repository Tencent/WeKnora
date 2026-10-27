package types

import "testing"

func TestQueryIntentNeedsKBRetrieval(t *testing.T) {
	tests := []struct {
		name   string
		intent QueryIntent
		want   bool
	}{
		{name: "knowledge base search", intent: IntentKBSearch, want: true},
		{name: "clarification", intent: IntentClarification, want: true},
		{name: "summarize", intent: IntentSummarize, want: false},
		{name: "web search is handled by ChatManage", intent: IntentWebSearch, want: false},
		{name: "greeting", intent: IntentGreeting, want: false},
		{name: "chitchat", intent: IntentChitchat, want: false},
		{name: "image only", intent: IntentImageOnly, want: false},
		{name: "document only", intent: IntentDocOnly, want: false},
		{name: "empty intent defaults to retrieval", intent: "", want: true},
		{name: "unknown intent", intent: QueryIntent("unknown"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.intent.NeedsKBRetrieval(); got != tt.want {
				t.Errorf("NeedsKBRetrieval() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestChatManageNeedsRetrieval(t *testing.T) {
	tests := []struct {
		name             string
		intent           QueryIntent
		webSearchEnabled bool
		want             bool
	}{
		{name: "knowledge base search", intent: IntentKBSearch, want: true},
		{name: "summarize", intent: IntentSummarize, want: false},
		{name: "web search enabled", intent: IntentWebSearch, webSearchEnabled: true, want: true},
		{name: "web search disabled", intent: IntentWebSearch, webSearchEnabled: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chat := &ChatManage{
				PipelineRequest: PipelineRequest{WebSearchEnabled: tt.webSearchEnabled},
				PipelineState:   PipelineState{Intent: tt.intent},
			}
			if got := chat.NeedsRetrieval(); got != tt.want {
				t.Errorf("NeedsRetrieval() = %t, want %t", got, tt.want)
			}
		})
	}
}
