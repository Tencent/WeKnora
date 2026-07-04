package docparser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const docReaderHealthCheckTimeout = 3 * time.Second

func withDefaultTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func isRetryableGRPCError(err error) bool {
	if err == nil || isContextError(err) {
		return false
	}
	code := status.Code(err)
	switch code {
	case codes.Unavailable:
		return true
	case codes.Internal, codes.Unknown:
		msg := strings.ToLower(status.Convert(err).Message())
		return strings.Contains(msg, "transport") ||
			strings.Contains(msg, "connection") ||
			strings.Contains(msg, "eof")
	default:
		return false
	}
}

type httpStatusError struct {
	op     string
	status int
	body   string
}

func (e *httpStatusError) Error() string {
	if e.body == "" {
		return fmt.Sprintf("http %s status %d", e.op, e.status)
	}
	return fmt.Sprintf("http %s status %d: %s", e.op, e.status, e.body)
}

func isRetryableHTTPError(err error) bool {
	if err == nil || isContextError(err) {
		return false
	}

	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.status {
		case 408, 502, 503, 504:
			return true
		default:
			return false
		}
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return !isContextError(urlErr.Err)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
