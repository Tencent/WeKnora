package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMCPService_RedactSensitiveData(t *testing.T) {
	t.Run("redacts both api_key and token when set", func(t *testing.T) {
		m := &MCPService{
			AuthConfig: &MCPAuthConfig{
				APIKey: "real-api",
				Token:  "real-token",
			},
		}
		m.RedactSensitiveData()
		assert.Equal(t, RedactedSecretPlaceholder, m.AuthConfig.APIKey)
		assert.Equal(t, RedactedSecretPlaceholder, m.AuthConfig.Token)
	})

	t.Run("leaves empty secrets empty", func(t *testing.T) {
		m := &MCPService{AuthConfig: &MCPAuthConfig{}}
		m.RedactSensitiveData()
		assert.Empty(t, m.AuthConfig.APIKey, "empty must stay empty to signal 'not set' to the frontend")
		assert.Empty(t, m.AuthConfig.Token)
	})

	t.Run("noop on nil AuthConfig", func(t *testing.T) {
		m := &MCPService{AuthConfig: nil}
		assert.NotPanics(t, func() { m.RedactSensitiveData() })
		assert.Nil(t, m.AuthConfig)
	})

	t.Run("zeroes write-only clear flags so they never echo back", func(t *testing.T) {
		// Regression for response-side leak: a true Clear* bool serializes
		// despite json:"...,omitempty" because true is not a zero value.
		// Any response path that runs RedactSensitiveData must produce JSON
		// without the clear_api_key / clear_token keys.
		m := &MCPService{
			AuthConfig: &MCPAuthConfig{
				APIKey:      "real-api",
				Token:       "real-token",
				ClearAPIKey: true,
				ClearToken:  true,
			},
		}
		m.RedactSensitiveData()
		assert.False(t, m.AuthConfig.ClearAPIKey, "ClearAPIKey must be reset before response")
		assert.False(t, m.AuthConfig.ClearToken, "ClearToken must be reset before response")
	})
}
