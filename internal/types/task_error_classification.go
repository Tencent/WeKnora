package types

import (
	"context"
	"errors"
	"net"
)

type httpStatusCoder interface {
	StatusCode() int
}

type ProviderHTTPError struct {
	Status int
	Body   string
}

func (e *ProviderHTTPError) Error() string {
	if e == nil {
		return ""
	}
	if e.Body == "" {
		return "provider http error"
	}
	return e.Body
}

func (e *ProviderHTTPError) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.Status
}

func ClassifyTaskError(err error) TaskErrorClass {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return TaskErrorClassCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return TaskErrorClassRetryable
	}
	if status := HTTPStatusFromError(err); status > 0 {
		switch {
		case status == 429:
			return TaskErrorClassRetryable
		case status == 401 || status == 403:
			return TaskErrorClassTerminal
		case status == 408 || status >= 500:
			return TaskErrorClassRetryable
		default:
			return TaskErrorClassTerminal
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return TaskErrorClassRetryable
	}
	return TaskErrorClassTerminal
}

func HTTPStatusFromError(err error) int {
	if err == nil {
		return 0
	}
	var coder httpStatusCoder
	if errors.As(err, &coder) {
		return coder.StatusCode()
	}
	return 0
}
