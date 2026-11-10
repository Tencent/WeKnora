package service

import (
	"context"
	"errors"
	"fmt"
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

// docreaderContainerEngines are the engines whose readers execute inside the
// docreader container, where the "check the DocReader logs" advice in
// docReaderFailure actually applies. Every other engine name runs in this
// process as a Go-native reader calling an external API directly.
var docreaderContainerEngines = map[string]bool{
	"":           true, // default routing: docreader builtin
	"builtin":    true,
	"markitdown": true, // remote-only engine served by the Python docreader
}

// parseFailureMessageForEngine replaces the docreader-oriented failure
// message for engines that never touch the docreader container. Pointing at
// DocReader logs when the failure came from, say, a self-hosted MinerU API
// call made from this process sends the operator to a component with zero
// evidence of the request. Naming the engine and surfacing the upstream
// error directs the investigation at the right service.
func parseFailureMessageForEngine(engine string, err error) string {
	if engine == "" || docreaderContainerEngines[engine] {
		return ""
	}
	return fmt.Sprintf("Document parsing failed in the %s engine: %v", engine, err)
}
