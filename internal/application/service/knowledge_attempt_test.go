package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type knowledgeAttemptOpenerStub struct {
	root    *Span
	attempt int
	err     error
}

func (s knowledgeAttemptOpenerStub) OpenAttempt(
	context.Context, string, string,
) (*Span, int, error) {
	return s.root, s.attempt, s.err
}

func TestOpenRequiredKnowledgeAttempt(t *testing.T) {
	t.Run("returns durable generation", func(t *testing.T) {
		attempt, err := openRequiredKnowledgeAttempt(
			context.Background(),
			knowledgeAttemptOpenerStub{root: &Span{KnowledgeID: "knowledge-1", Attempt: 3}, attempt: 3},
			"knowledge-1", "trace-1",
		)
		if err != nil || attempt != 3 {
			t.Fatalf("openRequiredKnowledgeAttempt() = (%d, %v), want (3, nil)", attempt, err)
		}
	})

	t.Run("propagates repository failure", func(t *testing.T) {
		wantErr := errors.New("database unavailable")
		attempt, err := openRequiredKnowledgeAttempt(
			context.Background(), knowledgeAttemptOpenerStub{err: wantErr}, "knowledge-1", "",
		)
		if attempt != 0 || !errors.Is(err, wantErr) {
			t.Fatalf("openRequiredKnowledgeAttempt() = (%d, %v), want wrapped %v", attempt, err, wantErr)
		}
	})

	for _, test := range []struct {
		name   string
		opener knowledgeAttemptOpenerStub
	}{
		{name: "rejects nil root", opener: knowledgeAttemptOpenerStub{attempt: 1}},
		{name: "rejects zero attempt", opener: knowledgeAttemptOpenerStub{root: &Span{KnowledgeID: "knowledge-1"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempt, err := openRequiredKnowledgeAttempt(
				context.Background(), test.opener, "knowledge-1", "",
			)
			if attempt != 0 || err == nil {
				t.Fatalf("openRequiredKnowledgeAttempt() = (%d, %v), want (0, error)", attempt, err)
			}
		})
	}
}

type knowledgeAttemptLookupStub struct {
	SpanTracker
	latest int
	err    error
}

func (s knowledgeAttemptLookupStub) LatestAttemptWithError(context.Context, string) (int, error) {
	return s.latest, s.err
}

type workerAttemptTrackerStub struct {
	SpanTracker
	latest     int
	latestErr  error
	openResult int
	openErr    error
	openCalls  int
}

func (s *workerAttemptTrackerStub) LatestAttemptWithError(context.Context, string) (int, error) {
	return s.latest, s.latestErr
}

func (s *workerAttemptTrackerStub) OpenAttempt(
	context.Context, string, string,
) (*Span, int, error) {
	s.openCalls++
	if s.openErr != nil {
		return nil, 0, s.openErr
	}
	return &Span{KnowledgeID: "knowledge-1", Attempt: s.openResult}, s.openResult, nil
}

func TestRequireCurrentKnowledgeAttempt(t *testing.T) {
	t.Run("accepts exact latest attempt", func(t *testing.T) {
		svc := &knowledgeService{spanTracker: knowledgeAttemptLookupStub{latest: 4}}
		current, err := svc.requireCurrentKnowledgeAttempt(context.Background(), "knowledge-1", 4)
		if err != nil || !current {
			t.Fatalf("requireCurrentKnowledgeAttempt() = (%v, %v), want (true, nil)", current, err)
		}
	})

	t.Run("rejects stale and unpersisted attempts", func(t *testing.T) {
		svc := &knowledgeService{spanTracker: knowledgeAttemptLookupStub{latest: 4}}
		for _, attempt := range []int{3, 5} {
			current, err := svc.requireCurrentKnowledgeAttempt(context.Background(), "knowledge-1", attempt)
			if err != nil || current {
				t.Fatalf("attempt %d = (%v, %v), want (false, nil)", attempt, current, err)
			}
		}
	})

	t.Run("fails closed on lookup error", func(t *testing.T) {
		wantErr := errors.New("database unavailable")
		svc := &knowledgeService{spanTracker: knowledgeAttemptLookupStub{latest: 4, err: wantErr}}
		current, err := svc.requireCurrentKnowledgeAttempt(context.Background(), "knowledge-1", 4)
		if current || !errors.Is(err, wantErr) {
			t.Fatalf("requireCurrentKnowledgeAttempt() = (%v, %v), want (false, wrapped error)", current, err)
		}
	})

	t.Run("keeps attempt zero compatibility for disabled tracking", func(t *testing.T) {
		svc := &knowledgeService{spanTracker: knowledgeAttemptLookupStub{err: errors.New("must not be called")}}
		current, err := svc.requireCurrentKnowledgeAttempt(context.Background(), "knowledge-1", 0)
		if err != nil || !current {
			t.Fatalf("requireCurrentKnowledgeAttempt() = (%v, %v), want (true, nil)", current, err)
		}
	})
}

func TestRequireCarriedKnowledgeAttempt(t *testing.T) {
	tests := []struct {
		name    string
		latest  int
		carried int
		want    bool
	}{
		{name: "accepts exact generation", latest: 4, carried: 4, want: true},
		{name: "rejects stale generation", latest: 4, carried: 3},
		{name: "rejects future unpersisted generation", latest: 4, carried: 5},
		{name: "accepts legacy only before tracking", latest: 0, carried: 0, want: true},
		{name: "rejects legacy after tracking starts", latest: 1, carried: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current, err := requireCarriedKnowledgeAttempt(
				context.Background(), knowledgeAttemptLookupStub{latest: test.latest},
				"knowledge-1", test.carried,
			)
			if err != nil || current != test.want {
				t.Fatalf("requireCarriedKnowledgeAttempt() = (%v, %v), want (%v, nil)", current, err, test.want)
			}
		})
	}

	wantErr := errors.New("database unavailable")
	current, err := requireCarriedKnowledgeAttempt(
		context.Background(), knowledgeAttemptLookupStub{err: wantErr},
		"knowledge-1", 2,
	)
	if current || !errors.Is(err, wantErr) {
		t.Fatalf("lookup failure = (%v, %v), want (false, wrapped %v)", current, err, wantErr)
	}
}

