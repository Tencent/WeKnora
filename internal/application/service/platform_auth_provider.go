package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/infrastructure/ldapauth"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// ---------------------------------------------------------------------------
// Platform auth provider service (Settings → 系统管理 → 用户管理)
//
// Two providers, one persistence shape: a `platform_auth_providers` row per
// kind holding non-secret JSON config plus a write-only secret. Resolution is
// always DB → legacy environment → built-in default, so an existing deployment
// that has never opened the new screen keeps the exact behaviour it had before
// the migration (this deployment runs OIDC_AUTH_ENABLE=false and has no row,
// so OIDC simply stays off until an admin configures it in the UI).
// ---------------------------------------------------------------------------

// Environment variable names consulted for the OIDC provider before the DB
// row exists. Mirrors internal/config/config.go's applyOIDCEnvOverrides plus
// the claim-mapping variables so an operator who already set them sees them
// surfaced in the UI as "still inherited from the environment".
var oidcEnvNames = []string{
	"OIDC_AUTH_ENABLE",
	"OIDC_AUTH_ISSUER_URL",
	"OIDC_AUTH_DISCOVERY_URL",
	"OIDC_AUTH_PROVIDER_DISPLAY_NAME",
	"OIDC_AUTH_CLIENT_ID",
	"OIDC_AUTH_CLIENT_SECRET",
	"OIDC_AUTH_AUTHORIZATION_ENDPOINT",
	"OIDC_AUTH_TOKEN_ENDPOINT",
	"OIDC_AUTH_USER_INFO_ENDPOINT",
	"OIDC_AUTH_JWKS_URI",
	"OIDC_AUTH_SCOPES",
	"OIDC_USER_INFO_MAPPING_USER_NAME",
	"OIDC_USER_INFO_MAPPING_EMAIL",
}

// LDAP defaults applied when the operator leaves a field blank.
const (
	defaultLDAPPort        = 389
	defaultLDAPUserFilter  = "(&(objectClass=person)(|(mail=%s)(sAMAccountName=%s)(uid=%s)))"
	defaultLDAPNameAttr    = "sAMAccountName"
	defaultLDAPEmailAttr   = "mail"
	defaultLDAPDisplayAttr = "displayName"

	// ldapThrottleMaxFailures / Window / Lockout bound how hard a single
	// identifier can hammer the directory. Directory servers commonly lock
	// accounts after a handful of bad binds, so WeKnora backs off well before
	// that and keeps the pressure off shared service-account bind pools.
	ldapThrottleMaxFailures = 5
	ldapThrottleWindow      = 5 * time.Minute
	ldapThrottleLockout     = 5 * time.Minute

	// oidcProbeTimeout / oidcProbeMaxBody bound the admin-triggered discovery
	// probe: a hung or oversized IdP document must not wedge the 通用OIDC
	// settings page.
	oidcProbeTimeout = 8 * time.Second
	oidcProbeMaxBody = 256 << 10
)

// platformAuthProviderService implements interfaces.PlatformAuthProviderService.
type platformAuthProviderService struct {
	repo     interfaces.PlatformAuthProviderRepository
	cfg      *config.Config
	throttle *ldapFailureThrottle
}

// NewPlatformAuthProviderService wires the provider service.
func NewPlatformAuthProviderService(
	repo interfaces.PlatformAuthProviderRepository,
	cfg *config.Config,
) interfaces.PlatformAuthProviderService {
	return &platformAuthProviderService{
		repo:     repo,
		cfg:      cfg,
		throttle: newLDAPFailureThrottle(),
	}
}

// ---------------------------------------------------------------------------
// persistence helpers
// ---------------------------------------------------------------------------

// loadRow returns the persisted row for kind, or nil when unset. A DB error is
// logged and treated as "unset" so a broken settings table degrades to the
// environment defaults instead of breaking login entirely.
func (s *platformAuthProviderService) loadRow(
	ctx context.Context, kind types.PlatformAuthProviderKind,
) *types.PlatformAuthProvider {
	row, err := s.repo.GetByKind(ctx, kind)
	if err != nil {
		logger.Warnf(ctx, "[auth_provider] load %s failed, falling back to environment: %v", kind, err)
		return nil
	}
	return row
}

