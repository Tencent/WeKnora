package config

import "testing"

// TestApplyAuthAndTenantDefaults_DisableRegistrationDrivesRegistrationMode
// pins down the env-vs-YAML contract for DISABLE_REGISTRATION. Without this,
// DISABLE_REGISTRATION=true would block /auth/register at the handler layer
// but leave /auth/config reporting self_serve, so the frontend would keep
// showing the (broken) Register entry. Coercing registration_mode here keeps
// both gates in sync, and matches the docs/rbac.md "env always wins over
// YAML" rule.
func TestApplyAuthAndTenantDefaults_DisableRegistrationDrivesRegistrationMode(t *testing.T) {
	cases := []struct {
		name     string
		disable  string // value for DISABLE_REGISTRATION (empty == unset)
		cfgMode  string // pre-set value on cfg.Auth.RegistrationMode
		expected string
	}{
		{"true coerces empty YAML to invite_only", "true", "", AuthRegistrationModeInviteOnly},
		{"case-insensitive TRUE also coerces", "TRUE", "", AuthRegistrationModeInviteOnly},
		{"true overrides explicit self_serve YAML", "true", AuthRegistrationModeSelfServe, AuthRegistrationModeInviteOnly},
		{"true is a no-op when YAML already invite_only", "true", AuthRegistrationModeInviteOnly, AuthRegistrationModeInviteOnly},
		{"false leaves YAML untouched", "false", AuthRegistrationModeSelfServe, AuthRegistrationModeSelfServe},
		{"unset falls back to default self_serve", "", "", AuthRegistrationModeSelfServe},
		{"unset keeps explicit invite_only YAML", "", AuthRegistrationModeInviteOnly, AuthRegistrationModeInviteOnly},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DISABLE_REGISTRATION", tc.disable)
			// Other tenant env vars must not leak between cases.
			t.Setenv("WEKNORA_TENANT_ENABLE_RBAC", "")
			t.Setenv("WEKNORA_TENANT_MAX_OWNED_PER_USER", "")

			cfg := &Config{Auth: &AuthConfig{RegistrationMode: tc.cfgMode}}
			applyAuthAndTenantDefaults(cfg)

			if cfg.Auth.RegistrationMode != tc.expected {
				t.Fatalf("registration_mode = %q, want %q", cfg.Auth.RegistrationMode, tc.expected)
			}
		})
	}
}
