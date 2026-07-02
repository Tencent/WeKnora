package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParserEngineCheckOverrides_PreservesStoredSecrets(t *testing.T) {
	body := types.ParserEngineConfig{
		MinerUAPIKey:          types.RedactedSecretPlaceholder,
		PaddleOCRVLCloudToken: types.RedactedSecretPlaceholder,
		MinerUEndpoint:        "http://mineru-new",
	}
	existing := &types.ParserEngineConfig{
		MinerUAPIKey:          "mineru-secret",
		PaddleOCRVLCloudToken: "paddle-secret",
		MinerUEndpoint:        "http://mineru-old",
	}

	merged := types.MergeParserEngineConfigForUpdate(&body, existing)
	require.NotNil(t, merged)
	overrides := merged.ToOverridesMap()
	assert.Equal(t, "mineru-secret", overrides["mineru_api_key"])
	assert.Equal(t, "paddle-secret", overrides["paddleocr_vl_cloud_token"])
	assert.Equal(t, "http://mineru-new", overrides["mineru_endpoint"])
}
