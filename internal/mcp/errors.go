package mcp

import (
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/client/transport"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
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

// toolNotInDirectoryError is a call refused before it was sent because the
// connection's current directory has no such tool. It matches ErrToolNotFound.
type toolNotInDirectoryError string

func (e toolNotInDirectoryError) Error() string {
	return fmt.Sprintf("tool %q not found in MCP directory", string(e))
}

func (e toolNotInDirectoryError) Is(target error) bool { return target == ErrToolNotFound }

// IsDirectoryMismatch reports a call that failed because the caller's tool
// definition no longer matches the server: the tool is missing from the
// server's current directory, or the server rejected the method or its
// arguments. MCP servers answer an unknown tool name with invalid params, and
// a changed input schema produces the same code, so both point at a tool
// directory that is older than the server.
func IsDirectoryMismatch(err error) bool {
	return errors.Is(err, ErrToolNotFound) ||
		errors.Is(err, sdkmcp.ErrInvalidParams) ||
		errors.Is(err, sdkmcp.ErrMethodNotFound)
}
