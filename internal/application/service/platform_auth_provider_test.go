package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/infrastructure/ldapauth"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// test doubles
// ---------------------------------------------------------------------------

// stubPlatformAuthProviderRepo is an in-memory stand-in for the persistence
// layer. It embeds the interface so an unstubbed method nil-panics loudly
// instead of silently returning zero values (matching the convention used by
// stubAuditRepo in audit_log_test.go).
type stubPlatformAuthProviderRepo struct {
	interfaces.PlatformAuthProviderRepository

	mu  sync.Mutex
	row *types.PlatformAuthProvider
	// getErr lets a test simulate a broken settings table.
	getErr error
	// upserts counts writes so a test can assert "nothing was persisted".
	upserts int
}

func (s *stubPlatformAuthProviderRepo) GetByKind(
	_ context.Context, kind types.PlatformAuthProviderKind,
) (*types.PlatformAuthProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.row == nil || s.row.Kind != kind {
		return nil, nil
	}
	// Return a copy so callers cannot mutate the stored row in place.
	cp := *s.row
	return &cp, nil
}

func (s *stubPlatformAuthProviderRepo) Upsert(
	_ context.Context, provider *types.PlatformAuthProvider,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *provider
	s.row = &cp
	s.upserts++
	return nil
}

// seedOIDC persists an OIDC row directly, bypassing the service, so tests can
// start from a "previously configured" state.
func (s *stubPlatformAuthProviderRepo) seedOIDC(
	t *testing.T, enabled bool, settings types.OIDCProviderSettings, secret string,
) {
	t.Helper()
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.row = &types.PlatformAuthProvider{
		Kind:      types.AuthProviderKindOIDC,
		Enabled:   enabled,
		Config:    types.JSON(raw),
		Secret:    secret,
		UpdatedBy: "admin-1",
		UpdatedAt: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
	}
}

func (s *stubPlatformAuthProviderRepo) seedLDAP(
	t *testing.T, enabled bool, settings types.LDAPProviderSettings, secret string,
) {
	t.Helper()
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.row = &types.PlatformAuthProvider{
		Kind:      types.AuthProviderKindLDAP,
		Enabled:   enabled,
		Config:    types.JSON(raw),
		Secret:    secret,
		UpdatedBy: "admin-1",
		UpdatedAt: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
	}
}

func newTestProviderService(
	t *testing.T, repo *stubPlatformAuthProviderRepo, cfg *config.Config,
) interfaces.PlatformAuthProviderService {
	t.Helper()
	return NewPlatformAuthProviderService(repo, cfg)
}

// clearOIDCEnv removes every legacy OIDC_AUTH_* variable so a test observes
// only the config it constructs. t.Setenv both sets and registers cleanup, so
// the empty value is restored to the original after the test.
func clearOIDCEnv(t *testing.T) {
	t.Helper()
	for _, name := range oidcEnvNames {
		t.Setenv(name, "")
	}
}

// ---------------------------------------------------------------------------
// secret is write-only
// ---------------------------------------------------------------------------

