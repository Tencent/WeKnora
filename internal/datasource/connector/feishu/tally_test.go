package feishu

import (
	"strings"
	"testing"
)

func TestFetchTally_CountsAndSummary(t *testing.T) {
	tally := newFetchTally(13)
	tally.fetch()
	tally.fetch()
	tally.fetch()
	tally.skip("mindnote")
	tally.skip("mindnote")
	tally.skip("slides")
	tally.fail()

	if got := tally.skipped(); got != 3 {
		t.Errorf("skipped() = %d, want 3", got)
	}

	summary := tally.summary()
	for _, want := range []string{
		"discovered=13",
		"fetched=3",
		"failed=1",
		"skipped_unsupported=3",
		"mindnote:2",
		"slides:1",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary() = %q, missing %q", summary, want)
		}
	}
}

func TestFetchTally_EmptyHasNoSkips(t *testing.T) {
	tally := newFetchTally(0)
	if got := tally.skipped(); got != 0 {
		t.Errorf("skipped() = %d, want 0", got)
	}
	if !strings.Contains(tally.summary(), "skipped_unsupported=0") {
		t.Errorf("summary() = %q, want skipped_unsupported=0", tally.summary())
	}
}