// saveRow encrypts the secret (when a key is configured) and upserts.
func (s *platformAuthProviderService) saveRow(
	ctx context.Context,
	kind types.PlatformAuthProviderKind,
	enabled bool,
	cfg any,
	secret string,
) (*types.PlatformAuthProvider, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to encode %s configuration: %w", kind, err)
	}

	stored := secret
	if stored != "" {
		enc, encErr := secutils.EncryptAESGCM(stored, secutils.GetAESKey())
		if encErr != nil {
			return nil, fmt.Errorf("failed to protect %s credential: %w", kind, encErr)
		}
		stored = enc
	}

	actorID, _ := types.UserIDFromContext(ctx)
	row := &types.PlatformAuthProvider{
		Kind:      kind,
		Enabled:   enabled,
		Config:    types.JSON(raw),
		Secret:    stored,
		UpdatedBy: actorID,
	}
	if err := s.repo.Upsert(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// revealSecret decrypts a stored credential. Values saved before SYSTEM_AES_KEY
// existed (or with the key unset) pass through unchanged.
func revealSecret(stored string) string {
	if stored == "" {
		return ""
	}
	plain, err := secutils.DecryptStoredSecret(stored)
	if err != nil {
		logger.Warnf(context.Background(),
			"[auth_provider] stored credential could not be decrypted (%v); treating as unset", err)
		return ""
	}
	return plain
}

// ---------------------------------------------------------------------------
// OIDC
// ---------------------------------------------------------------------------

// oidcEnvDefaults projects the (env-overridden) startup config into the
// resolved shape, so the environment acts as the pre-migration default.
func (s *platformAuthProviderService) oidcEnvDefaults() *types.ResolvedOIDCConfig {
	out := &types.ResolvedOIDCConfig{
		ProviderDisplayName: "OIDC",
		Scopes:              []string{"openid", "profile", "email"},
		UsernameClaim:       "name",
		EmailClaim:          "email",
	}
	if s.cfg != nil && s.cfg.OIDCAuth != nil {
		o := s.cfg.OIDCAuth
		out.Enabled = o.Enable
		out.IssuerURL = strings.TrimSpace(o.IssuerURL)
		out.DiscoveryURL = strings.TrimSpace(o.DiscoveryURL)
		out.ClientID = strings.TrimSpace(o.ClientID)
		out.ClientSecret = o.ClientSecret
		out.AuthorizationEndpoint = strings.TrimSpace(o.AuthorizationEndpoint)
		out.TokenEndpoint = strings.TrimSpace(o.TokenEndpoint)
		out.UserInfoEndpoint = strings.TrimSpace(o.UserInfoEndpoint)
		out.JwksURI = strings.TrimSpace(o.JwksURI)
		if strings.TrimSpace(o.ProviderDisplayName) != "" {
			out.ProviderDisplayName = strings.TrimSpace(o.ProviderDisplayName)
		}
		if len(o.Scopes) > 0 {
			out.Scopes = append([]string(nil), o.Scopes...)
		}
		if o.UserInfoMapping != nil {
			if v := strings.TrimSpace(o.UserInfoMapping.Username); v != "" {
				out.UsernameClaim = v
			}
			if v := strings.TrimSpace(o.UserInfoMapping.Email); v != "" {
				out.EmailClaim = v
			}
		}
	}
	out.Source = types.AuthProviderSourceEnvironment
	if len(envOverrideMap(oidcEnvNames)) == 0 {
		out.Source = types.AuthProviderSourceDefault
	}
	return out
}

// envOverrideMap returns the subset of `names` that are set on this process,
// with secrets masked. Used to tell the operator which values are still being
// inherited from the environment.
func envOverrideMap(names []string) map[string]string {
	out := map[string]string{}
	for _, name := range names {
		v := strings.TrimSpace(os.Getenv(name))
		if v == "" {
			continue
		}
		if strings.Contains(strings.ToUpper(name), "SECRET") || strings.Contains(strings.ToUpper(name), "PASSWORD") {
			out[name] = "(set)"
			continue
		}
		out[name] = v
	}
	return out
}

// oidcResolvedFromRow projects a persisted provider row into the effective
// configuration.
//
// Once an operator has saved the 通用OIDC screen the row is authoritative for
// every field: no environment value is mixed back in, so a field the operator
// cleared stays cleared and, crucially, changing the issuer cannot silently
// keep pointing at the previous provider's discovery URL. The environment only
// seeds the form the first time — GetOIDCProvider returns the env-derived
// defaults until a row exists, so the first save freezes whatever was in force.
func oidcResolvedFromRow(row *types.PlatformAuthProvider) *types.ResolvedOIDCConfig {
	out := &types.ResolvedOIDCConfig{
		Enabled:             row.Enabled,
		ProviderDisplayName: "OIDC",
		Scopes:              []string{"openid", "profile", "email"},
		UsernameClaim:       "name",
		EmailClaim:          "email",
		Source:              types.AuthProviderSourceDatabase,
	}

	var stored types.OIDCProviderSettings
	if len(row.Config) > 0 {
		if err := json.Unmarshal(row.Config, &stored); err != nil {
			logger.Warnf(context.Background(),
				"[auth_provider] stored oidc config is not valid JSON, treating it as empty: %v", err)
			stored = types.OIDCProviderSettings{}
		}
	}

	out.IssuerURL = strings.TrimSpace(stored.IssuerURL)
	out.DiscoveryURL = strings.TrimSpace(stored.DiscoveryURL)
	out.ClientID = strings.TrimSpace(stored.ClientID)
	out.AuthorizationEndpoint = strings.TrimSpace(stored.AuthorizationEndpoint)
	out.TokenEndpoint = strings.TrimSpace(stored.TokenEndpoint)
	out.UserInfoEndpoint = strings.TrimSpace(stored.UserInfoEndpoint)
	out.JwksURI = strings.TrimSpace(stored.JwksURI)
	if v := strings.TrimSpace(stored.ProviderDisplayName); v != "" {
		out.ProviderDisplayName = v
	}
	if len(stored.Scopes) > 0 {
		out.Scopes = append([]string(nil), stored.Scopes...)
	}
	if v := strings.TrimSpace(stored.UsernameClaim); v != "" {
		out.UsernameClaim = v
	}
	if v := strings.TrimSpace(stored.EmailClaim); v != "" {
		out.EmailClaim = v
	}
	out.ClientSecret = revealSecret(row.Secret)

	// Derive discovery from the issuer the operator actually saved (never from
	// the environment) so switching IdP cannot leave the old well-known URL in
	// place.
	if out.DiscoveryURL == "" && out.IssuerURL != "" {
		out.DiscoveryURL = strings.TrimRight(out.IssuerURL, "/") + "/.well-known/openid-configuration"
	}
	return out
}

// oidcEffective returns the configuration currently in force, plus the row it
// came from (nil when the deployment is still running on environment only).
func (s *platformAuthProviderService) oidcEffective(
	ctx context.Context,
) (*types.ResolvedOIDCConfig, *types.PlatformAuthProvider) {
	row := s.loadRow(ctx, types.AuthProviderKindOIDC)
	if row != nil {
		return oidcResolvedFromRow(row), row
	}
	return s.oidcEnvDefaults(), nil
}

// ResolveOIDCConfig implements the login-path resolution. A disabled provider
// resolves to nil so every caller can treat it as "not configured".
func (s *platformAuthProviderService) ResolveOIDCConfig(ctx context.Context) *types.ResolvedOIDCConfig {
	cfg, _ := s.oidcEffective(ctx)
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	return cfg
}

// GetOIDCProvider returns the admin view. HasSecret is the only thing the
// caller learns about the credential — the value itself never leaves the
// server.
func (s *platformAuthProviderService) GetOIDCProvider(
	ctx context.Context,
) (*types.OIDCProviderView, error) {
	cfg, row := s.oidcEffective(ctx)

	view := &types.OIDCProviderView{
		Success:               true,
		Enabled:               cfg.Enabled,
		IssuerURL:             cfg.IssuerURL,
		DiscoveryURL:          cfg.DiscoveryURL,
		ProviderDisplayName:   cfg.ProviderDisplayName,
		ClientID:              cfg.ClientID,
		HasSecret:             cfg.ClientSecret != "",
		AuthorizationEndpoint: cfg.AuthorizationEndpoint,
		TokenEndpoint:         cfg.TokenEndpoint,
		UserInfoEndpoint:      cfg.UserInfoEndpoint,
		JwksURI:               cfg.JwksURI,
		Scopes:                cfg.Scopes,
		UsernameClaim:         cfg.UsernameClaim,
		EmailClaim:            cfg.EmailClaim,
		Source:                cfg.Source,
		// Reported even when a row exists, so the operator can see which
		// legacy variables are set but no longer in effect.
		EnvOverrides: envOverrideMap(oidcEnvNames),
	}
	if view.ProviderDisplayName == "" {
		view.ProviderDisplayName = "OIDC"
	}
	if len(view.Scopes) == 0 {
		view.Scopes = []string{"openid", "profile", "email"}
	}
	if row != nil {
		updatedAt := row.UpdatedAt
		view.UpdatedAt = &updatedAt
		view.UpdatedBy = row.UpdatedBy
	}
	return view, nil
}

// UpdateOIDCProvider validates + persists the OIDC provider.
func (s *platformAuthProviderService) UpdateOIDCProvider(
	ctx context.Context, req *types.OIDCProviderUpdateRequest,
) (*types.OIDCProviderView, error) {
	if req == nil {
		return nil, errors.New("missing request body")
	}

	stored := types.OIDCProviderSettings{
		IssuerURL:             strings.TrimSpace(req.IssuerURL),
		DiscoveryURL:          strings.TrimSpace(req.DiscoveryURL),
		ProviderDisplayName:   strings.TrimSpace(req.ProviderDisplayName),
		ClientID:              strings.TrimSpace(req.ClientID),
		AuthorizationEndpoint: strings.TrimSpace(req.AuthorizationEndpoint),
		TokenEndpoint:         strings.TrimSpace(req.TokenEndpoint),
		UserInfoEndpoint:      strings.TrimSpace(req.UserInfoEndpoint),
		JwksURI:               strings.TrimSpace(req.JwksURI),
		Scopes:                normalizeScopes(req.Scopes),
		UsernameClaim:         strings.TrimSpace(req.UsernameClaim),
		EmailClaim:            strings.TrimSpace(req.EmailClaim),
	}
	if stored.ProviderDisplayName == "" {
		stored.ProviderDisplayName = "OIDC"
	}

	// Enabled providers must be resolvable end-to-end, otherwise the login
	// button would render and then fail on click. Endpoints may still be
	// discovered at request time, so only the minimum identity is required.
	if req.Enabled {
		if stored.ClientID == "" {
			return nil, errors.New("client_id is required when OIDC is enabled")
		}
		if stored.IssuerURL == "" && stored.DiscoveryURL == "" &&
			(stored.AuthorizationEndpoint == "" || stored.TokenEndpoint == "") {
			return nil, errors.New(
				"issuer_url (or discovery_url, or explicit authorization/token endpoints) is required when OIDC is enabled")
		}
	}

	prev := s.loadRow(ctx, types.AuthProviderKindOIDC)
	secret := ""
	secretChanged := false
	switch {
	case req.ClearSecret:
		secret = ""
		secretChanged = prev != nil && prev.Secret != ""
	case req.ClientSecret != nil && strings.TrimSpace(*req.ClientSecret) != "":
		secret = strings.TrimSpace(*req.ClientSecret)
		secretChanged = true
	default:
		// Preserve the existing credential — the UI never receives it, so a
		// save without re-typing the secret must not wipe it.
		if prev != nil {
			secret = prev.Secret
		}
	}

	row, err := s.saveRow(ctx, types.AuthProviderKindOIDC, req.Enabled, stored, secret)
	if err != nil {
		return nil, err
	}

	// Re-derive via the read path so the caller sees exactly what a later GET
	// would return (including resolved endpoint discovery behaviour).
	view, err := s.GetOIDCProvider(ctx)
	if err != nil {
		return nil, err
	}
	logger.Infof(ctx, "OIDC provider updated (enabled=%t, secret_changed=%t, by=%s)",
		req.Enabled, secretChanged, row.UpdatedBy)
	return view, nil
}

func normalizeScopes(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
			p := strings.TrimSpace(part)
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{"openid", "profile", "email"}
	}
	return out
}