// TestGetOIDCProviderNeverReturnsClientSecret is the core write-only contract:
// the admin read path reports `has_secret` and nothing else about the
// credential, and the marshalled payload must not contain it either.
func TestGetOIDCProviderNeverReturnsClientSecret(t *testing.T) {
	clearOIDCEnv(t)
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")

	repo := &stubPlatformAuthProviderRepo{}
	svc := newTestProviderService(t, repo, &config.Config{})
	ctx := t.Context()

	const secret = "super-secret-value"

	// The deployment starts with no row and no env: nothing to disclose.
	view, err := svc.GetOIDCProvider(ctx)
	require.NoError(t, err)
	require.False(t, view.HasSecret)
	require.Equal(t, types.AuthProviderSourceDefault, view.Source)

	// Persist a provider through the real write path.
	secretPtr := secret
	_, err = svc.UpdateOIDCProvider(ctx, &types.OIDCProviderUpdateRequest{
		Enabled:      true,
		IssuerURL:    "https://idp.example.com",
		ClientID:     "weknora-client",
		ClientSecret: &secretPtr,
	})
	require.NoError(t, err)

	view, err = svc.GetOIDCProvider(ctx)
	require.NoError(t, err)
	require.True(t, view.HasSecret, "the view must report that a credential exists")
	require.Equal(t, types.AuthProviderSourceDatabase, view.Source)

	raw, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(raw), secret,
		"the client secret must never appear in the admin response payload")
	require.NotContains(t, string(raw), secutils.EncPrefix,
		"nor may the ciphertext leak")

	// The login path is the only place that resolves the plaintext.
	resolved := svc.ResolveOIDCConfig(ctx)
	require.NotNil(t, resolved)
	require.Equal(t, secret, resolved.ClientSecret)

	// Stored at rest it is encrypted, never plaintext.
	repo.mu.Lock()
	stored := repo.row.Secret
	repo.mu.Unlock()
	require.True(t, strings.HasPrefix(stored, secutils.EncPrefix),
		"the credential is wrapped before it reaches the repository")
	require.NotEqual(t, secret, stored)
}

// TestUpdateOIDCProviderSecretLifecycle pins the three-way secret semantics the
// UI relies on: omitted keeps, re-typed replaces, ClearSecret wipes.
func TestUpdateOIDCProviderSecretLifecycle(t *testing.T) {
	clearOIDCEnv(t)
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")

	repo := &stubPlatformAuthProviderRepo{}
	svc := newTestProviderService(t, repo, &config.Config{})
	ctx := t.Context()

	first := "first-secret"
	_, err := svc.UpdateOIDCProvider(ctx, &types.OIDCProviderUpdateRequest{
		Enabled:      true,
		IssuerURL:    "https://idp.example.com",
		ClientID:     "weknora-client",
		ClientSecret: &first,
	})
	require.NoError(t, err)

	// 1. Omitted (nil) -> preserved.
	_, err = svc.UpdateOIDCProvider(ctx, &types.OIDCProviderUpdateRequest{
		Enabled:   true,
		IssuerURL: "https://idp.example.com",
		ClientID:  "weknora-client",
	})
	require.NoError(t, err)
	require.Equal(t, first, svc.ResolveOIDCConfig(ctx).ClientSecret)

	// 2. Empty string -> preserved (the UI sends "" for an untouched field).
	empty := ""
	_, err = svc.UpdateOIDCProvider(ctx, &types.OIDCProviderUpdateRequest{
		Enabled:      true,
		IssuerURL:    "https://idp.example.com",
		ClientID:     "weknora-client",
		ClientSecret: &empty,
	})
	require.NoError(t, err)
	require.Equal(t, first, svc.ResolveOIDCConfig(ctx).ClientSecret)

	// 3. Re-typed -> replaced.
	second := "second-secret"
	_, err = svc.UpdateOIDCProvider(ctx, &types.OIDCProviderUpdateRequest{
		Enabled:      true,
		IssuerURL:    "https://idp.example.com",
		ClientID:     "weknora-client",
		ClientSecret: &second,
	})
	require.NoError(t, err)
	require.Equal(t, second, svc.ResolveOIDCConfig(ctx).ClientSecret)

	// 4. ClearSecret -> wiped.
	view, err := svc.UpdateOIDCProvider(ctx, &types.OIDCProviderUpdateRequest{
		Enabled:     true,
		IssuerURL:   "https://idp.example.com",
		ClientID:    "weknora-client",
		ClearSecret: true,
	})
	require.NoError(t, err)
	require.False(t, view.HasSecret)
	require.Equal(t, "", svc.ResolveOIDCConfig(ctx).ClientSecret)
}

