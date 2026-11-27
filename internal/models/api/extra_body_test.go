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

func TestMergeExtraBodyAddsMissingNestedFields(t *testing.T) {
	thinking := map[string]any{"type": "between_tools"}
	body := map[string]any{"thinking": thinking}
	extra := map[string]any{"thinking": map[string]any{
		"type":          "disabled",
		"block_binding": map[string]any{"prefix_mismatch_behavior": "drop_block"},
	}}
	MergeExtraBody(body, extra, false)
	require.Equal(t, map[string]any{
		"type":          "between_tools",
		"block_binding": map[string]any{"prefix_mismatch_behavior": "drop_block"},
	}, body["thinking"])
	require.Equal(t, map[string]any{"type": "between_tools"}, thinking, "request map must not be mutated")
}

func TestMergeExtraBodyKeepsScalarWhenConfigHasObject(t *testing.T) {
	body := map[string]any{"thinking": "off"}
	MergeExtraBody(body, map[string]any{"thinking": map[string]any{"type": "adaptive"}}, false)
	require.Equal(t, "off", body["thinking"])
}
