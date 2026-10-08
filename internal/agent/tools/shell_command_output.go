package tools

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/google/uuid"
)

// Shell tools publish this bounded, human-facing preview. It is not
// appended to the model conversation and does not change the final tool result.
func shellCommandOutput(ctx context.Context, command string) (func(string, []byte), func()) {
	meta, ok := ToolExecFromContext(ctx)
	if !ok || meta.EventBus == nil {
		return nil, func() {}
	}
	started := time.Now()
	var mu sync.Mutex
	var tail string
	var last time.Time
	var flushTimer *time.Timer
	sentOutput := false
	closed := false
	emit := func(done bool) {
		last = time.Now()
		_ = meta.EventBus.Emit(ctx, event.Event{
			ID: uuid.NewString(), Type: event.EventAgentCommandOutput, SessionID: meta.SessionID,
			Data: event.CommandOutputData{
				ToolCallID: meta.ToolCallID,
				Command:    maskCommandAssignments(command), StartedAt: started,
				Output: strings.ToValidUTF8(tail, ""), Done: done,
			},
		})
	}
	emit(false)
	appendOutput := func(_ string, chunk []byte) {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		tail += string(chunk)
		const limit = 8192
		if len(tail) > limit {
			tail = tail[len(tail)-limit:]
			for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
				tail = tail[1:]
			}
		}
		if !sentOutput || time.Since(last) >= 500*time.Millisecond {
			if flushTimer != nil {
				flushTimer.Stop()
				flushTimer = nil
			}
			sentOutput = true
			emit(false)
		} else if flushTimer == nil {
			flushTimer = time.AfterFunc(500*time.Millisecond-time.Since(last), func() {
				mu.Lock()
				defer mu.Unlock()
				flushTimer = nil
				if !closed {
					emit(false)
				}
			})
		}
	}
	finish := func() {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			closed = true
			if flushTimer != nil {
				flushTimer.Stop()
				flushTimer = nil
			}
			emit(true)
		}
	}
	return appendOutput, finish
}
