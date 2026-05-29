package cmdutil

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyHTTPError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorCode
	}{
		{"nil", nil, ""},
		{"non-HTTP transport", errors.New("dial tcp: lookup host: no such host"), CodeNetworkError},
		{"401", fmt.Errorf("HTTP error 401: invalid token"), CodeAuthUnauthenticated},
		{"403", fmt.Errorf("HTTP error 403: forbidden"), CodeAuthForbidden},
		{"404", fmt.Errorf("HTTP error 404: kb not found"), CodeResourceNotFound},
		{"409", fmt.Errorf("HTTP error 409: already exists"), CodeResourceAlreadyExists},
		{"429", fmt.Errorf("HTTP error 429: slow down"), CodeServerRateLimited},
		{"500", fmt.Errorf("HTTP error 500: internal"), CodeServerError},
		{"503", fmt.Errorf("HTTP error 503: unavailable"), CodeServerError},
		{"400 generic", fmt.Errorf("HTTP error 400: bad input"), CodeInputInvalidArgument},
		{"422 unprocessable", fmt.Errorf("HTTP error 422: validation"), CodeInputInvalidArgument},
		{"malformed status", fmt.Errorf("HTTP error abc: oops"), CodeServerError},
		{"missing colon", fmt.Errorf("HTTP error 404 no colon"), CodeServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ClassifyHTTPError(tc.err))
		})
	}
}

func TestClassifyHTTPError_500NotFoundRescue(t *testing.T) {
	// Server's 1003 = ErrNotFound is the only typed "not found" code we
	// rescue. 1007 = ErrInternalServer is the catch-all bucket — its
	// presence around a "not found"-shaped message reflects a server-side
	// classification gap, not an authoritative not-found signal, so we
	// must NOT silently re-route it.
	err := fmt.Errorf("HTTP error 500: %s", `{"error":{"code":1003,"message":"Knowledge not found"},"success":false}`)
	if got := ClassifyHTTPError(err); got != CodeResourceNotFound {
		t.Errorf("expected resource.not_found rescue for code 1003; got %v", got)
	}
}

// TestClassifyHTTPError_500GenericCode_StaysServerError pins the round-9
// finding: code 1007 is server's generic ErrInternalServer bucket
// (validation errors, DB failures, etc.), NOT a not-found signal. Even
// when its message text contains "not found", rescuing it would mis-route
// e.g. a 10k-char KB name (SQLSTATE 22001) as resource.not_found.
func TestClassifyHTTPError_500GenericCode_StaysServerError(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "code 1007 with 'not found' in message",
			body: `HTTP error 500: {"error":{"code":1007,"message":"knowledge base not found"},"success":false}`,
		},
		{
			name: "code 1007 with SQLSTATE",
			body: `HTTP error 500: {"error":{"code":1007,"message":"value too long for type character varying(255) (SQLSTATE 22001)"},"success":false}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fmt.Errorf("%s", tc.body)
			if got := ClassifyHTTPError(err); got != CodeServerError {
				t.Errorf("expected server.error for generic code 1007; got %v", got)
			}
		})
	}
}

func TestClassifyHTTPError_500Generic_StaysServerError(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "generic 500",
			body: "HTTP error 500: internal server error",
		},
		{
			name: "config file not found in stack trace",
			body: "HTTP error 500: panic: config file not found in /etc/app/config.yaml",
		},
		{
			name: "not found substring without server code",
			body: `HTTP error 500: {"message":"something not found","other":true}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fmt.Errorf("%s", tc.body)
			if got := ClassifyHTTPError(err); got != CodeServerError {
				t.Errorf("expected server.error for generic 500; got %v", got)
			}
		})
	}
}
