package handler

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateAutomationPreservesModelOverride(t *testing.T) {
	var req CreateAutomationRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"name": "Digest", "instruction": "Summarize the news",
		"model_id": "selected-model",
		"trigger": {"type": "cron", "expr": "0 9 * * *"}
	}`), &req))
	require.NotNil(t, req.patch().ModelID)
	require.Equal(t, "selected-model", *req.patch().ModelID)
}
