package im

import "testing"

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
