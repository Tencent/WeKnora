package worker

import "testing"

func TestDurationSecondsFromFloatRoundsUpForChapterBoundaries(t *testing.T) {
	if got := durationSecondsFromFloat(386.2); got != 387 {
		t.Fatalf("durationSecondsFromFloat = %d, want 387", got)
	}
}
