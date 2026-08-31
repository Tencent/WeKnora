package wecom

import (
	"encoding/json"
	"testing"
)

func TestNewStreamReplyBodyIncludesFeedbackOnFinalFrame(t *testing.T) {
	finalBody, err := json.Marshal(newStreamReplyBody("stream-1", "answer", true, "req-1"))
	if err != nil {
		t.Fatalf("marshal final stream body: %v", err)
	}

	var finalPayload map[string]any
	if err := json.Unmarshal(finalBody, &finalPayload); err != nil {
		t.Fatalf("unmarshal final stream body: %v", err)
	}
	feedback, ok := finalPayload["feedback"].(map[string]any)
	if !ok {
		t.Fatalf("final stream body feedback = %#v, want object", finalPayload["feedback"])
	}
	if feedback["id"] != "req-1" {
		t.Errorf("feedback.id = %#v, want %q", feedback["id"], "req-1")
	}

	partialBody, err := json.Marshal(newStreamReplyBody("stream-1", "answer", false, "req-1"))
	if err != nil {
		t.Fatalf("marshal partial stream body: %v", err)
	}
	var partialPayload map[string]any
	if err := json.Unmarshal(partialBody, &partialPayload); err != nil {
		t.Fatalf("unmarshal partial stream body: %v", err)
	}
	if _, ok := partialPayload["feedback"]; ok {
		t.Fatalf("partial stream body unexpectedly contains feedback: %#v", partialPayload["feedback"])
	}
}

func TestBotMessageFeedbackSupportsNestedAndTopLevelPayloads(t *testing.T) {
	nested := botMessage{
		Event: botEvent{
			EventType:            "feedback_event",
			FeedbackID:           "req-nested",
			FeedbackType:         1,
			FeedbackContent:      "helpful",
			InaccurateReasonList: []int{2, 4},
		},
	}
	got := nested.feedback()
	if got == nil {
		t.Fatal("nested feedback() = nil")
	}
	if got.ID != "req-nested" || got.Type != 1 || got.Content != "helpful" {
		t.Fatalf("nested feedback() = %+v", got)
	}
	if len(got.InaccurateReasonList) != 2 || got.InaccurateReasonList[1] != 4 {
		t.Fatalf("nested reasons = %#v", got.InaccurateReasonList)
	}

	topLevel := botMessage{
		EventType:       "feedback_event",
		FeedbackID:      "req-top-level",
		FeedbackType:    0,
		FeedbackContent: "not useful",
	}
	got = topLevel.feedback()
	if got == nil || got.ID != "req-top-level" || got.Content != "not useful" {
		t.Fatalf("top-level feedback() = %+v", got)
	}

	directEvent := botMessage{MsgType: "feedback_event", FeedbackID: "req-direct"}
	if got := directEvent.eventType(); got != "feedback_event" {
		t.Fatalf("direct feedback event type = %q, want feedback_event", got)
	}
}
