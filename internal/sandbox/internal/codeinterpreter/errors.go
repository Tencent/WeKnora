package codeinterpreter

import "fmt"

// SandboxError is the generic error returned by the SDK for unexpected
// server responses.
type SandboxError struct {
	Message    string
	StatusCode int
}

func (e *SandboxError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("sandbox error (%d): %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("sandbox error: %s", e.Message)
}

// NotFoundError is returned when a resource (context, sandbox, file) is missing.
type NotFoundError struct {
	Message string
}

func (e *NotFoundError) Error() string { return "not found: " + e.Message }

// TimeoutError is returned when a request or execution times out.
type TimeoutError struct {
	Message string
}

func (e *TimeoutError) Error() string { return "timeout: " + e.Message }

// InvalidArgumentError is returned when input parameters are invalid
// (e.g. providing both `context` and `language`).
type InvalidArgumentError struct {
	Message string
}

func (e *InvalidArgumentError) Error() string { return "invalid argument: " + e.Message }

// AuthenticationError is returned when the supplied API key is invalid or
// missing.
type AuthenticationError struct {
	Message string
}

func (e *AuthenticationError) Error() string { return "authentication error: " + e.Message }

// RateLimitError is returned when the caller has exceeded the API's rate limit.
type RateLimitError struct {
	Message string
}

func (e *RateLimitError) Error() string { return "rate limit: " + e.Message }

// formatRequestTimeoutError wraps an error with a friendlier timeout message.
func formatRequestTimeoutError() error {
	return &TimeoutError{
		Message: "Request timed out — the 'RequestTimeout' option can be used to increase this timeout",
	}
}

// formatExecutionTimeoutError wraps an error with a friendlier timeout message
// for code execution.
func formatExecutionTimeoutError() error {
	return &TimeoutError{
		Message: "Execution timed out — the 'Timeout' option can be used to increase this timeout",
	}
}
