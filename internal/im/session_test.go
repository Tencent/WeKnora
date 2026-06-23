package im

import (
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestMakeUserKey_UserMode(t *testing.T) {
	tests := []struct {
		name      string
		channelID string
		userID    string
		chatID    string
		threadID  string
		want      string
	}{
		{
			name:      "user mode with empty threadID",
			channelID: "ch-1",
			userID:    "user-1",
			chatID:    "chat-1",
			threadID:  "",
			want:      "ch-1:user-1:chat-1",
		},
		{
			name:      "user mode with empty chatID (DM)",
			channelID: "ch-1",
			userID:    "user-1",
			chatID:    "",
			threadID:  "",
			want:      "ch-1:user-1:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeUserKey(tt.channelID, tt.userID, tt.chatID, tt.threadID)
			if got != tt.want {
				t.Errorf("makeUserKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMakeUserKey_ThreadMode(t *testing.T) {
	tests := []struct {
		name      string
		channelID string
		userID    string
		chatID    string
		threadID  string
		want      string
	}{
		{
			name:      "thread mode with Slack thread_ts",
			channelID: "ch-1",
			userID:    "user-1",
			chatID:    "chat-1",
			threadID:  "1234567890.123456",
			want:      "ch-1:user-1:chat-1:1234567890.123456",
		},
		{
			name:      "thread mode with Mattermost root_id",
			channelID: "ch-2",
			userID:    "user-2",
			chatID:    "chat-2",
			threadID:  "abc123def456",
			want:      "ch-2:user-2:chat-2:abc123def456",
		},
		{
			name:      "thread mode with Telegram topic ID",
			channelID: "ch-3",
			userID:    "user-3",
			chatID:    "chat-3",
			threadID:  "42",
			want:      "ch-3:user-3:chat-3:42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeUserKey(tt.channelID, tt.userID, tt.chatID, tt.threadID)
			if got != tt.want {
				t.Errorf("makeUserKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMakeUserKey_ThreadIDGuard(t *testing.T) {
	// Verify that the same user+chat produces different keys with different threadIDs
	keyA := makeUserKey("ch", "user", "chat", "thread-A")
	keyB := makeUserKey("ch", "user", "chat", "thread-B")
	keyNone := makeUserKey("ch", "user", "chat", "")

	if keyA == keyB {
		t.Error("different threadIDs should produce different keys")
	}
	if keyA == keyNone {
		t.Error("thread key should differ from non-thread key")
	}
	if keyB == keyNone {
		t.Error("thread key should differ from non-thread key")
	}
}

func TestMakeUserKey_SameThreadDifferentUsers(t *testing.T) {
	// In thread mode, different users in the same thread produce different keys
	// (this is intentional: /stop only cancels the caller's own request)
	keyUserA := makeUserKey("ch", "alice", "chat", "thread-1")
	keyUserB := makeUserKey("ch", "bob", "chat", "thread-1")

	if keyUserA == keyUserB {
		t.Error("different users in same thread should have different keys")
	}
}

func TestIncomingMessageThreadID(t *testing.T) {
	// Verify ThreadID field works correctly on IncomingMessage
	msg := &IncomingMessage{
		Platform:  PlatformSlack,
		UserID:    "U123",
		ChatID:    "C456",
		MessageID: "1234567890.123456",
		ThreadID:  "1234567890.123456",
	}

	if msg.ThreadID != msg.MessageID {
		t.Errorf("Slack ThreadID should equal MessageID for top-level, got ThreadID=%q MessageID=%q",
			msg.ThreadID, msg.MessageID)
	}

	// Mattermost: ThreadID from Extra
	msgMM := &IncomingMessage{
		Platform:  PlatformMattermost,
		UserID:    "user-1",
		ChatID:    "channel-1",
		MessageID: "post-123",
		ThreadID:  "root-456",
		Extra: map[string]string{
			"thread_root_id": "root-456",
		},
	}

	if msgMM.ThreadID != msgMM.Extra["thread_root_id"] {
		t.Error("Mattermost ThreadID should match Extra thread_root_id")
	}
}

func TestBuildIMLastRequestStateFromAgent(t *testing.T) {
	agent := &types.CustomAgent{
		ID: "agent-1",
		Config: types.CustomAgentConfig{
			AgentMode:        types.AgentModeSmartReasoning,
			ModelID:          "model-1",
			KnowledgeBases:   []string{"kb-agent"},
			WebSearchEnabled: true,
		},
	}

	state := buildIMLastRequestState(agent.ID, agent, nil)

	if state.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1", state.AgentID)
	}
	if !state.AgentEnabled {
		t.Fatal("AgentEnabled = false, want true")
	}
	if state.ModelID != "model-1" {
		t.Fatalf("ModelID = %q, want model-1", state.ModelID)
	}
	if !state.WebSearchEnabled {
		t.Fatal("WebSearchEnabled = false, want true")
	}
	if !reflect.DeepEqual(state.KnowledgeBaseIDs, []string{"kb-agent"}) {
		t.Fatalf("KnowledgeBaseIDs = %#v, want [kb-agent]", state.KnowledgeBaseIDs)
	}
}

func TestBuildIMLastRequestStateKeepsExplicitKBs(t *testing.T) {
	agent := &types.CustomAgent{
		ID: "agent-1",
		Config: types.CustomAgentConfig{
			AgentMode:      types.AgentModeQuickAnswer,
			ModelID:        "model-1",
			KnowledgeBases: []string{"kb-agent"},
		},
	}

	state := buildIMLastRequestState(agent.ID, agent, []string{"kb-explicit"})

	if state.AgentEnabled {
		t.Fatal("AgentEnabled = true, want false for quick-answer agent")
	}
	if !reflect.DeepEqual(state.KnowledgeBaseIDs, []string{"kb-explicit"}) {
		t.Fatalf("KnowledgeBaseIDs = %#v, want [kb-explicit]", state.KnowledgeBaseIDs)
	}
}

func TestCreateIMMessagePayloadsShareRequestShape(t *testing.T) {
	userMsg := createIMUserMessagePayload("session-1", "hello", "request-1")
	assistantMsg := createIMAssistantMessagePayload("session-1", "request-1")

	if userMsg.SessionID != "session-1" || assistantMsg.SessionID != "session-1" {
		t.Fatalf("SessionID mismatch: user=%q assistant=%q", userMsg.SessionID, assistantMsg.SessionID)
	}
	if userMsg.RequestID != assistantMsg.RequestID || userMsg.RequestID != "request-1" {
		t.Fatalf("RequestID mismatch: user=%q assistant=%q", userMsg.RequestID, assistantMsg.RequestID)
	}
	if userMsg.Role != "user" || assistantMsg.Role != "assistant" {
		t.Fatalf("Role mismatch: user=%q assistant=%q", userMsg.Role, assistantMsg.Role)
	}
	if userMsg.Channel != "im" || assistantMsg.Channel != "im" {
		t.Fatalf("Channel mismatch: user=%q assistant=%q", userMsg.Channel, assistantMsg.Channel)
	}
	if !userMsg.IsCompleted {
		t.Fatal("user message should be completed")
	}
	if assistantMsg.IsCompleted {
		t.Fatal("assistant placeholder should not be completed")
	}
	if userMsg.Content != "hello" {
		t.Fatalf("user content = %q, want hello", userMsg.Content)
	}
	if assistantMsg.Content != "" {
		t.Fatalf("assistant placeholder content = %q, want empty", assistantMsg.Content)
	}
}

func TestApplyIMCompleteDataToMessage(t *testing.T) {
	msg := &types.Message{ID: "assistant-1", Role: "assistant"}
	ref := &types.SearchResult{ID: "chunk-1", KnowledgeID: "knowledge-1"}
	steps := []types.AgentStep{{
		Iteration:        1,
		ReasoningContent: "thinking",
	}}

	applyIMCompleteDataToMessage(msg, event.AgentCompleteData{
		MessageID:       "assistant-1",
		TotalDurationMs: 1234,
		KnowledgeRefs:   []interface{}{ref},
		AgentSteps:      steps,
	})

	if !msg.IsCompleted {
		t.Fatal("message should be marked completed")
	}
	if msg.AgentDurationMs != 1234 {
		t.Fatalf("AgentDurationMs = %d, want 1234", msg.AgentDurationMs)
	}
	if len(msg.KnowledgeReferences) != 1 || msg.KnowledgeReferences[0].ID != "chunk-1" {
		t.Fatalf("KnowledgeReferences = %#v, want chunk-1", msg.KnowledgeReferences)
	}
	if len(msg.AgentSteps) != 1 || msg.AgentSteps[0].ReasoningContent != "thinking" {
		t.Fatalf("AgentSteps = %#v, want one thinking step", msg.AgentSteps)
	}
}
