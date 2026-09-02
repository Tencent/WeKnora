package config

import "testing"

// TestApplyAuthAndTenantDefaults_CrossTenantAccessEnvOverride pins down the
// env-vs-YAML contract for WEKNORA_TENANT_ENABLE_CROSS_TENANT_ACCESS.
//
// Previously the variable was documented in .env.example and echoed in the
// startup log, but applyAuthAndTenantDefaults never read it — only the
// config.yaml `tenant.enable_cross_tenant_access` value took effect. Operators
// who set WEKNORA_TENANT_ENABLE_CROSS_TENANT_ACCESS=true saw no change and
// middleware.RequireCrossTenantAccess kept returning 1002 "Cross-workspace
// access is disabled". This test locks in the "env always wins when set"
// contract that other tenant env vars (enable_rbac, max_owned_per_user, ...)
// already follow.
func TestApplyAuthAndTenantDefaults_CrossTenantAccessEnvOverride(t *testing.T) {
	cases := []struct {
		name     string
		env      string // value for WEKNORA_TENANT_ENABLE_CROSS_TENANT_ACCESS ("" == unset)
		yaml     bool   // value pre-set on cfg.Tenant.EnableCrossTenantAccess
		expected bool
	}{
		{"unset preserves YAML true", "", true, true},
		{"unset preserves YAML false", "", false, false},
		{"true overrides YAML false", "true", false, true},
		{"TRUE overrides case-insensitively", "TRUE", false, true},
		{"false overrides YAML true", "false", true, false},
		{"whitespace is trimmed before parsing", " true ", false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEKNORA_TENANT_ENABLE_CROSS_TENANT_ACCESS", tc.env)
			// Other tenant env vars must not leak between cases.
			t.Setenv("WEKNORA_TENANT_ENABLE_RBAC", "")
			t.Setenv("WEKNORA_TENANT_MAX_OWNED_PER_USER", "")
			t.Setenv("WEKNORA_TENANT_SELF_SERVICE_CREATION_ENABLED", "")
			t.Setenv("DISABLE_REGISTRATION", "")

			cfg := &Config{Tenant: &TenantConfig{EnableCrossTenantAccess: tc.yaml}}
			applyAuthAndTenantDefaults(cfg)

			if cfg.Tenant.EnableCrossTenantAccess != tc.expected {
				t.Fatalf(
					"EnableCrossTenantAccess = %v, want %v (env=%q yaml=%v)",
					cfg.Tenant.EnableCrossTenantAccess, tc.expected, tc.env, tc.yaml,
				)
			}
		})
	}
}

// TestApplyAuthAndTenantDefaults_CrossTenantAccessKeepsWorkingWithNilTenant
// guards against a nil *TenantConfig panic: applyAuthAndTenantDefaults
// allocates the section itself, so callers with a bare &Config{} are safe.
func TestApplyAuthAndTenantDefaults_CrossTenantAccessKeepsWorkingWithNilTenant(t *testing.T) {
	t.Setenv("WEKNORA_TENANT_ENABLE_CROSS_TENANT_ACCESS", "true")

	cfg := &Config{}
	applyAuthAndTenantDefaults(cfg)

	if cfg.Tenant == nil || !cfg.Tenant.EnableCrossTenantAccess {
		t.Fatalf("env override should allocate Tenant and set EnableCrossTenantAccess=true, got %+v", cfg.Tenant)
	}
}
