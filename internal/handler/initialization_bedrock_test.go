package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/providers"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestBedrockRerankTestUsesModelSecretNotTenantSecret(t *testing.T) {
	model := &types.Model{Parameters: types.ModelParameters{
		Provider: providers.BedrockID, APIKey: "aws-access-key", AppSecret: "aws-secret",
	}}
	appID, secret := rerankTestCredentials(model, "tenant-app", "tenant-secret")
	assert.Empty(t, appID)
	assert.Equal(t, "aws-secret", secret)
}

func TestWeKnoraCloudRerankTestKeepsTenantCredentials(t *testing.T) {
	model := &types.Model{Parameters: types.ModelParameters{Provider: providers.WeKnoraCloudID}}
	appID, secret := rerankTestCredentials(model, "tenant-app", "tenant-secret")
	assert.Equal(t, "tenant-app", appID)
	assert.Equal(t, "tenant-secret", secret)
}
