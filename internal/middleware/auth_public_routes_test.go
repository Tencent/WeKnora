package middleware

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMicrosoftOAuthCallbacksArePublicOnlyForGET(t *testing.T) {
	for _, callback := range []string{
		"/api/v1/datasource-oauth/onedrive/callback",
		"/api/v1/datasource-oauth/microsoft/callback",
	} {
		require.True(t, isNoAuthAPI(callback, http.MethodGet))
		require.False(t, isNoAuthAPI(callback, http.MethodPost))
	}
	require.False(t, isNoAuthAPI("/api/v1/datasource/oauth/callback", http.MethodGet))
}
