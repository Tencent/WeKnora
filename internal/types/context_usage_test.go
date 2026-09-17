package types

import "testing"

func TestContextUsageCalibrateScalesBucketsToPromptTokens(t *testing.T) {
	u := ContextUsage{
		SystemPrompt: 10,
		Tools:        20,
		Conversation: 60,
		MCP:          5,
		Skills:       5,
	}
	u.Calibrate(200)
	if u.Total != 200 {
		t.Fatalf("total: got %d want 200", u.Total)
	}
	if u.SystemPrompt+u.Tools+u.Conversation+u.MCP+u.Skills != 200 {
		t.Fatalf("buckets must sum to the calibrated total: %+v", u)
	}
	if u.Conversation < u.SystemPrompt || u.Conversation < u.Tools {
		t.Fatalf("conversation should remain the largest bucket: %+v", u)
	}
}

func TestContextUsageCalibrateNoopsWithoutPromptTokens(t *testing.T) {
	u := ContextUsage{SystemPrompt: 10, Conversation: 90}
	u.Calibrate(0)
	if u.Total != 100 || u.SystemPrompt != 10 || u.Conversation != 90 {
		t.Fatalf("zero prompt tokens must keep the estimate: %+v", u)
	}
}
