package mcp

import (
	"errors"
	"net"
	"strings"

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
)

// Undelivered reports whether a failed call certainly did not reach the
// tool: the client was not connected, the connection could not be made, or
// the server rejected the session before handling the request. Only then is
// calling again on a fresh connection safe for a tool with side effects; any
// other failure (a timeout, a connection lost mid-call, a server error) may
// have come after the tool ran.
func Undelivered(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotConnected) || errors.Is(err, transport.ErrSessionTerminated) {
		return true
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	msg := err.Error()
	// Session rejections some servers send instead of a 404.
	return strings.Contains(msg, "Invalid session ID") || strings.Contains(msg, "No active connection")
}
