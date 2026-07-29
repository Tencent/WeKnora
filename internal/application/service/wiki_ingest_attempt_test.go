package service

import (
	"context"
	"errors"
	"testing"
)

type wikiAttemptLookupTracker struct {
	SpanTracker
	latest      int
	err         error
	checkedCall int
	legacyCall  int
}

func (t *wikiAttemptLookupTracker) LatestAttempt(context.Context, string) int {
	t.legacyCall++
	return t.latest
}

func (t *wikiAttemptLookupTracker) LatestAttemptWithError(context.Context, string) (int, error) {
	t.checkedCall++
	return t.latest, t.err
}

func TestFilterCurrentWikiAttemptsFailsClosedOnLookupError(t *testing.T) {
	wantErr := errors.New("span repository unavailable")
	tracker := &wikiAttemptLookupTracker{latest: 0, err: wantErr}
	svc := &wikiIngestService{spanTracker: tracker}

	updates, err := svc.filterCurrentWikiAttempts(context.Background(), []SlugUpdate{{
		KnowledgeID:      "knowledge-1",
		KnowledgeAttempt: 5,
	}})

	if !errors.Is(err, wantErr) {
		t.Fatalf("filterCurrentWikiAttempts() error = %v, want %v", err, wantErr)
	}
	if updates != nil {
		t.Fatalf("filterCurrentWikiAttempts() updates = %+v, want nil on lookup failure", updates)
	}
	if tracker.checkedCall != 1 {
		t.Fatalf("checked LatestAttempt calls = %d, want 1", tracker.checkedCall)
	}
	if tracker.legacyCall != 0 {
		t.Fatalf("best-effort LatestAttempt called %d time(s), want 0", tracker.legacyCall)
	}
}

func TestFilterCurrentWikiAttemptsRequiresExactLatestAttempt(t *testing.T) {
	tracker := &wikiAttemptLookupTracker{latest: 7}
	svc := &wikiIngestService{spanTracker: tracker}

	updates, err := svc.filterCurrentWikiAttempts(context.Background(), []SlugUpdate{
		{KnowledgeID: "knowledge-1", KnowledgeAttempt: 6, DocTitle: "stale"},
		{KnowledgeID: "knowledge-1", KnowledgeAttempt: 7, DocTitle: "current"},
		{KnowledgeID: "knowledge-1", KnowledgeAttempt: 8, DocTitle: "unpersisted"},
		{KnowledgeID: "knowledge-1", KnowledgeAttempt: 0, DocTitle: "unguarded"},
		{Type: "retract", KnowledgeID: "", KnowledgeAttempt: 0, DocTitle: "explicit-retract"},
	})
	if err != nil {
		t.Fatalf("filterCurrentWikiAttempts() error = %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("filterCurrentWikiAttempts() returned %d updates, want 2: %+v", len(updates), updates)
	}
	if updates[0].DocTitle != "current" || updates[1].DocTitle != "explicit-retract" {
		t.Fatalf("filterCurrentWikiAttempts() updates = %+v", updates)
	}
	if tracker.checkedCall != 1 {
		t.Fatalf("checked LatestAttempt calls = %d, want one cached lookup", tracker.checkedCall)
	}
}
