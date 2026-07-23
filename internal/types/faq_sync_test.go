package types

import "testing"

func TestFAQChunkNeedsStatusSync(t *testing.T) {
	src := &FAQChunkStatus{TagID: "a", IsEnabled: true, Flags: ChunkFlagRecommended, AnswerStrategy: AnswerStrategyAll}
	dst := &FAQChunkStatus{TagID: "old", IsEnabled: true, Flags: ChunkFlagRecommended, AnswerStrategy: AnswerStrategyAll}
	if !FAQChunkNeedsStatusSync(src, dst, "mapped") {
		t.Fatal("tag mismatch should need sync")
	}
	dst.TagID = "mapped"
	if FAQChunkNeedsStatusSync(src, dst, "mapped") {
		t.Fatal("in sync after tag mapped")
	}
	dst.IsEnabled = false
	if !FAQChunkNeedsStatusSync(src, dst, "mapped") {
		t.Fatal("is_enabled mismatch should need sync")
	}
}
