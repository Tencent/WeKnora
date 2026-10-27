package service

import (
	"context"
	"errors"
	"net"
	"strings"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Keep actionable public messages separate from raw transport diagnostics,
// which may contain internal addresses or credentials and belong in error_detail.
func docReaderFailure(err error) (code, message string) {
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded ||
		strings.Contains(err.Error(), "docreader call timeout") {
		return werrors.ErrCodeDocReaderTimeout,
			"Document parsing service timed out. Check service health and load before retrying."
	}
	var networkError *net.OpError
	if status.Code(err) == codes.Unavailable || errors.As(err, &networkError) {
		return werrors.ErrCodeDocReaderUnavailable,
			"Cannot connect to the document parsing service, or the connection was interrupted. " +
				"Check that DocReader is running and healthy, then retry."
	}
	return werrors.ErrCodeDocReaderParseFailed,
		"Document parsing failed. Check the file format and the DocReader logs for this attempt."
}
