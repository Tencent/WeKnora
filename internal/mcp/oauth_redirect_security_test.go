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