// TestGetLDAPProviderNeverReturnsBindPassword mirrors the OIDC contract for the
// directory credential.
func TestGetLDAPProviderNeverReturnsBindPassword(t *testing.T) {
	clearOIDCEnv(t)
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")

	repo := &stubPlatformAuthProviderRepo{}
	svc := newTestProviderService(t, repo, &config.Config{})
	ctx := t.Context()

	const bindPassword = "svc-account-password"

	_, err := svc.UpdateLDAPProvider(ctx, &types.LDAPProviderUpdateRequest{
		Enabled:      true,
		Host:         "ldap.example.com",
		BaseDN:       "dc=example,dc=com",
		BindDN:       "cn=svc,dc=example,dc=com",
		BindPassword: strPtr(bindPassword),
	})
	require.NoError(t, err)

	view, err := svc.GetLDAPProvider(ctx)
	require.NoError(t, err)
	require.True(t, view.HasBindPassword)

	raw, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(raw), bindPassword)
	require.NotContains(t, string(raw), secutils.EncPrefix)

	resolved := svc.ResolveLDAPConfig(ctx)
	require.NotNil(t, resolved)
	require.Equal(t, bindPassword, resolved.BindPassword)
}

func strPtr(s string) *string { return &s }

// TestRevealSecretDegradesOnUndecryptableValue guards the operational case
// where SYSTEM_AES_KEY was rotated or removed: the service must treat the
// credential as unset rather than crash or hand out ciphertext.
func TestRevealSecretDegradesOnUndecryptableValue(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")
	require.Equal(t, "", revealSecret(secutils.EncPrefix+"bogus"),
		"undecryptable ciphertext resolves to empty, never to the raw value")
	require.Equal(t, "", revealSecret(""))
	// Legacy plaintext (saved before SYSTEM_AES_KEY existed) passes through.
	require.Equal(t, "plain", revealSecret("plain"))
}

// ---------------------------------------------------------------------------
// environment default -> database takeover
// ---------------------------------------------------------------------------

// TestOIDCConfigSourceSwitchesFromEnvironmentToDatabase covers the migration
// contract: the environment seeds the form until the first save, after which
// the row is authoritative for every field — including fields the operator
// cleared, which must not be re-filled from the environment.
func TestOIDCConfigSourceSwitchesFromEnvironmentToDatabase(t *testing.T) {
	clearOIDCEnv(t)
	t.Setenv("OIDC_AUTH_ENABLE", "true")
	t.Setenv("OIDC_AUTH_ISSUER_URL", "https://env-idp.example.com")
	t.Setenv("OIDC_AUTH_CLIENT_ID", "env-client")
	t.Setenv("OIDC_AUTH_CLIENT_SECRET", "env-secret")
	t.Setenv("OIDC_AUTH_PROVIDER_DISPLAY_NAME", "Env IdP")

	repo := &stubPlatformAuthProviderRepo{}
	svc := newTestProviderService(t, repo, &config.Config{OIDCAuth: &config.OIDCAuthConfig{
		Enable:              true,
		IssuerURL:           "https://env-idp.example.com",
		ClientID:            "env-client",
		ClientSecret:        "env-secret",
		ProviderDisplayName: "Env IdP",
	}})
	ctx := t.Context()

	// Phase 1 — no row: the environment is in force and labelled as such.
	view, err := svc.GetOIDCProvider(ctx)
	require.NoError(t, err)
	require.True(t, view.Enabled)
	require.Equal(t, "https://env-idp.example.com", view.IssuerURL)
	require.Equal(t, "env-client", view.ClientID)
	require.True(t, view.HasSecret, "the env secret is reported as present")
	require.Equal(t, types.AuthProviderSourceEnvironment, view.Source)
	require.Contains(t, view.EnvOverrides, "OIDC_AUTH_CLIENT_ID")
	require.Equal(t, "(set)", view.EnvOverrides["OIDC_AUTH_CLIENT_SECRET"],
		"secret-valued variables are reported as set, never disclosed")

	// Phase 2 — one save freezes the config, and the row takes over entirely.
	dbSecret := "db-secret"
	_, err = svc.UpdateOIDCProvider(ctx, &types.OIDCProviderUpdateRequest{
		Enabled:      true,
		IssuerURL:    "https://db-idp.example.com",
		ClientID:     "db-client",
		ClientSecret: &dbSecret,
		// ProviderDisplayName deliberately left blank.
	})
	require.NoError(t, err)

	view, err = svc.GetOIDCProvider(ctx)
	require.NoError(t, err)
	require.Equal(t, types.AuthProviderSourceDatabase, view.Source)
	require.Equal(t, "https://db-idp.example.com", view.IssuerURL)
	require.Equal(t, "db-client", view.ClientID)
	require.Equal(t, "OIDC", view.ProviderDisplayName,
		"a blank field keeps the built-in default; the env value must not leak back in")
	require.Equal(t, dbSecret, svc.ResolveOIDCConfig(ctx).ClientSecret,
		"the DB secret replaces the env secret")

	// The env variables are still reported so the operator can clean them up.
	require.Contains(t, view.EnvOverrides, "OIDC_AUTH_ISSUER_URL")

	// Phase 3 — after ClearSecret the env secret must NOT resurface.
	_, err = svc.UpdateOIDCProvider(ctx, &types.OIDCProviderUpdateRequest{
		Enabled:     true,
		IssuerURL:   "https://db-idp.example.com",
		ClientID:    "db-client",
		ClearSecret: true,
	})
	require.NoError(t, err)
	require.Equal(t, "", svc.ResolveOIDCConfig(ctx).ClientSecret,
		"clearing the credential must not fall back to the environment value")
}

