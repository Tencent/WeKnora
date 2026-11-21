package voyageapi

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapErrorLiftsTheDetailMessage(t *testing.T) {
	err := WrapError(&api.HTTPError{
		StatusCode: 400,
		Body:       `{"detail": "Value '300' supplied for argument 'output_dimension' is not valid"}`,
	})
	require.Error(t, err)
	assert.Equal(t,
		"voyage API error (status 400): Value '300' supplied for argument 'output_dimension' is not valid",
		err.Error())

	// The status stays reachable for retry and auth decisions.
	var httpErr *api.HTTPError
	require.True(t, errors.As(err, &httpErr))
	assert.Equal(t, 400, httpErr.StatusCode)
}

func TestWrapErrorLeavesOtherFailuresAlone(t *testing.T) {
	for name, err := range map[string]error{
		"not json":            &api.HTTPError{StatusCode: 502, Body: "<html>Bad Gateway</html>"},
		"no detail":           &api.HTTPError{StatusCode: 400, Body: `{"error": {"message": "x"}}`},
		"detail is an object": &api.HTTPError{StatusCode: 422, Body: `{"detail": [{"loc": ["body"], "msg": "x"}]}`},
		"empty detail":        &api.HTTPError{StatusCode: 400, Body: `{"detail": " "}`},
		"transport":           &api.TransportError{Op: "send request", Err: errors.New("refused")},
		"plain":               fmt.Errorf("decode response: boom"),
	} {
		t.Run(name, func(t *testing.T) {
			assert.Same(t, err, WrapError(err))
		})
	}
	assert.NoError(t, WrapError(nil))
}
