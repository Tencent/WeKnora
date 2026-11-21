// Package voyageapi holds what the Voyage AI protocol packages share: the
// reading of its error body.
//
// Voyage answers every 4XX with {"detail": "<message>"}
// (https://docs.voyageai.com/reference/embeddings-api, "4XX" response). The
// shared transport keeps the raw body in api.HTTPError, which is right for
// vendors with no common envelope; here the message is the useful part, so
// it is lifted out. The extractor is deliberately narrow: only a string
// `detail` is trusted, and anything else (a validation error that nests
// objects, an HTML gateway page) is returned untouched rather than guessed at.
package voyageapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Error is a Voyage failure whose message was read from {"detail": ...}.
type Error struct {
	Status int
	Detail string
	cause  *api.HTTPError
}

func (e *Error) Error() string {
	return fmt.Sprintf("voyage API error (status %d): %s", e.Status, e.Detail)
}

// Unwrap keeps the transport error reachable: callers decide on retries and
// auth failures from the status code, not from the message.
func (e *Error) Unwrap() error { return e.cause }

// WrapError turns an api.HTTPError carrying a Voyage {"detail": "..."} body
// into an *Error. Every other error, including an HTTPError whose body is
// not that shape, is returned as it came.
func WrapError(err error) error {
	var httpErr *api.HTTPError
	if !errors.As(err, &httpErr) {
		return err
	}
	var body struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal([]byte(httpErr.Body), &body) != nil {
		return err
	}
	var detail string
	if json.Unmarshal(body.Detail, &detail) != nil || strings.TrimSpace(detail) == "" {
		return err
	}
	return &Error{Status: httpErr.StatusCode, Detail: detail, cause: httpErr}
}
