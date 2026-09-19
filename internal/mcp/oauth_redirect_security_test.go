package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFrontendRedirectIsApplicationRelative(t *testing.T) {
	t.Setenv("APP_EXTERNAL_URL", "")
	for _, raw := range []string{
		"https://evil.example/", "//evil.example/", `/\evil.example/`, "/%2fexample.com",
		"/%2f%2fevil.com", "/%2F%2Fevil.com", "/%5cexample.com", "/%5c%5cevil.com",
		"javascript:alert(1)", " /example", "/\nexample",
	} {
		_, err := validateFrontendRedirect(raw)
		require.Error(t, err, raw)
	}
	for _, raw := range []string{"/", "/embed/channel?theme=dark", "/prefix/settings"} {
		result, err := validateFrontendRedirect(raw)
		require.NoError(t, err)
		require.Equal(t, raw, result)
	}
}

func TestFrontendRedirectAllowsOnlyConfiguredOrigin(t *testing.T) {
	t.Setenv("APP_EXTERNAL_URL", "https://app.example/prefix")
	_, err := validateFrontendRedirect("https://app.example/prefix/settings")
	require.NoError(t, err)
	for _, raw := range []string{
		"https://app.example.evil.example/", "http://app.example/", "https://app.example:444/",
	} {
		_, err = validateFrontendRedirect(raw)
		require.Error(t, err)
	}
}

func TestValidateOAuthRedirectURIWithConfiguredBase(t *testing.T) {
	t.Setenv("APP_EXTERNAL_URL", "https://weknora.example.com")
	require.NoError(t, ValidateOAuthRedirectURI(
		"https://weknora.example.com/api/v1/mcp-oauth/callback", "weknora.example.com"))
	for _, raw := range []string{
		"https://weknora.example.com/api/v1/mcp-oauth/callback/extra",
		"https://weknora.example.com.evil.com/api/v1/mcp-oauth/callback",
		"http://weknora.example.com/api/v1/mcp-oauth/callback",
		"https://weknora.example.com:444/api/v1/mcp-oauth/callback",
		"https://elsewhere.example/api/v1/mcp-oauth/callback",
		"/api/v1/mcp-oauth/callback",
		"https://weknora.example.com@evil.com/api/v1/mcp-oauth/callback",
		`https://weknora.example.com\@evil.com/api/v1/mcp-oauth/callback`,
		"https://weknora.example.com/api/v1/mcp-oauth/callback?code=x",
		"https://weknora.example.com/api/v1/mcp-oauth/callback#frag",
	} {
		require.Error(t, ValidateOAuthRedirectURI(raw, "weknora.example.com"), raw)
	}
}

func TestValidateOAuthRedirectURIFallsBackToRequestHost(t *testing.T) {
	// Default deployment: APP_EXTERNAL_URL unset (.env.example / docker-compose
	// default). The frontend builds window.location.origin + callback path, so
	// same-origin callbacks must be accepted.
	t.Setenv("APP_EXTERNAL_URL", "")
	require.NoError(t, ValidateOAuthRedirectURI(
		"https://weknora.example.com/api/v1/mcp-oauth/callback", "weknora.example.com"))
	require.NoError(t, ValidateOAuthRedirectURI(
		"http://192.168.1.10:8080/api/v1/mcp-oauth/callback", "192.168.1.10:8080"))
	for _, raw := range []string{
		"https://evil.example/api/v1/mcp-oauth/callback",
		"https://weknora.example.com/api/v1/mcp-oauth/callback/extra",
		"https://weknora.example.com/other/path",
	} {
		require.Error(t, ValidateOAuthRedirectURI(raw, "weknora.example.com"), raw)
	}
	// A missing request host must not silently accept anything.
	require.Error(t, ValidateOAuthRedirectURI(
		"https://weknora.example.com/api/v1/mcp-oauth/callback", ""))
}
