package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

type memConnections struct {
	mu   sync.Mutex
	rows map[string]types.PluginOAuthConnection
}

func (m *memConnections) Create(_ context.Context, c *types.PluginOAuthConnection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[c.ID] = *c
	return nil
}

func (m *memConnections) Get(
	_ context.Context,
	pluginID string,
	tenantID uint64,
	id string,
) (*types.PluginOAuthConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.rows[id]
	if !ok || c.PluginID != pluginID || c.TenantID != tenantID {
		return nil, nil
	}
	return &c, nil
}

func (m *memConnections) UpdateToken(_ context.Context, c *types.PluginOAuthConnection, prev time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.rows[c.ID].UpdatedAt.Equal(prev) {
		return false, nil
	}
	m.rows[c.ID] = *c
	return true, nil
}

// provider is a fake authorization server.
type provider struct {
	*httptest.Server
	refreshes atomic.Int32
	verifier  string
}

func newProvider(t *testing.T) *provider {
	p := &provider{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if user, pass, _ := r.BasicAuth(); user != "app-id" || pass != "app-secret" {
			if r.Form.Get("client_id") != "app-id" || r.Form.Get("client_secret") != "app-secret" {
				http.Error(w, "bad client", http.StatusUnauthorized)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "the-code" {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			p.verifier = r.Form.Get("code_verifier")
			_, _ = fmt.Fprint(w, `{"access_token":"at-1","refresh_token":"rt-1","token_type":"bearer","expires_in":30}`)
		case "refresh_token":
			n := p.refreshes.Add(1)
			_, _ = fmt.Fprintf(w, `{"access_token":"at-%d","token_type":"bearer","expires_in":3600}`, n+1)
		}
	}))
	t.Cleanup(p.Close)
	return p
}

func setup(t *testing.T, p *provider) *Service {
	t.Helper()
	utils.SetSSRFWhitelistFromRaw("127.0.0.1,localhost")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	tenantSchema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{"account": map[string]any{
			"type": "string", "x-oauth": map[string]any{
				"authorizeUrl": "https://auth.example.com/authorize",
				"tokenUrl":     strings.Replace(p.URL, "127.0.0.1", "localhost", 1) + "/token",
				"scopes": []string{
					"read",
				}, "clientId": "${system.client_id}", "clientSecret": "${system.client_secret}",
				"pkce": true, "params": map[string]string{"audience": "api.example.com"},
			},
		}},
	})
	systemSchema, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{
		"client_id": map[string]any{
			"type": "string",
		}, "client_secret": map[string]any{"type": "string", "x-secret": true},
	}})
	m := &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, ID: "acme.jira", Version: "1.0.0", APIVersion: pluginapi.APIVersion,
		Name: manifest.Text("Jira", nil), Publisher: manifest.Publisher{ID: "acme"},
		Runtime:     manifest.Runtime{Type: manifest.RuntimeHost, Kind: "binary", Entry: "bin/x"},
		Contributes: manifest.Contributions{manifest.PointWebSearch: {{ID: "x", Name: manifest.Text("X", nil)}}},
		Config:      manifest.ConfigSchemas{SystemSchema: systemSchema, TenantSchema: tenantSchema},
	}
	reg := registry.New()
	if err := reg.Register(m); err != nil {
		t.Fatal(err)
	}
	plugins := plugintest.NewMemRepo()
	sys, _ := json.Marshal(map[string]any{"client_id": "app-id", "client_secret": "app-secret"})
	_ = plugins.SavePlugin(context.Background(), &types.InstalledPlugin{ID: "acme.jira", SystemConfig: types.JSON(sys)})
	return NewService(reg, plugins, &memConnections{rows: map[string]types.PluginOAuthConnection{}}, nil)
}

func TestAuthorizeExchangeAndRefresh(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t)
	s := setup(t, p)
	target := Target{PluginID: "acme.jira", TenantID: 7, Scope: ScopeTenant, Field: "account"}

	authURL, state, err := s.Start(ctx, target, "u1", "https://app.example.com", "https://weknora.example.com")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if u.Host != "auth.example.com" || q.Get("client_id") != "app-id" || q.Get("state") != state ||
		q.Get("code_challenge_method") != "S256" || q.Get("audience") != "api.example.com" ||
		q.Get("redirect_uri") != "https://weknora.example.com"+CallbackPath || q.Get("scope") != "read" {
		t.Fatalf("authorize URL = %s", authURL)
	}

	res := s.Complete(ctx, state, "the-code", "")
	if res.Err != nil || res.Origin != "https://app.example.com" || res.ConnectionID == "" || p.verifier == "" {
		t.Fatalf("complete = %+v verifier %q", res, p.verifier)
	}
	if again := s.Complete(ctx, state, "the-code", ""); again.Err == nil {
		t.Fatal("a state must not be usable twice")
	}
	ref := configschema.OAuthRefPrefix + res.ConnectionID
	if !configschema.IsOAuthRef(ref) {
		t.Fatalf("ref %s is not a reference", ref)
	}

	// The first token expires within the refresh window: it is refreshed,
	// keeping the refresh token.
	tok, err := s.AccessToken(ctx, "acme.jira", 7, ref)
	if err != nil || tok != "at-2" || p.refreshes.Load() != 1 {
		t.Fatalf("token = %q, %v (refreshes %d)", tok, err, p.refreshes.Load())
	}
	if tok, _ := s.AccessToken(ctx, "acme.jira", 7, ref); tok != "at-2" || p.refreshes.Load() != 1 {
		t.Fatalf("a fresh token is reused, got %q", tok)
	}
	if _, err := s.AccessToken(ctx, "acme.jira", 8, ref); err == nil {
		t.Fatal("another workspace cannot use the connection")
	}
	if raw := s.repo.(*memConnections).rows[res.ConnectionID].Token; strings.Contains(raw, "at-") &&
		utils.GetAESKey() != nil {
		t.Fatal("tokens are stored sealed")
	}
}

func TestStartRefusesFieldsWithoutOAuth(t *testing.T) {
	s := setup(t, newProvider(t))
	for _, target := range []Target{
		{PluginID: "acme.jira", TenantID: 7, Scope: ScopeTenant, Field: "other"},
		{PluginID: "acme.jira", Scope: ScopeSystem, Field: "account"},
		{PluginID: "acme.nope", TenantID: 7, Scope: ScopeTenant, Field: "account"},
	} {
		if _, _, err := s.Start(context.Background(), target, "u", "https://a", "https://a"); err == nil {
			t.Errorf("%+v started", target)
		}
	}
	if res := s.Complete(context.Background(), "unknown", "code", ""); res.Err == nil {
		t.Fatal("an unknown state completed")
	}
}