// TestResolveOIDCConfigNilWhenDisabled proves the login path treats a disabled
// (or unconfigured) provider as "not available".
func TestResolveOIDCConfigNilWhenDisabled(t *testing.T) {
	clearOIDCEnv(t)
	repo := &stubPlatformAuthProviderRepo{}
	svc := newTestProviderService(t, repo, &config.Config{})
	ctx := t.Context()

	require.Nil(t, svc.ResolveOIDCConfig(ctx), "no row and no env => provider off")

	repo.seedOIDC(t, false, types.OIDCProviderSettings{
		IssuerURL: "https://idp.example.com",
		ClientID:  "c",
	}, "")
	require.Nil(t, svc.ResolveOIDCConfig(ctx), "an explicitly disabled row stays off")
}

// TestOIDCDiscoveryURLDerivedFromSavedIssuer documents that the discovery URL
// is derived from the operator's issuer, never from the environment — so
// switching IdP cannot leave the previous well-known URL pointed at the old one.
func TestOIDCDiscoveryURLDerivedFromSavedIssuer(t *testing.T) {
	raw, err := json.Marshal(types.OIDCProviderSettings{
		IssuerURL: "https://new-idp.example.com/",
	})
	require.NoError(t, err)
	cfg := oidcResolvedFromRow(&types.PlatformAuthProvider{
		Kind:    types.AuthProviderKindOIDC,
		Enabled: true,
		Config:  types.JSON(raw),
	})
	require.Equal(t, "https://new-idp.example.com/.well-known/openid-configuration",
		cfg.DiscoveryURL, "trailing slash is normalised")

	// An explicit discovery_url always wins over the derived one.
	raw, err = json.Marshal(types.OIDCProviderSettings{
		IssuerURL:    "https://new-idp.example.com",
		DiscoveryURL: "https://custom.example.com/well-known",
	})
	require.NoError(t, err)
	cfg = oidcResolvedFromRow(&types.PlatformAuthProvider{
		Kind:    types.AuthProviderKindOIDC,
		Enabled: true,
		Config:  types.JSON(raw),
	})
	require.Equal(t, "https://custom.example.com/well-known", cfg.DiscoveryURL)
}

