package langfuse

import "testing"

func TestARecallSpanKeepsTheMetaItWasGiven(t *testing.T) {
	meta := map[string]interface{}{
		"outcome":       "ok",
		"episode_count": 1,
		"ranking_mode":  "lexical_only",
	}
	out := SummarizeMemoryRecallOutput(meta)

	if out["outcome"] != "ok" || out["episode_count"] != 1 || out["ranking_mode"] != "lexical_only" {
		t.Fatalf("meta not preserved: %#v", out)
	}
	// A span's recorded output must not change after it was finished, so the
	// caller's map cannot be the one that got stored.
	meta["outcome"] = "disabled"
	if out["outcome"] != "ok" {
		t.Fatalf("recorded output followed the caller's map: %#v", out)
	}
}

func TestRetrievalConditioningOutputOmitsAnEmptyBackground(t *testing.T) {
	out := SummarizeRetrievalContextOutput("", nil)
	if _, ok := out["background"]; ok {
		t.Fatalf("an empty background must not be recorded: %#v", out)
	}
	if out["interest_count"] != 0 {
		t.Fatalf("interest count must always be reported: %#v", out)
	}
}
