package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestGetPassageListPreservesPassageIDAsIndex(t *testing.T) {
	passages := getPassageList([]*types.QAPair{
		{
			PIDs:     []int{3, 7},
			Passages: []string{"passage three", "passage seven"},
		},
	})

	if len(passages) != 8 {
		t.Fatalf("len(passages) = %d, want 8", len(passages))
	}
	if got := passages[3]; got != "passage three" {
		t.Errorf("passages[3] = %q, want %q", got, "passage three")
	}
	if got := passages[7]; got != "passage seven" {
		t.Errorf("passages[7] = %q, want %q", got, "passage seven")
	}
	if got := passages[0]; got != "" {
		t.Errorf("passages[0] = %q, want an empty gap", got)
	}
}