// TestOIDCUpdateValidationRejectsUnresolvableProvider keeps the login button
// from rendering a provider that cannot complete a flow.
func TestOIDCUpdateValidationRejectsUnresolvableProvider(t *testing.T) {
	clearOIDCEnv(t)
	repo := &stubPlatformAuthProviderRepo{}
	svc := newTestProviderService(t, repo, &config.Config{})
	ctx := t.Context()

	_, err := svc.UpdateOIDCProvider(ctx, &types.OIDCProviderUpdateRequest{
		Enabled:   true,
		IssuerURL: "https://idp.example.com",
	})
	require.ErrorContains(t, err, "client_id is required")

	_, err = svc.UpdateOIDCProvider(ctx, &types.OIDCProviderUpdateRequest{
		Enabled:  true,
		ClientID: "weknora-client",
	})
	require.ErrorContains(t, err, "issuer_url")

	// Explicit endpoints are an acceptable alternative to issuer/discovery.
	_, err = svc.UpdateOIDCProvider(ctx, &types.OIDCProviderUpdateRequest{
		Enabled:               true,
		ClientID:              "weknora-client",
		AuthorizationEndpoint: "https://idp.example.com/authorize",
		TokenEndpoint:         "https://idp.example.com/token",
	})
	require.NoError(t, err)

	require.Equal(t, 1, repo.upserts, "only the valid save was persisted")
	_ = ctx
}

// ---------------------------------------------------------------------------
// LDAP: defaults, resolution, throttle
// ---------------------------------------------------------------------------

func TestLDAPDefaultsAppliedWhenNeverConfigured(t *testing.T) {
	clearOIDCEnv(t)
	repo := &stubPlatformAuthProviderRepo{}
	svc := newTestProviderService(t, repo, &config.Config{})
	ctx := t.Context()

	view, err := svc.GetLDAPProvider(ctx)
	require.NoError(t, err)
	require.False(t, view.Enabled)
	require.Equal(t, types.AuthProviderSourceDefault, view.Source)
	require.Equal(t, defaultLDAPPort, view.Port)
	require.Equal(t, defaultLDAPUserFilter, view.UserFilter)
	require.Equal(t, defaultLDAPNameAttr, view.UserNameAttr)
	require.Equal(t, defaultLDAPEmailAttr, view.UserEmailAttr)
	require.Equal(t, defaultLDAPDisplayAttr, view.UserDisplayAttr)
	require.Nil(t, svc.ResolveLDAPConfig(ctx), "an unconfigured directory stays off")
}

func TestResolveLDAPConfigRequiresHostAndBaseDN(t *testing.T) {
	clearOIDCEnv(t)
	repo := &stubPlatformAuthProviderRepo{}
	svc := newTestProviderService(t, repo, &config.Config{})
	ctx := t.Context()

	// Enabled but missing host/base_dn: must resolve to nil rather than
	// attempt a directory call with an incomplete configuration.
	repo.seedLDAP(t, true, types.LDAPProviderSettings{Port: 389}, "")
	require.Nil(t, svc.ResolveLDAPConfig(ctx))

	repo.seedLDAP(t, true, types.LDAPProviderSettings{
		Host: "ldap.example.com", Port: 389, BaseDN: "dc=example,dc=com",
	}, "")
	require.NotNil(t, svc.ResolveLDAPConfig(ctx))

	// Disabled with a complete config still resolves to nil.
	repo.seedLDAP(t, false, types.LDAPProviderSettings{
		Host: "ldap.example.com", Port: 389, BaseDN: "dc=example,dc=com",
	}, "")
	require.Nil(t, svc.ResolveLDAPConfig(ctx))
}

