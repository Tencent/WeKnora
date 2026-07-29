package service

import (
	"context"
	"errors"
	"testing"
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
