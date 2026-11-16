package mcp

import (
	"errors"

	"github.com/mark3labs/mcp-go/client/transport"
)

var (
	// ErrUnsupportedTransport is returned when transport type is not supported
	ErrUnsupportedTransport = errors.New("unsupported transport type")

	// ErrNotConnected is returned when operation requires connection but client is not connected
	ErrNotConnected = errors.New("client not connected")

	// ErrAlreadyConnected is returned when trying to connect an already connected client
	ErrAlreadyConnected = errors.New("client already connected")

	// ErrInitializeFailed is returned when MCP initialize handshake fails
	ErrInitializeFailed = errors.New("MCP initialize handshake failed")

	// ErrToolNotFound is returned when requested tool is not found
	ErrToolNotFound = errors.New("tool not found")

	// ErrResourceNotFound is returned when requested resource is not found
	ErrResourceNotFound = errors.New("resource not found")

	// ErrInvalidResponse is returned when server response is invalid
	ErrInvalidResponse = errors.New("invalid response from server")

	// ErrTimeout is returned when operation times out
	ErrTimeout = errors.New("operation timed out")

	// ErrConnectionClosed is returned when connection is closed unexpectedly
	ErrConnectionClosed = errors.New("connection closed")

	// ErrToolCallOutcomeUnknown means a call was attempted but its response
	// does not establish whether the server executed the tool.
	ErrToolCallOutcomeUnknown = errors.New("MCP tool execution outcome is unknown")
)

// IsToolCallRetrySafe identifies rejections before tool execution. A missing
// local connection sends no request; a terminated HTTP session is rejected
// with 404 and requires initialization. Typed authorization failures also
// reject the request and may require in-conversation authorization recovery.
// Other transport/RPC failures may occur after a side effect and must not
// authorize an automatic replay.
func IsToolCallRetrySafe(err error) bool {
	return !errors.Is(err, ErrToolCallOutcomeUnknown) &&
		(errors.Is(err, ErrNotConnected) || errors.Is(err, transport.ErrSessionTerminated) ||
			isOAuthAuthorizationFailure(err))
}