func TestLDAPUpdateValidation(t *testing.T) {
	clearOIDCEnv(t)
	repo := &stubPlatformAuthProviderRepo{}
	svc := newTestProviderService(t, repo, &config.Config{})
	ctx := t.Context()
	base := types.LDAPProviderUpdateRequest{
		Enabled: true, Host: "ldap.example.com", BaseDN: "dc=example,dc=com",
	}

	t.Run("scheme in host is rejected", func(t *testing.T) {
		req := base
		req.Host = "ldaps://ldap.example.com"
		_, err := svc.UpdateLDAPProvider(ctx, &req)
		require.ErrorContains(t, err, "without a scheme")
	})

	t.Run("use_tls and start_tls are mutually exclusive", func(t *testing.T) {
		req := base
		req.UseTLS, req.StartTLS = true, true
		_, err := svc.UpdateLDAPProvider(ctx, &req)
		require.ErrorContains(t, err, "mutually exclusive")
	})

	t.Run("host and base_dn required when enabled", func(t *testing.T) {
		req := base
		req.Host = ""
		_, err := svc.UpdateLDAPProvider(ctx, &req)
		require.ErrorContains(t, err, "host is required")

		req = base
		req.BaseDN = ""
		_, err = svc.UpdateLDAPProvider(ctx, &req)
		require.ErrorContains(t, err, "base_dn is required")
	})

	t.Run("default_tenant_mode is constrained", func(t *testing.T) {
		req := base
		req.DefaultTenantMode = "bogus"
		_, err := svc.UpdateLDAPProvider(ctx, &req)
		require.ErrorContains(t, err, "default_tenant_mode")
	})

	t.Run("blank fields receive defaults on save", func(t *testing.T) {
		req := base
		view, err := svc.UpdateLDAPProvider(ctx, &req)
		require.NoError(t, err)
		require.Equal(t, defaultLDAPPort, view.Port)
		require.Equal(t, defaultLDAPUserFilter, view.UserFilter)
		require.Equal(t, defaultLDAPNameAttr, view.UserNameAttr)
	})

	require.Equal(t, 1, repo.upserts, "only the valid save was persisted")
}

// TestLDAPUpdatePreservesBindPassword mirrors the OIDC secret lifecycle.
func TestLDAPUpdatePreservesBindPassword(t *testing.T) {
	clearOIDCEnv(t)
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	repo := &stubPlatformAuthProviderRepo{}
	svc := newTestProviderService(t, repo, &config.Config{})
	ctx := t.Context()

	base := func() types.LDAPProviderUpdateRequest {
		return types.LDAPProviderUpdateRequest{
			Enabled: true, Host: "ldap.example.com", BaseDN: "dc=example,dc=com",
		}
	}

	req := base()
	req.BindPassword = strPtr("bind-1")
	_, err := svc.UpdateLDAPProvider(ctx, &req)
	require.NoError(t, err)

	// Omitted -> preserved.
	req = base()
	_, err = svc.UpdateLDAPProvider(ctx, &req)
	require.NoError(t, err)
	require.Equal(t, "bind-1", svc.ResolveLDAPConfig(ctx).BindPassword)

	// Re-typed -> replaced.
	req = base()
	req.BindPassword = strPtr("bind-2")
	_, err = svc.UpdateLDAPProvider(ctx, &req)
	require.NoError(t, err)
	require.Equal(t, "bind-2", svc.ResolveLDAPConfig(ctx).BindPassword)

	// ClearBindPassword -> wiped.
	req = base()
	req.ClearBindPassword = true
	view, err := svc.UpdateLDAPProvider(ctx, &req)
	require.NoError(t, err)
	require.False(t, view.HasBindPassword)
	require.Equal(t, "", svc.ResolveLDAPConfig(ctx).BindPassword)
}

func TestLDAPClientConfigMapsTimeout(t *testing.T) {
	// Zero means "use the transport default" rather than "no timeout".
	cfg := ldapClientConfig(&types.ResolvedLDAPConfig{
		LDAPProviderSettings: types.LDAPProviderSettings{Host: "h", BaseDN: "b"},
	})
	require.Equal(t, ldapauth.DefaultTimeout, cfg.Timeout)
	require.Equal(t, "h", cfg.Host)
	require.Equal(t, "b", cfg.BaseDN)

	cfg = ldapClientConfig(&types.ResolvedLDAPConfig{
		LDAPProviderSettings: types.LDAPProviderSettings{
			Host: "h", BaseDN: "b", TimeoutSeconds: 3,
		},
	})
	require.Equal(t, 3*time.Second, cfg.Timeout)
}

