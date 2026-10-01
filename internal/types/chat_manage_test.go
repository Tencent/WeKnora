package types

import "testing"

func TestChatManageNeedsRetrievalByIntent(t *testing.T) {
	tests := []struct {
		name             string
		intent           QueryIntent
		webSearchEnabled bool
		want             bool
	}{
		{name: "empty intent fails safe", want: true},
		{name: "knowledge base search", intent: IntentKBSearch, want: true},
		{name: "clarification", intent: IntentClarification, want: true},
		{name: "conversation summary", intent: IntentSummarize, want: false},
		{name: "follow up", intent: IntentFollowUp, want: false},
		{name: "greeting", intent: IntentGreeting, want: false},
		{name: "chitchat", intent: IntentChitchat, want: false},
		{name: "image only", intent: IntentImageOnly, want: false},
		{name: "document only", intent: IntentDocOnly, want: false},
		{name: "web search disabled", intent: IntentWebSearch, want: false},
		{name: "web search enabled", intent: IntentWebSearch, webSearchEnabled: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chatManage := &ChatManage{
				PipelineRequest: PipelineRequest{WebSearchEnabled: tt.webSearchEnabled},
				PipelineState:   PipelineState{Intent: tt.intent},
			}
			if got := chatManage.NeedsRetrieval(); got != tt.want {
				t.Fatalf("NeedsRetrieval() = %v, want %v", got, tt.want)
			}
		})
	}
}
