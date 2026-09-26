// Package oauth runs the OAuth 2.0 authorization code flow behind plugin form
// fields that declare x-oauth. The platform's OAuth app (client ID and
// secret in the plugin's system configuration) authorizes an account; the
// tokens are kept sealed in plugin_oauth_connections and the field holds
// only "oauth:<connection id>". When WeKnora calls the plugin it swaps the
// reference for a fresh access token, refreshing it when it is about to
// expire.
package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// CallbackPath is where authorization servers send the browser back.
const CallbackPath = "/api/v1/plugin-oauth/callback"

// refreshBefore refreshes tokens this close to expiring.
const refreshBefore = time.Minute

// Scopes a form edits, as pluginapi.OptionsScope*.
const (
	ScopeSystem   = "system"
	ScopeTenant   = "tenant"
	ScopeInstance = "instance"
)

// ErrNotConnected means a reference names no usable connection.
var ErrNotConnected = errors.New("the OAuth connection is gone; connect again")

// Target is the form field an authorization is for.
type Target struct {
	PluginID string
	// TenantID is 0 for the system configuration.
	TenantID     uint64
	Scope        string
	Contribution string // "<point>/<id>" for instance forms
	Field        string // dotted path
}

// Service runs the flow and resolves references.
type Service struct {
	registry *registry.Registry
	plugins  interfaces.PluginRepository
	repo     interfaces.PluginOAuthRepository
	states   *stateStore
	http     *http.Client
	now      func() time.Time
}

// NewService creates the service; rdb may be nil (single node).
func NewService(
	reg *registry.Registry, plugins interfaces.PluginRepository, repo interfaces.PluginOAuthRepository,
	rdb *redis.Client,
) *Service {
	cfg := utils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = 30 * time.Second
	return &Service{
		registry: reg, plugins: plugins, repo: repo, states: newStateStore(rdb),
		http: utils.NewSSRFSafeHTTPClient(cfg), now: time.Now,
	}
}

// spec finds the x-oauth spec of a target's field.
func (s *Service) spec(m *manifest.Manifest, t Target) (*configschema.OAuthSpec, error) {
	var raw json.RawMessage
	switch t.Scope {
	case ScopeSystem:
		raw = m.Config.SystemSchema
	case ScopeTenant:
		raw = m.Config.TenantSchema
	case ScopeInstance:
		point, id, _ := strings.Cut(t.Contribution, "/")
		for _, c := range m.Contributes[manifest.Point(point)] {
			if c.ID == id {
				raw = c.InstanceSchemaJSON
			}
		}
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("no %s form %q in %s", t.Scope, t.Contribution, m.ID)
	}
	schema, err := configschema.Parse(raw)
	if err != nil {
		return nil, err
	}
	spec, ok := schema.OAuthFields()[t.Field]
	if !ok {
		return nil, fmt.Errorf("field %s has no x-oauth", t.Field)
	}
	return spec, nil
}