func TestLDAPFailureThrottle(t *testing.T) {
	th := newLDAPFailureThrottle()

	require.True(t, mustAllow(t, th, "alice"), "a fresh key is always allowed")

	// The first four failures do not lock the identifier out.
	for i := 0; i < ldapThrottleMaxFailures-1; i++ {
		th.RecordFailure("alice")
		require.True(t, mustAllow(t, th, "alice"), "failure %d is still under budget", i+1)
	}

	// The fifth exhausts the budget and locks the key.
	th.RecordFailure("alice")
	ok, retryAfter := th.Allow("alice")
	require.False(t, ok, "the identifier is locked out after the budget is spent")
	require.Greater(t, retryAfter, time.Duration(0))

	// Other identifiers are unaffected.
	require.True(t, mustAllow(t, th, "bob"))

	// A successful bind clears the counter.
	th.Reset("alice")
	require.True(t, mustAllow(t, th, "alice"), "Reset clears the lockout")

	// The throttle is shared state; guard against nil-map panics.
	th2 := newLDAPFailureThrottle()
	th2.Reset("never-seen")
	require.True(t, mustAllow(t, th2, "never-seen"))
}

func mustAllow(t *testing.T, th *ldapFailureThrottle, key string) bool {
	t.Helper()
	ok, _ := th.Allow(key)
	return ok
}

func TestNormalizeScopes(t *testing.T) {
	require.Equal(t, []string{"openid", "profile", "email"}, normalizeScopes(nil))
	require.Equal(t, []string{"openid"}, normalizeScopes([]string{"  ", "openid", ""}))
	require.Equal(t, []string{"openid", "profile", "email"},
		normalizeScopes([]string{"openid profile email"}),
		"space-separated scopes are split")
	require.Equal(t, []string{"openid", "profile"},
		normalizeScopes([]string{"openid,profile", "openid"}),
		"duplicates are dropped across entries")
}

func TestEnvOverrideMapMasksSecrets(t *testing.T) {
	clearOIDCEnv(t)
	t.Setenv("OIDC_AUTH_CLIENT_ID", "visible-client")
	t.Setenv("OIDC_AUTH_CLIENT_SECRET", "hidden-secret")

	got := envOverrideMap(oidcEnvNames)
	require.Equal(t, "visible-client", got["OIDC_AUTH_CLIENT_ID"])
	require.Equal(t, "(set)", got["OIDC_AUTH_CLIENT_SECRET"])
	require.NotContains(t, got, "OIDC_AUTH_ISSUER_URL", "unset variables are omitted")
}

// TestSubjectAttributeName covers the label reported by the LDAP test panel.
func TestSubjectAttributeName(t *testing.T) {
	entryUUID := &ldapauth.Entry{Attributes: map[string]string{"entryUUID": "u"}}
	require.Equal(t, "entryUUID", subjectAttributeName(entryUUID))

	objectGUID := &ldapauth.Entry{Attributes: map[string]string{"objectGUID": "g"}}
	require.Equal(t, "objectGUID", subjectAttributeName(objectGUID))

	dnOnly := &ldapauth.Entry{Attributes: map[string]string{"cn": "alice"}}
	require.Equal(t, "dn", subjectAttributeName(dnOnly))
}

func TestTruncateForMessage(t *testing.T) {
	require.Equal(t, "short", truncateForMessage("  short  "))
	require.Equal(t, "abc", truncateForMessage("abc"))
	long := strings.Repeat("x", 250)
	got := truncateForMessage(long)
	require.Len(t, []rune(got), 201, "200 characters plus the ellipsis")
	require.True(t, strings.HasSuffix(got, "…"))
}
