package client

import (
	"encoding/json"
	"testing"
)

func TestCompletedTurnTokenAccounting(t *testing.T) {
	payload := []byte(`{
  "usage": {
    "prompt_tokens": 40,
    "completion_tokens": 6,
    "total_tokens": 46,
    "cache_read_tokens": 30,
    "cache_reported": true
  }
}`)
	var live StreamResponse
	var agent AgentStreamResponse
	var saved Message
	for _, v := range []any{&live, &agent, &saved} {
		if err := json.Unmarshal(payload, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []*TokenUsage{live.Usage, agent.Usage, saved.Usage} {
		if u == nil || u.TotalTokens != 46 || u.CacheReadTokens != 30 || !u.CacheReported {
			t.Fatalf("lost accounting: %+v", u)
		}
	}
}
