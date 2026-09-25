package pluginapi

import (
	"errors"
	"fmt"
)

// ErrorCode classifies a failed call; WeKnora reacts to each differently.
type ErrorCode string

const (
	// CodeInvalidConfig means the configuration is wrong; Details.Fields says
	// where. WeKnora shows it on the form.
	CodeInvalidConfig ErrorCode = "invalid_config"
	// CodeUnauthorized means the credentials no longer work.
	CodeUnauthorized ErrorCode = "unauthorized"
	// CodeNotFound means the resource does not exist.
	CodeNotFound ErrorCode = "not_found"
	// CodeRateLimited means back off for Details.RetryAfter seconds.
	CodeRateLimited ErrorCode = "rate_limited"
	// CodeUnavailable means the upstream service is down; retry later.
	CodeUnavailable ErrorCode = "unavailable"
	// CodeBadRequest means the call itself is malformed (protocol error).
	CodeBadRequest ErrorCode = "bad_request"
	// CodeInternal means the plugin failed.
	CodeInternal ErrorCode = "internal"
)

// Error is a failed call. Plugins return it (or wrap it) to choose the code;
// any other error becomes CodeInternal.
type Error struct {
	Code      ErrorCode     `json:"code"`
	Message   string        `json:"message"`
	Retryable bool          `json:"retryable,omitempty"`
	Details   *ErrorDetails `json:"details,omitempty"`
}

// ErrorDetails refines some codes.
type ErrorDetails struct {
	// Fields are per-field problems for CodeInvalidConfig, keyed by the
	// dotted field path.
	Fields map[string]string `json:"fields,omitempty"`
	// RetryAfter is seconds to wait for CodeRateLimited.
	RetryAfter int `json:"retryAfter,omitempty"`
}

// ErrorBody is the JSON body of a failed call.
type ErrorBody struct {
	Error Error `json:"error"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Errorf builds an Error. Unavailable and rate-limited errors are retryable.
func Errorf(code ErrorCode, format string, args ...any) *Error {
	return &Error{
		Code: code, Message: fmt.Sprintf(format, args...),
		Retryable: code == CodeUnavailable || code == CodeRateLimited,
	}
}

// InvalidConfig reports field problems, keyed by dotted path.
func InvalidConfig(message string, fields map[string]string) *Error {
	return &Error{Code: CodeInvalidConfig, Message: message, Details: &ErrorDetails{Fields: fields}}
}

// AsError finds an *Error in err's chain.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// HTTPStatus is the status an error code travels with.
func (c ErrorCode) HTTPStatus() int {
	switch c {
	case CodeInvalidConfig, CodeBadRequest:
		return 400
	case CodeUnauthorized:
		return 401
	case CodeNotFound:
		return 404
	case CodeRateLimited:
		return 429
	case CodeUnavailable:
		return 503
	default:
		return 500
	}
}
