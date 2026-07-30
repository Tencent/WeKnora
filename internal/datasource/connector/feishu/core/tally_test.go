package core

import (
	"strings"
	"testing"
)

func TestFetchTally_CountsAndSummary(t *testing.T) {
	tally := NewFetchTally(13)
	tally.Fetch()
	tally.Fetch()
	tally.Fetch()
	tally.Skip("mindnote")
	tally.Skip("mindnote")
	tally.Skip("slides")
	tally.Fail()

	if got := tally.Skipped(); got != 3 {
		t.Errorf("Skipped() = %d, want 3", got)
	}

	Summary := tally.Summary()
	for _, want := range []string{
		"discovered=13",
		"fetched=3",
		"failed=1",
		"skipped_unsupported=3",
		"mindnote:2",
		"slides:1",
	} {
		if !strings.Contains(Summary, want) {
			t.Errorf("Summary() = %q, missing %q", Summary, want)
		}
	}
}

func TestFetchTally_EmptyHasNoSkips(t *testing.T) {
	tally := NewFetchTally(0)
	if got := tally.Skipped(); got != 0 {
		t.Errorf("Skipped() = %d, want 0", got)
	}
	if !strings.Contains(tally.Summary(), "skipped_unsupported=0") {
		t.Errorf("Summary() = %q, want skipped_unsupported=0", tally.Summary())
	}
}