func TestResolveKnowledgeWorkerAttempt(t *testing.T) {
	t.Run("keeps attempt carried by a new task", func(t *testing.T) {
		tracker := &workerAttemptTrackerStub{latestErr: errors.New("must not be queried")}
		svc := &knowledgeService{spanTracker: tracker}
		attempt, accepted, err := svc.resolveKnowledgeWorkerAttempt(
			context.Background(), "knowledge-1", "", 8,
		)
		if err != nil || !accepted || attempt != 8 || tracker.openCalls != 0 {
			t.Fatalf("resolve = (%d, %v, %v), openCalls=%d", attempt, accepted, err, tracker.openCalls)
		}
	})

	t.Run("drops legacy task after a generation exists", func(t *testing.T) {
		tracker := &workerAttemptTrackerStub{latest: 4, openResult: 5}
		svc := &knowledgeService{spanTracker: tracker}
		attempt, accepted, err := svc.resolveKnowledgeWorkerAttempt(
			context.Background(), "knowledge-1", "", 0,
		)
		if err != nil || accepted || attempt != 0 || tracker.openCalls != 0 {
			t.Fatalf("resolve = (%d, %v, %v), openCalls=%d", attempt, accepted, err, tracker.openCalls)
		}
	})

	t.Run("opens first generation for a pre-upgrade task", func(t *testing.T) {
		tracker := &workerAttemptTrackerStub{openResult: 1}
		svc := &knowledgeService{spanTracker: tracker}
		attempt, accepted, err := svc.resolveKnowledgeWorkerAttempt(
			context.Background(), "knowledge-1", "trace-1", 0,
		)
		if err != nil || !accepted || attempt != 1 || tracker.openCalls != 1 {
			t.Fatalf("resolve = (%d, %v, %v), openCalls=%d", attempt, accepted, err, tracker.openCalls)
		}
	})

	t.Run("fails closed when latest generation cannot be read", func(t *testing.T) {
		wantErr := errors.New("database unavailable")
		tracker := &workerAttemptTrackerStub{latestErr: wantErr}
		svc := &knowledgeService{spanTracker: tracker}
		attempt, accepted, err := svc.resolveKnowledgeWorkerAttempt(
			context.Background(), "knowledge-1", "", 0,
		)
		if attempt != 0 || accepted || !errors.Is(err, wantErr) {
			t.Fatalf("resolve = (%d, %v, %v), want (0, false, wrapped error)", attempt, accepted, err)
		}
	})
}

func TestKnowledgeMutableColumnsDoesNotWriteObservedUpdatedAt(t *testing.T) {
	columns := knowledgeMutableColumns(&types.Knowledge{UpdatedAt: time.Now()})
	if _, exists := columns["updated_at"]; exists {
		t.Fatal("knowledgeMutableColumns must let the database advance updated_at")
	}
}

func TestProcessingPayloadGenerationRoundTrip(t *testing.T) {
	documentIn := types.DocumentProcessPayload{
		KnowledgeID: "knowledge-1",
		NeedCleanup: true,
		Attempt:     8,
	}
	documentJSON, err := json.Marshal(documentIn)
	if err != nil {
		t.Fatal(err)
	}
	var documentKeys map[string]json.RawMessage
	if err := json.Unmarshal(documentJSON, &documentKeys); err != nil {
		t.Fatal(err)
	}
	if _, ok := documentKeys["need_cleanup"]; !ok {
		t.Fatalf("document JSON %s does not contain need_cleanup", documentJSON)
	}
	if _, ok := documentKeys["attempt"]; !ok {
		t.Fatalf("document JSON %s does not contain attempt", documentJSON)
	}
	var documentOut types.DocumentProcessPayload
	if err := json.Unmarshal(documentJSON, &documentOut); err != nil {
		t.Fatal(err)
	}
	if !documentOut.NeedCleanup || documentOut.Attempt != 8 {
		t.Fatalf("document payload = %+v, want cleanup=true attempt=8", documentOut)
	}

	manualIn := types.ManualProcessPayload{KnowledgeID: "knowledge-2", NeedCleanup: true, Attempt: 9}
	manualJSON, err := json.Marshal(manualIn)
	if err != nil {
		t.Fatal(err)
	}
	var manualOut types.ManualProcessPayload
	if err := json.Unmarshal(manualJSON, &manualOut); err != nil {
		t.Fatal(err)
	}
	if !manualOut.NeedCleanup || manualOut.Attempt != 9 {
		t.Fatalf("manual payload = %+v, want cleanup=true attempt=9", manualOut)
	}

	var legacy types.DocumentProcessPayload
	if err := json.Unmarshal([]byte(`{"knowledge_id":"legacy"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.NeedCleanup || legacy.Attempt != 0 {
		t.Fatalf("legacy payload defaults = %+v, want cleanup=false attempt=0", legacy)
	}
}
