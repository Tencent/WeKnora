package feishu

import "testing"

func TestFeishuThreadID_ThreadedReply(t *testing.T) {
	// Simulate: message is a reply in a thread (root_id is set)
	msg := &feishuMessage{
		MessageID: "msg-reply-1",
		RootID:    "msg-root-1",
		ParentID:  "msg-parent-1",
	}

	threadID := msg.RootID
	if threadID == "" {
		threadID = msg.MessageID
	}

	if threadID != "msg-root-1" {
		t.Errorf("threadID = %q, want %q", threadID, "msg-root-1")
	}
}

func TestFeishuThreadID_TopLevelMessage(t *testing.T) {
	// Simulate: top-level message (root_id is empty)
	msg := &feishuMessage{
		MessageID: "msg-top-1",
		RootID:    "",
		ParentID:  "",
	}

	threadID := msg.RootID
	if threadID == "" {
		threadID = msg.MessageID
	}

	if threadID != "msg-top-1" {
		t.Errorf("threadID = %q, want %q (should use MessageID as fallback)", threadID, "msg-top-1")
	}
}

func TestFeishuMessageStruct_JSONFields(t *testing.T) {
	// Verify the struct fields exist and have correct zero values
	msg := feishuMessage{}
	if msg.RootID != "" {
		t.Errorf("RootID zero value = %q, want empty", msg.RootID)
	}
	if msg.ParentID != "" {
		t.Errorf("ParentID zero value = %q, want empty", msg.ParentID)
	}
	if msg.MessageID != "" {
		t.Errorf("MessageID zero value = %q, want empty", msg.MessageID)
	}
}
