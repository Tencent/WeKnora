package service

import (
	"github.com/Tencent/WeKnora/internal/types"
	"testing"
)

func TestSummaryRejectsTruncatedResponse(t *testing.T) {
	for _, reason := range []string{"length", types.FinishReasonIncomplete} {
		_, err := validateSummaryOutput(&types.ChatResponse{Content: `{"summary":"未结束`, FinishReason: reason})
		if err == nil {
			t.Fatal("accepted incomplete summary", reason)
		}
	}
	text, err := validateSummaryOutput(&types.ChatResponse{Content: "完整摘要", FinishReason: "stop"})
	if err != nil || text != "完整摘要" {
		t.Fatal(text, err)
	}
}
