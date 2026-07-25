package router

import (
	"errors"
	"fmt"
	"testing"

	"github.com/hibiken/asynq"
)

func TestIsAsynqQueueNotFound(t *testing.T) {
	internalQueueNotFound := errors.New(`NOT_FOUND: queue "default" does not exist`)
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "public sentinel", err: asynq.ErrQueueNotFound, want: true},
		{name: "wrapped public sentinel", err: fmt.Errorf("inspect queue: %w", asynq.ErrQueueNotFound), want: true},
		{name: "internal queue error", err: internalQueueNotFound, want: true},
		{name: "wrapped internal queue error", err: fmt.Errorf("inspect queue: %w", internalQueueNotFound), want: true},
		{name: "task not found", err: errors.New(`NOT_FOUND: task "default" does not exist`), want: false},
		{name: "different code", err: errors.New(`UNKNOWN: queue "default" does not exist`), want: false},
		{name: "different reason", err: errors.New(`NOT_FOUND: queue "default" is unavailable`), want: false},
		{name: "empty queue name", err: errors.New(`NOT_FOUND: queue "" does not exist`), want: false},
		{name: "trailing context", err: errors.New(`NOT_FOUND: queue "default" does not exist: retry later`), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAsynqQueueNotFound(tt.err); got != tt.want {
				t.Fatalf("isAsynqQueueNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
