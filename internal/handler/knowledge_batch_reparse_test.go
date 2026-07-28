package handler

import (
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestNormalizeBatchReparseIDs(t *testing.T) {
	got := normalizeBatchReparseIDs([]string{" one ", "", "two", "one", "  ", "two", "three"})
	want := []string{"one", "two", "three"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestSplitBatchReparseIDsKeepsTaskLimit(t *testing.T) {
	ids := make([]string, batchReparseTaskSize*2+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("knowledge-%d", i)
	}

	batches := splitBatchReparseIDs(ids)
	if len(batches) != 3 {
		t.Fatalf("got %d batches, want 3", len(batches))
	}
	if len(batches[0]) != batchReparseTaskSize || len(batches[1]) != batchReparseTaskSize || len(batches[2]) != 1 {
		t.Fatalf("unexpected batch sizes: %d, %d, %d", len(batches[0]), len(batches[1]), len(batches[2]))
	}
	if batches[0][0] != ids[0] || batches[1][0] != ids[batchReparseTaskSize] || batches[2][0] != ids[batchReparseTaskSize*2] {
		t.Fatal("batch order was not preserved")
	}
}

func TestIsKnowledgeReparseInFlight(t *testing.T) {
	for _, status := range []string{types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing} {
		if !isKnowledgeReparseInFlight(status) {
			t.Fatalf("expected %q to be in flight", status)
		}
	}
	for _, status := range []string{types.ParseStatusFailed, types.ParseStatusCompleted, types.ParseStatusCancelled, types.ParseStatusDeleting, ""} {
		if isKnowledgeReparseInFlight(status) {
			t.Fatalf("expected %q not to be in flight", status)
		}
	}
}

func TestBatchReparseKnowledgeFilter(t *testing.T) {
	filter := &batchReparseKnowledgeFilter{
		TagIDs:      []string{" tag-a ", "tag-a", "tag-b"},
		Keyword:     "  invoice  ",
		ParseStatus: types.ParseStatusFailed,
		StartTime:   "2026-07-01",
		EndTime:     "2026-07-31 23:59:59",
	}
	got, err := filter.toKnowledgeListFilter()
	if err != nil {
		t.Fatalf("toKnowledgeListFilter() error = %v", err)
	}
	if got.Keyword != "invoice" || got.ParseStatus != types.ParseStatusFailed {
		t.Fatalf("unexpected filter: %#v", got)
	}
	if len(got.TagIDs) != 2 || got.TagIDs[0] != "tag-a" || got.TagIDs[1] != "tag-b" {
		t.Fatalf("unexpected tag IDs: %v", got.TagIDs)
	}
	if got.UpdatedFrom.IsZero() || got.UpdatedTo.IsZero() {
		t.Fatalf("expected parsed timestamps, got %#v", got)
	}
	if isEmptyKnowledgeListFilter(got) {
		t.Fatal("expected populated filter not to be empty")
	}
	empty, err := (&batchReparseKnowledgeFilter{TagIDs: []string{" "}}).toKnowledgeListFilter()
	if err != nil {
		t.Fatalf("toKnowledgeListFilter() error = %v", err)
	}
	if !isEmptyKnowledgeListFilter(empty) {
		t.Fatal("expected whitespace-only filter to be empty")
	}
}
