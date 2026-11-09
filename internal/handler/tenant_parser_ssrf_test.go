package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateParserEngineOutboundURLs_RejectsPrivateMinerUEndpoint(t *testing.T) {
	err := validateParserEngineConfig(&types.ParserEngineConfig{
		MinerUEndpoint: "http://127.0.0.1:8080",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mineru_endpoint")
}

func TestValidateParserEngineOutboundURLs_RejectsPrivateVLMServerURL(t *testing.T) {
	err := validateParserEngineConfig(&types.ParserEngineConfig{
		MinerUVLMServerURL: "http://169.254.169.254",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mineru_vlm_server_url")
}

func TestValidateParserEngineConfig_RejectsReservedPaddleOCRVLHeader(t *testing.T) {
	err := validateParserEngineConfig(&types.ParserEngineConfig{
		PaddleOCRVLHeaders: map[string]string{"Content-Type": "text/plain"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Content-Type")
}

func TestValidateParserEngineConfig_AcceptsGatewayHeader(t *testing.T) {
	err := validateParserEngineConfig(&types.ParserEngineConfig{
		PaddleOCRVLHeaders: map[string]string{"X-Gateway-Token": "abc"},
	})
	require.NoError(t, err)
}
