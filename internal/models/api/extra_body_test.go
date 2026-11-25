package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeExtraBodyKeepsCallerKeys(t *testing.T) {
	body := map[string]any{"model": "m"}
	MergeExtraBody(body, map[string]any{"model": "other", "enable_search": true}, false)
	require.Equal(t, "m", body["model"])
	require.Equal(t, true, body["enable_search"])
}

func TestMergeExtraBodyDropsToolAndSessionKeysWhenIsolated(t *testing.T) {
	extra := map[string]any{"enable_search": true}
	for _, key := range IsolatedExtraBodyKeys {
		extra[key] = "x"
	}
	body := map[string]any{}
	MergeExtraBody(body, extra, true)
	require.Equal(t, map[string]any{"enable_search": true}, body)
}
