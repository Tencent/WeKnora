package config

import "testing"

func TestConfiguredExternalURLPrefersAppURL(t *testing.T) {
	t.Setenv("APP_EXTERNAL_URL", " https://app.example.com/// ")
	t.Setenv("FRONTEND_BASE_URL", "https://frontend.example.com")
	if got := ConfiguredExternalURL(); got != "https://app.example.com" {
		t.Fatalf("ConfiguredExternalURL() = %q, want APP_EXTERNAL_URL", got)
	}
}

func TestConfiguredExternalURLFallsBackToFrontendURL(t *testing.T) {
	t.Setenv("APP_EXTERNAL_URL", "  ")
	t.Setenv("FRONTEND_BASE_URL", " https://frontend.example.com/ ")
	if got := ConfiguredExternalURL(); got != "https://frontend.example.com" {
		t.Fatalf("ConfiguredExternalURL() = %q, want FRONTEND_BASE_URL", got)
	}
}
