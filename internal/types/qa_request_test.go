package types

import (
	"strings"
	"testing"
)

func TestSteerMessageContentKeepsContinuationHintSmall(t *testing.T) {
	content := strings.Repeat("补充说明\n", 1000)
	wrapped := SteerMessageContent(content)
	if strings.Count(wrapped, content) != 1 {
		t.Fatal("user update must appear exactly once")
	}
	if len(wrapped)-len(content) > 210 {
		t.Fatal("steering delivery overhead exceeded 210 bytes")
	}
	if !strings.Contains(wrapped, "guidance for the task in progress") ||
		!strings.Contains(wrapped, "explicitly changes or cancels") {
		t.Fatal("steering must preserve the task while respecting explicit redirection")
	}
	for _, marker := range []string{"<steer_message>", "</steer_message>", "<continue_task>", "</continue_task>"} {
		if strings.Count(wrapped, marker) != 1 {
			t.Fatalf("expected a single %s", marker)
		}
	}
}