// config builds the OAuth client, with its credentials from the plugin's
// system configuration.
func (s *Service) config(ctx context.Context, m *manifest.Manifest, spec *configschema.OAuthSpec, redirect string) (
	*oauth2.Config, error,
) {
	system, _, err := install.OpenSystemConfig(ctx, s.plugins, m)
	if err != nil {
		return nil, err
	}
	lookup := func(scope, key string) (string, bool) {
		if scope != manifest.ScopeSystem {
			return "", false
		}
		v, ok := system[key].(string)
		return v, ok && v != ""
	}
	id, err := manifest.ExpandTemplate(spec.ClientID, lookup)
	if err != nil {
		return nil, fmt.Errorf("the platform has not set up this plugin's OAuth app: %w", err)
	}
	secret := ""
	if spec.ClientSecret != "" {
		if secret, err = manifest.ExpandTemplate(spec.ClientSecret, lookup); err != nil {
			return nil, fmt.Errorf("the platform has not set up this plugin's OAuth app: %w", err)
		}
	}
	return &oauth2.Config{
		ClientID: id, ClientSecret: secret, Scopes: spec.Scopes, RedirectURL: redirect,
		Endpoint: oauth2.Endpoint{AuthURL: spec.AuthorizeURL, TokenURL: spec.TokenURL},
	}, nil
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Start begins an authorization for a field and returns the URL to open and
// the state that will come back. origin is the app's origin, where the
// callback page reports the result; baseURL is where the authorization
// server reaches WeKnora.
func (s *Service) Start(ctx context.Context, t Target, userID, origin, baseURL string) (string, string, error) {
	m, ok := s.registry.Plugin(t.PluginID)
	if !ok {
		return "", "", fmt.Errorf("plugin %s is not loaded", t.PluginID)
	}
	spec, err := s.spec(m, t)
	if err != nil {
		return "", "", err
	}
	redirect := strings.TrimSuffix(baseURL, "/") + CallbackPath
	cfg, err := s.config(ctx, m, spec, redirect)
	if err != nil {
		return "", "", err
	}
	state := randomToken()
	st := pending{Target: t, UserID: userID, Origin: origin, Redirect: redirect}
	var opts []oauth2.AuthCodeOption
	if spec.PKCE {
		st.Verifier = oauth2.GenerateVerifier()
		opts = append(opts, oauth2.S256ChallengeOption(st.Verifier))
	}
	for k, v := range spec.Params {
		opts = append(opts, oauth2.SetAuthURLParam(k, v))
	}
	opts = append(opts, oauth2.AccessTypeOffline)
	if err := s.states.put(ctx, state, st); err != nil {
		return "", "", err
	}
	return cfg.AuthCodeURL(state, opts...), state, nil
}

// Result is how an authorization ended, for the callback page.
type Result struct {
	State        string
	Origin       string
	ConnectionID string
	Err          error
}

// Complete exchanges the code the authorization server sent back and keeps
// the tokens as a new connection.
func (s *Service) Complete(ctx context.Context, state, code, errParam string) Result {
	st, ok, err := s.states.take(ctx, state)
	if err != nil || !ok {
		return Result{State: state, Err: errors.New("this authorization expired or was already used; try again")}
	}
	res := Result{State: state, Origin: st.Origin}
	if errParam != "" {
		res.Err = fmt.Errorf("the authorization was refused: %s", errParam)
		return res
	}
	m, ok := s.registry.Plugin(st.Target.PluginID)
	if !ok {
		res.Err = fmt.Errorf("plugin %s is not loaded", st.Target.PluginID)
		return res
	}
	spec, err := s.spec(m, st.Target)
	if err == nil {
		var cfg *oauth2.Config
		if cfg, err = s.config(ctx, m, spec, st.Redirect); err == nil {
			var opts []oauth2.AuthCodeOption
			if st.Verifier != "" {
				opts = append(opts, oauth2.VerifierOption(st.Verifier))
			}
			var tok *oauth2.Token
			if tok, err = cfg.Exchange(context.WithValue(ctx, oauth2.HTTPClient, s.http), code, opts...); err == nil {
				res.ConnectionID, err = s.save(ctx, st, tok)
			}
		}
	}
	if err != nil {
		logger.Warnf(ctx, "[plugin] OAuth for %s %s: %v", st.Target.PluginID, st.Target.Field, err)
		res.Err = err
	}
	return res
}

func (s *Service) save(ctx context.Context, st pending, tok *oauth2.Token) (string, error) {
	sealed, err := seal(tok)
	if err != nil {
		return "", err
	}
	now := s.now()
	c := &types.PluginOAuthConnection{
		ID: uuid.NewString(), PluginID: st.Target.PluginID, TenantID: st.Target.TenantID, Scope: st.Target.Scope,
		Contribution: st.Target.Contribution, Field: st.Target.Field, Token: sealed, CreatedBy: st.UserID,
		CreatedAt: now, UpdatedAt: now,
	}
	if !tok.Expiry.IsZero() {
		exp := tok.Expiry
		c.ExpiresAt = &exp
	}
	return c.ID, s.repo.Create(ctx, c)
}

func seal(tok *oauth2.Token) (string, error) {
	raw, err := json.Marshal(tok)
	if err != nil {
		return "", err
	}
	return utils.EncryptAESGCM(string(raw), utils.GetAESKey())
}

func open(sealed string) (*oauth2.Token, error) {
	raw, err := utils.DecryptStoredSecret(sealed)
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	return &tok, json.Unmarshal([]byte(raw), &tok)
}

// AccessToken resolves a field value "oauth:<id>" of a plugin in a tenant to
// a current access token, refreshing it when it is about to expire.
func (s *Service) AccessToken(ctx context.Context, pluginID string, tenantID uint64, ref string) (string, error) {
	id := strings.TrimPrefix(ref, configschema.OAuthRefPrefix)
	c, err := s.repo.Get(ctx, pluginID, tenantID, id)
	if err != nil {
		return "", err
	}
	if c == nil {
		return "", ErrNotConnected
	}
	tok, err := open(c.Token)
	if err != nil {
		return "", err
	}
	if tok.Expiry.IsZero() || tok.Expiry.After(s.now().Add(refreshBefore)) {
		return tok.AccessToken, nil
	}
	if tok.RefreshToken == "" {
		return "", ErrNotConnected
	}
	m, ok := s.registry.Plugin(pluginID)
	if !ok {
		return "", ErrNotConnected
	}
	spec, err := s.spec(m, Target{PluginID: pluginID, Scope: c.Scope, Contribution: c.Contribution, Field: c.Field})
	if err != nil {
		return "", err
	}
	cfg, err := s.config(ctx, m, spec, "")
	if err != nil {
		return "", err
	}
	// A token source only refreshes an invalid token; ours may still be
	// valid for seconds.
	stale := *tok
	stale.AccessToken = ""
	fresh, err := cfg.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, s.http), &stale).Token()
	if err != nil {
		return "", fmt.Errorf("refresh the OAuth token: %w", err)
	}
	if fresh.RefreshToken == "" {
		fresh.RefreshToken = tok.RefreshToken
	}
	sealed, err := seal(fresh)
	if err != nil {
		return "", err
	}
	prev := c.UpdatedAt
	c.Token, c.UpdatedAt = sealed, s.now()
	if !fresh.Expiry.IsZero() {
		exp := fresh.Expiry
		c.ExpiresAt = &exp
	}
	saved, err := s.repo.UpdateToken(ctx, c, prev)
	if err != nil {
		return "", err
	}
	if !saved {
		// Another node refreshed first; its token is the current one (a
		// rotated refresh token makes ours unusable).
		return s.AccessToken(ctx, pluginID, tenantID, ref)
	}
	return fresh.AccessToken, nil
}