// fetchOIDCDiscoveryProbe loads the well-known document so the 通用OIDC test
// panel can show what the IdP actually advertises, rather than only what the
// operator typed into the form.
func fetchOIDCDiscoveryProbe(ctx context.Context, discoveryURL string) (*oidcDiscoveryProbe, error) {
	reqCtx, cancel := context.WithTimeout(ctx, oidcProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造发现文档请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := newOIDCHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法访问发现文档: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, oidcProbeMaxBody))
	if err != nil {
		return nil, fmt.Errorf("读取发现文档失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("发现文档返回 HTTP %d: %s",
			resp.StatusCode, truncateForMessage(string(body)))
	}

	var probe oidcDiscoveryProbe
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, fmt.Errorf("发现文档不是合法 JSON: %w", err)
	}
	return &probe, nil
}

// oidcDiscoveryProbe is the superset of discovery fields the test panel shows.
type oidcDiscoveryProbe struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	UserInfoEndpoint              string   `json:"userinfo_endpoint"`
	JwksURI                       string   `json:"jwks_uri"`
	TokenEndpointAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

// TestOIDCProvider probes the configured endpoints without mutating state.
func (s *platformAuthProviderService) TestOIDCProvider(ctx context.Context) *types.OIDCTestResponse {
	resp := &types.OIDCTestResponse{}

	cfg, _ := s.oidcEffective(ctx)

	resp.Issuer = cfg.IssuerURL
	resp.AuthorizationEndpoint = cfg.AuthorizationEndpoint
	resp.TokenEndpoint = cfg.TokenEndpoint
	resp.UserInfoEndpoint = cfg.UserInfoEndpoint
	resp.JwksURI = cfg.JwksURI

	discoveryURL := cfg.DiscoveryURL
	if discoveryURL == "" && cfg.IssuerURL != "" {
		discoveryURL = strings.TrimRight(cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	}
	if discoveryURL == "" {
		// No discovery document — report the manually configured endpoints
		// instead of failing.
		if cfg.AuthorizationEndpoint == "" || cfg.TokenEndpoint == "" {
			resp.Message = "未配置 issuer_url / discovery_url，且缺少 authorization/token 端点"
			return resp
		}
		resp.Success = true
		resp.Message = "使用显式配置的端点（未做联网探测）"
		resp.Warnings = append(resp.Warnings, "未配置 discovery_url，跳过联网发现")
		return resp
	}

	if err := validateOIDCEndpoint("discovery", discoveryURL, true); err != nil {
		resp.Message = err.Error()
		return resp
	}

	probe, err := fetchOIDCDiscoveryProbe(ctx, discoveryURL)
	if err != nil {
		resp.Message = err.Error()
		return resp
	}

	resp.Success = true
	resp.DiscoveryOK = true
	resp.Message = "发现文档加载成功"
	if probe.Issuer != "" {
		resp.Issuer = probe.Issuer
	}
	if probe.AuthorizationEndpoint != "" {
		resp.AuthorizationEndpoint = probe.AuthorizationEndpoint
	}
	if probe.TokenEndpoint != "" {
		resp.TokenEndpoint = probe.TokenEndpoint
	}
	if probe.UserInfoEndpoint != "" {
		resp.UserInfoEndpoint = probe.UserInfoEndpoint
	}
	if probe.JwksURI != "" {
		resp.JwksURI = probe.JwksURI
	}
	resp.TokenEndpointAuthMethods = probe.TokenEndpointAuthMethods

	if resp.AuthorizationEndpoint == "" || resp.TokenEndpoint == "" {
		resp.Success = false
		resp.Message = "发现文档缺少 authorization_endpoint 或 token_endpoint"
	}
	if len(probe.TokenEndpointAuthMethods) > 0 &&
		!containsString(probe.TokenEndpointAuthMethods, "client_secret_post") &&
		!containsString(probe.TokenEndpointAuthMethods, "client_secret_basic") {
		resp.Warnings = append(resp.Warnings,
			"IdP 未声明 client_secret_post/basic，若使用机密客户端可能无法换取 token")
	}
	return resp
}

func truncateForMessage(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// ---------------------------------------------------------------------------
// LDAP
// ---------------------------------------------------------------------------

// GetLDAPProvider returns the admin view.
func (s *platformAuthProviderService) GetLDAPProvider(
	ctx context.Context,
) (*types.LDAPProviderView, error) {
	stored, enabled, row := s.ldapStoredSettings(ctx)

	view := &types.LDAPProviderView{
		Success:           true,
		Enabled:           enabled,
		Host:              stored.Host,
		Port:              stored.Port,
		UseTLS:            stored.UseTLS,
		StartTLS:          stored.StartTLS,
		SkipTLSVerify:     stored.SkipTLSVerify,
		BindDN:            stored.BindDN,
		UserFilter:        stored.UserFilter,
		AutoCreateUser:    stored.AutoCreateUser,
		DefaultTenantMode: stored.DefaultTenantMode,
		TimeoutSeconds:    stored.TimeoutSeconds,
		Source:            types.AuthProviderSourceDefault,
	}
	_ = s.fillLDAPDefaults(view, stored)
	if row != nil {
		view.Source = types.AuthProviderSourceDatabase
		view.HasBindPassword = row.Secret != ""
		updatedAt := row.UpdatedAt
		view.UpdatedAt = &updatedAt
		view.UpdatedBy = row.UpdatedBy
	}
	view.Enabled = enabled
	view.Success = true
	return view, nil
}

// ldapStoredSettings loads the LDAP settings merged over the built-in
// defaults. Returns the merged settings, the effective `enabled` flag, and the
// raw row (nil when the deployment has never saved the provider).
func (s *platformAuthProviderService) ldapStoredSettings(
	ctx context.Context,
) (types.LDAPProviderSettings, bool, *types.PlatformAuthProvider) {
	row := s.loadRow(ctx, types.AuthProviderKindLDAP)
	settings := types.LDAPProviderSettings{
		Port:              defaultLDAPPort,
		UserFilter:        defaultLDAPUserFilter,
		UserNameAttr:      defaultLDAPNameAttr,
		UserEmailAttr:     defaultLDAPEmailAttr,
		UserDisplayAttr:   defaultLDAPDisplayAttr,
		DefaultTenantMode: "",
	}
	if row == nil {
		return settings, false, nil
	}
	var stored types.LDAPProviderSettings
	if len(row.Config) > 0 {
		if err := json.Unmarshal(row.Config, &stored); err != nil {
			logger.Warnf(ctx, "[auth_provider] ldap config is not valid JSON, ignoring: %v", err)
			stored = types.LDAPProviderSettings{}
		}
	}
	if v := strings.TrimSpace(stored.Host); v != "" {
		settings.Host = v
	}
	if stored.Port > 0 {
		settings.Port = stored.Port
	}
	settings.UseTLS = stored.UseTLS
	settings.StartTLS = stored.StartTLS
	settings.SkipTLSVerify = stored.SkipTLSVerify
	if v := strings.TrimSpace(stored.BindDN); v != "" {
		settings.BindDN = v
	}
	if v := strings.TrimSpace(stored.BaseDN); v != "" {
		settings.BaseDN = v
	}
	if v := strings.TrimSpace(stored.UserSearchBase); v != "" {
		settings.UserSearchBase = v
	}
	if v := strings.TrimSpace(stored.UserFilter); v != "" {
		settings.UserFilter = v
	}
	if v := strings.TrimSpace(stored.UserNameAttr); v != "" {
		settings.UserNameAttr = v
	}
	if v := strings.TrimSpace(stored.UserEmailAttr); v != "" {
		settings.UserEmailAttr = v
	}
	if v := strings.TrimSpace(stored.UserDisplayAttr); v != "" {
		settings.UserDisplayAttr = v
	}
	settings.AutoCreateUser = stored.AutoCreateUser
	settings.DefaultTenantMode = strings.TrimSpace(stored.DefaultTenantMode)
	settings.TimeoutSeconds = stored.TimeoutSeconds
	return settings, row.Enabled, row
}

// fillLDAPDefaults copies the settings into the view, defaulting blank
// attribute names so the form always shows a usable value.
func (s *platformAuthProviderService) fillLDAPDefaults(
	view *types.LDAPProviderView, in types.LDAPProviderSettings,
) error {
	view.BaseDN = in.BaseDN
	view.UserSearchBase = in.UserSearchBase
	view.UserNameAttr = orDefault(in.UserNameAttr, defaultLDAPNameAttr)
	view.UserEmailAttr = orDefault(in.UserEmailAttr, defaultLDAPEmailAttr)
	view.UserDisplayAttr = orDefault(in.UserDisplayAttr, defaultLDAPDisplayAttr)
	view.UserFilter = orDefault(in.UserFilter, defaultLDAPUserFilter)
	if view.Port <= 0 {
		view.Port = defaultLDAPPort
	}
	return nil
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

// ResolveLDAPConfig implements the login-path resolution.
func (s *platformAuthProviderService) ResolveLDAPConfig(ctx context.Context) *types.ResolvedLDAPConfig {
	settings, enabled, row := s.ldapStoredSettings(ctx)
	if row == nil || !enabled {
		return nil
	}
	if strings.TrimSpace(settings.Host) == "" || strings.TrimSpace(settings.BaseDN) == "" {
		logger.Warnf(ctx, "[auth_provider] ldap enabled but host/base_dn missing; skipping directory login")
		return nil
	}
	return &types.ResolvedLDAPConfig{
		LDAPProviderSettings: settings,
		BindPassword:         revealSecret(row.Secret),
		Source:               types.AuthProviderSourceDatabase,
	}
}

// ldapClientConfig converts a resolved config into the transport config.
func ldapClientConfig(cfg *types.ResolvedLDAPConfig) ldapauth.Config {
	timeout := ldapauth.DefaultTimeout
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	return ldapauth.Config{
		Host:            cfg.Host,
		Port:            cfg.Port,
		UseTLS:          cfg.UseTLS,
		StartTLS:        cfg.StartTLS,
		SkipTLSVerify:   cfg.SkipTLSVerify,
		BindDN:          cfg.BindDN,
		BindPassword:    cfg.BindPassword,
		BaseDN:          cfg.BaseDN,
		UserSearchBase:  cfg.UserSearchBase,
		UserFilter:      cfg.UserFilter,
		UserNameAttr:    cfg.UserNameAttr,
		UserEmailAttr:   cfg.UserEmailAttr,
		UserDisplayAttr: cfg.UserDisplayAttr,
		Timeout:         timeout,
	}
}

// UpdateLDAPProvider validates + persists the LDAP provider.
func (s *platformAuthProviderService) UpdateLDAPProvider(
	ctx context.Context, req *types.LDAPProviderUpdateRequest,
) (*types.LDAPProviderView, error) {
	if req == nil {
		return nil, errors.New("missing request body")
	}

	stored := types.LDAPProviderSettings{
		Host:              strings.TrimSpace(req.Host),
		Port:              req.Port,
		UseTLS:            req.UseTLS,
		StartTLS:          req.StartTLS,
		SkipTLSVerify:     req.SkipTLSVerify,
		BindDN:            strings.TrimSpace(req.BindDN),
		BaseDN:            strings.TrimSpace(req.BaseDN),
		UserSearchBase:    strings.TrimSpace(req.UserSearchBase),
		UserFilter:        strings.TrimSpace(req.UserFilter),
		UserNameAttr:      strings.TrimSpace(req.UserNameAttr),
		UserEmailAttr:     strings.TrimSpace(req.UserEmailAttr),
		UserDisplayAttr:   strings.TrimSpace(req.UserDisplayAttr),
		AutoCreateUser:    req.AutoCreateUser,
		DefaultTenantMode: strings.TrimSpace(req.DefaultTenantMode),
		TimeoutSeconds:    req.TimeoutSeconds,
	}
	if stored.Port <= 0 {
		stored.Port = defaultLDAPPort
	}
	if stored.UserFilter == "" {
		stored.UserFilter = defaultLDAPUserFilter
	}
	if stored.UserNameAttr == "" {
		stored.UserNameAttr = defaultLDAPNameAttr
	}
	if stored.UserEmailAttr == "" {
		stored.UserEmailAttr = defaultLDAPEmailAttr
	}
	if stored.UserDisplayAttr == "" {
		stored.UserDisplayAttr = defaultLDAPDisplayAttr
	}
	if stored.DefaultTenantMode != "" &&
		stored.DefaultTenantMode != string(types.TenantProvisioningCreatePersonal) &&
		stored.DefaultTenantMode != string(types.TenantProvisioningTenantless) {
		return nil, fmt.Errorf("default_tenant_mode must be %q or %q",
			types.TenantProvisioningCreatePersonal, types.TenantProvisioningTenantless)
	}
	if strings.Contains(stored.Host, "://") {
		return nil, errors.New("host must be a bare hostname or IP, without a scheme")
	}
	if req.Enabled {
		if stored.Host == "" {
			return nil, errors.New("host is required when LDAP is enabled")
		}
		if stored.BaseDN == "" {
			return nil, errors.New("base_dn is required when LDAP is enabled")
		}
		if stored.UseTLS && stored.StartTLS {
			return nil, errors.New("use_tls (ldaps://) and start_tls are mutually exclusive")
		}
	}

	prev := s.loadRow(ctx, types.AuthProviderKindLDAP)
	secret := ""
	secretChanged := false
	switch {
	case req.ClearBindPassword:
		secretChanged = prev != nil && prev.Secret != ""
		secret = ""
	case req.BindPassword != nil && *req.BindPassword != "":
		secret = *req.BindPassword
		secretChanged = true
	default:
		if prev != nil {
			secret = prev.Secret
		}
	}

	row, err := s.saveRow(ctx, types.AuthProviderKindLDAP, req.Enabled, stored, secret)
	if err != nil {
		return nil, err
	}
	view, err := s.GetLDAPProvider(ctx)
	if err != nil {
		return nil, err
	}
	logger.Infof(ctx, "LDAP provider updated (enabled=%t, secret_changed=%t, by=%s)",
		req.Enabled, secretChanged, row.UpdatedBy)
	return view, nil
}

// TestLDAPProvider probes connectivity, optional user search and optional bind.
func (s *platformAuthProviderService) TestLDAPProvider(
	ctx context.Context, req *types.LDAPTestRequest,
) *types.LDAPTestResponse {
	resp := &types.LDAPTestResponse{}

	settings, _, row := s.ldapStoredSettings(ctx)
	if row == nil {
		resp.Message = "LDAP 尚未配置，请先保存配置再测试"
		return resp
	}
	if strings.TrimSpace(settings.Host) == "" || strings.TrimSpace(settings.BaseDN) == "" {
		resp.Message = "host 与 base_dn 为必填项"
		return resp
	}

	bindPassword := revealSecret(row.Secret)
	clientCfg := ldapauth.Config{
		Host:            settings.Host,
		Port:            settings.Port,
		UseTLS:          settings.UseTLS,
		StartTLS:        settings.StartTLS,
		SkipTLSVerify:   settings.SkipTLSVerify,
		BindDN:          settings.BindDN,
		BindPassword:    bindPassword,
		BaseDN:          settings.BaseDN,
		UserSearchBase:  settings.UserSearchBase,
		UserFilter:      settings.UserFilter,
		UserNameAttr:    settings.UserNameAttr,
		UserEmailAttr:   settings.UserEmailAttr,
		UserDisplayAttr: settings.UserDisplayAttr,
		Timeout:         ldapauth.DefaultTimeout,
	}
	if settings.TimeoutSeconds > 0 {
		clientCfg.Timeout = time.Duration(settings.TimeoutSeconds) * time.Second
	}
	if !settings.UseTLS {
		resp.Warnings = append(resp.Warnings, "未启用 TLS，绑定密码将以明文发送")
	} else if settings.SkipTLSVerify {
		resp.Warnings = append(resp.Warnings, "已跳过证书校验，仅建议用于测试环境")
	}

	conn, err := ldapauth.ServiceBind(clientCfg)
	if err != nil {
		resp.Message = fmt.Sprintf("服务账号绑定失败: %v", err)
		return resp
	}
	defer func() { _ = conn.Close() }()

	resp.ServiceOK = true
	resp.Success = true
	resp.Message = "服务账号绑定成功"

	if detail, derr := ldapauth.DetectSubjectAttribute(conn, clientCfg); derr == nil && detail != "" {
		resp.DetectedSubjectAttribute = detail
	}

	identifier := strings.TrimSpace(req.Username)
	if identifier == "" {
		return resp
	}

	entry, err := ldapauth.SearchUser(conn, clientCfg, identifier)
	if err != nil {
		resp.Success = false
		resp.Message = fmt.Sprintf("用户检索失败: %v", err)
		return resp
	}
	resp.UserFound = true
	resp.UserDN = entry.DN
	resp.Attributes = entry.Attributes
	if entry.Subject != "" {
		resp.DetectedSubjectAttribute = subjectAttributeName(entry)
	}
	resp.Message = "服务绑定 + 用户检索成功"

	if req.Password == "" {
		resp.Warnings = append(resp.Warnings, "未提供密码，跳过用户绑定验证")
		return resp
	}
	if err := ldapauth.VerifyPassword(clientCfg, entry.DN, req.Password); err != nil {
		resp.Success = false
		resp.Message = fmt.Sprintf("用户绑定失败: %v", err)
		return resp
	}
	resp.Message = "服务绑定 + 用户检索 + 用户绑定全部通过"
	return resp
}

// subjectAttributeName reports which attribute produced the stable subject id.
func subjectAttributeName(entry *ldapauth.Entry) string {
	if _, ok := entry.Attributes["entryUUID"]; ok {
		return "entryUUID"
	}
	if _, ok := entry.Attributes["objectGUID"]; ok {
		return "objectGUID"
	}
	return "dn"
}

// AuthenticateLDAP resolves the entry for `identifier` and verifies the
// password. Every failure mode collapses to (nil, nil) so the login flow can
// report a single "invalid credentials" without revealing which step failed —
// mirroring the anti-enumeration behaviour of the local password path.
func (s *platformAuthProviderService) AuthenticateLDAP(
	ctx context.Context, identifier, password string,
) (*types.LDAPUserInfo, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" || password == "" {
		return nil, nil
	}

	resolved := s.ResolveLDAPConfig(ctx)
	if resolved == nil {
		return nil, nil
	}

	throttleKey := strings.ToLower(identifier)
	if allowed, retryAfter := s.throttle.Allow(throttleKey); !allowed {
		logger.Warnf(ctx, "[auth_provider] ldap attempts for %s throttled for %s",
			secutils.SanitizeForLog(identifier), retryAfter.Round(time.Second))
		return nil, nil
	}

	clientCfg := ldapClientConfig(resolved)

	conn, err := ldapauth.ServiceBind(clientCfg)
	if err != nil {
		logger.Warnf(ctx, "[auth_provider] ldap service bind failed: %v", err)
		return nil, nil
	}
	defer func() { _ = conn.Close() }()

	entry, err := ldapauth.SearchUser(conn, clientCfg, identifier)
	if err != nil {
		if errors.Is(err, ldapauth.ErrNoUserMatch) {
			s.throttle.RecordFailure(throttleKey)
			logger.Infof(ctx, "[auth_provider] ldap: no directory entry for the supplied identifier")
		} else {
			logger.Warnf(ctx, "[auth_provider] ldap search failed: %v", err)
		}
		return nil, nil
	}

	if err := ldapauth.VerifyPassword(clientCfg, entry.DN, password); err != nil {
		s.throttle.RecordFailure(throttleKey)
		logger.Infof(ctx, "[auth_provider] ldap: user bind rejected for %s",
			secutils.SanitizeForLog(identifier))
		return nil, nil
	}
	s.throttle.Reset(throttleKey)

	return &types.LDAPUserInfo{
		DN:          entry.DN,
		SubjectID:   entry.Subject,
		Username:    entry.Username,
		Email:       entry.Email,
		DisplayName: entry.Display,
		Attributes:  entry.Attributes,
	}, nil
}

// ---------------------------------------------------------------------------
// failure throttle
// ---------------------------------------------------------------------------

// ldapFailureThrottle bounds how many times a single identifier may be tried
// against the directory inside a rolling window. It is intentionally
// in-process: the goal is to protect the directory from a single misbehaving
// client, not to be a distributed rate limiter.
type ldapFailureThrottle struct {
	mu      sync.Mutex
	entries map[string]*ldapThrottleEntry
}

type ldapThrottleEntry struct {
	failures  int
	firstAt   time.Time
	lockedFor time.Time
}

func newLDAPFailureThrottle() *ldapFailureThrottle {
	return &ldapFailureThrottle{entries: map[string]*ldapThrottleEntry{}}
}

// Allow reports whether another attempt is permitted, plus how long the caller
// must wait when it is not.
func (t *ldapFailureThrottle) Allow(key string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	e, ok := t.entries[key]
	if !ok {
		return true, 0
	}
	if now.Sub(e.firstAt) > ldapThrottleWindow {
		delete(t.entries, key)
		return true, 0
	}
	if now.Before(e.lockedFor) {
		return false, e.lockedFor.Sub(now)
	}
	return true, 0
}

// RecordFailure bumps the counter and locks the identifier out once the
// per-window budget is exhausted.
func (t *ldapFailureThrottle) RecordFailure(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	e, ok := t.entries[key]
	if !ok || now.Sub(e.firstAt) > ldapThrottleWindow {
		t.entries[key] = &ldapThrottleEntry{failures: 1, firstAt: now}
		return
	}
	e.failures++
	if e.failures >= ldapThrottleMaxFailures {
		e.lockedFor = now.Add(ldapThrottleLockout)
		e.failures = 0
		e.firstAt = now
	}
}

// Reset clears the counter after a successful bind.
func (t *ldapFailureThrottle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}
