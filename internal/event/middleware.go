package event

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

// Middleware is a function that wraps an EventHandler
type Middleware func(EventHandler) EventHandler

// WithLogging creates a middleware that logs event handling
func WithLogging() Middleware {
	return func(next EventHandler) EventHandler {
		return func(ctx context.Context, event Event) error {
			logger.Infof(ctx, "Event triggered: type=%s, session=%s, request=%s",
				event.Type, event.SessionID, event.RequestID)

			err := next(ctx, event)

			if err != nil {
				logger.Errorf(ctx, "Event handler error: type=%s, error=%v", event.Type, err)
			} else {
				logger.Debugf(ctx, "Event handled successfully: type=%s", event.Type)
			}

			return err
		}
	}
}

// WithTiming creates a middleware that tracks event handling duration
func WithTiming() Middleware {
	return func(next EventHandler) EventHandler {
		return func(ctx context.Context, event Event) error {
			start := time.Now()
			err := next(ctx, event)
			duration := time.Since(start)

			logger.Debugf(ctx, "Event %s took %v", event.Type, duration)

			// 将耗时添加到事件元数据中
			if event.Metadata == nil {
				event.Metadata = make(map[string]interface{})
			}
			event.Metadata["duration_ms"] = duration.Milliseconds()

			return err
		}
	}
}

// WithRecovery creates a middleware that recovers from panics
func WithRecovery() Middleware {
	return func(next EventHandler) EventHandler {
		return func(ctx context.Context, event Event) (err error) {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf(ctx, "Event handler panic: type=%s, panic=%v", event.Type, r)
					err = &PanicError{Panic: r}
				}
			}()

			return next(ctx, event)
		}
	}
}

// PanicError represents a panic that occurred in an event handler
type PanicError struct {
	Panic interface{}
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("panic in event handler: %v", e.Panic)
}

// Chain combines multiple middlewares into a single middleware
func Chain(middlewares ...Middleware) Middleware {
	return func(handler EventHandler) EventHandler {
		// Apply middlewares in reverse order so they execute in the correct order
		for i := len(middlewares) - 1; i >= 0; i-- {
			handler = middlewares[i](handler)
		}
		return handler
	}
}

// ApplyMiddleware applies middleware to an event handler
func ApplyMiddleware(handler EventHandler, middlewares ...Middleware) EventHandler {
	return Chain(middlewares...)(handler)
}
