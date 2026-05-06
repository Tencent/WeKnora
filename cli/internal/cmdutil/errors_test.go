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
