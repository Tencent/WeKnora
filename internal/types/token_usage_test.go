package types

import (
	"encoding/json"
	"testing"
)

func TestTokenUsageAccumulateSumsEveryCounter(t *testing.T) {
	var turn TokenUsage

	first := TokenUsage{PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200}
	first.SetPromptCacheUsage(800, 0, 200, true)
	second := TokenUsage{PromptTokens: 1500, CompletionTokens: 300, TotalTokens: 1800}
	second.SetPromptCacheUsage(0, 1500, 1500, true)

	turn.Accumulate(first)
	turn.Accumulate(second)

	if turn.PromptTokens != 2500 || turn.CompletionTokens != 500 || turn.TotalTokens != 3000 {
		t.Fatalf("token sums wrong: %+v", turn)
	}
	if turn.CacheReadTokens != 800 || turn.CacheWriteTokens != 1500 || turn.CacheMissTokens != 1700 {
		t.Fatalf("cache sums wrong: %+v", turn)
	}
	if turn.CachedTokens != turn.CacheReadTokens {
		t.Fatalf("legacy alias diverged from cache reads: %+v", turn)
	}
	if !turn.CacheReported || turn.CacheStatus != PromptCacheStatusHit {
		t.Fatalf("a hit anywhere in the turn must read as a hit: %+v", turn)
	}
}

func TestTokenUsageAccumulateStatusFollowsCombinedCounters(t *testing.T) {
	var unreported TokenUsage
	unreported.Accumulate(TokenUsage{PromptTokens: 10, TotalTokens: 10})
	if unreported.CacheReported || unreported.CacheStatus != PromptCacheStatusUnreported {
		t.Fatalf("all-unreported accumulation must stay unreported: %+v", unreported)
	}

	var missOnly TokenUsage
	reportedMiss := TokenUsage{PromptTokens: 10, TotalTokens: 10}
	reportedMiss.SetPromptCacheUsage(0, 0, 10, true)
	missOnly.Accumulate(reportedMiss)
	if missOnly.CacheStatus != PromptCacheStatusMiss {
		t.Fatalf("reported without reads must read as miss: %+v", missOnly)
	}
}

func TestTokenUsageAccumulatePreservesUnsupported(t *testing.T) {
	var unsupported TokenUsage
	call := TokenUsage{PromptTokens: 10, TotalTokens: 10}
	call.MarkPromptCacheUnsupported()

	unsupported.Accumulate(call)
	unsupported.Accumulate(call)
	if unsupported.CacheStatus != PromptCacheStatusUnsupported {
		t.Fatalf("all-unsupported accumulation must stay unsupported: %+v", unsupported)
	}

	// Mixing in a merely-unreported call degrades the aggregate: the turn no
	// longer proves every provider path was incapable of reporting.
	unsupported.Accumulate(TokenUsage{PromptTokens: 5, TotalTokens: 5})
	if unsupported.CacheStatus != PromptCacheStatusUnreported {
		t.Fatalf("unsupported+unreported mix must read unreported: %+v", unsupported)
	}
}

func TestTokenUsageAccumulateOnNilReceiverIsNoOp(t *testing.T) {
	var u *TokenUsage
	u.Accumulate(TokenUsage{PromptTokens: 1, TotalTokens: 1}) // must not panic
}

func TestTokenUsageValueScanRoundTrip(t *testing.T) {
	original := &TokenUsage{PromptTokens: 42, CompletionTokens: 7, TotalTokens: 49}
	original.SetPromptCacheUsage(30, 0, 12, true)

	value, err := original.Value()
	if err != nil {
		t.Fatalf("Value failed: %v", err)
	}
	raw, ok := value.([]byte)
	if !ok {
		t.Fatalf("Value must produce bytes, got %T", value)
	}

	var restored TokenUsage
	if err := restored.Scan(raw); err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if restored != *original {
		t.Fatalf("round trip diverged: got %+v want %+v", restored, *original)
	}

	// json.Marshal must agree with Value so API responses and the persisted
	// column carry the same shape.
	direct, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if string(direct) != string(raw) {
		t.Fatalf("Value diverged from json.Marshal: %s vs %s", raw, direct)
	}
}

func TestTokenUsageValueNilAndScanNull(t *testing.T) {
	var u *TokenUsage
	value, err := u.Value()
	if err != nil || value != nil {
		t.Fatalf("nil usage must persist as SQL NULL, got (%v, %v)", value, err)
	}

	restored := TokenUsage{PromptTokens: 5}
	if err := restored.Scan(nil); err != nil {
		t.Fatalf("Scan(NULL) failed: %v", err)
	}
	if restored.PromptTokens != 5 {
		t.Fatalf("Scan(NULL) must leave the receiver untouched: %+v", restored)
	}
}
