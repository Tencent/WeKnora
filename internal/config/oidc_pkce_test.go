package config

import "testing"

func TestOIDCPKCEEnvOverride(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("OIDC_AUTH_PKCE_ENABLED", value)
			cfg := &Config{OIDCAuth: &OIDCAuthConfig{PKCEEnabled: value != "true"}}
			applyOIDCEnvOverrides(cfg)
			if cfg.OIDCAuth.PKCEEnabled != (value == "true") {
				t.Fatal("PKCE environment override was not applied")
			}
		})
	}
}
