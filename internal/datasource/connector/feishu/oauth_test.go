package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// tokenServer returns an httptest server that answers the v2 oauth/token
// endpoint with the given response, and records the last request body.
func tokenServer(t *testing.T, resp map[string]interface{}, captured *map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/authen/v2/oauth/token" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if captured != nil {
			*captured = body
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestParseFeishuConfig_AuthModeFromSettings(t *testing.T) {
	cfg := &types.DataSourceConfig{
		Credentials: map[string]interface{}{"app_id": "cli_x", "app_secret": "sec"},
		Settings:    map[string]interface{}{"auth_mode": "user"},
	}
	fc, err := parseFeishuConfig(cfg)
	if err != nil {
		t.Fatalf("parseFeishuConfig: %v", err)
	}
	if !fc.IsUserMode() {
		t.Fatalf("expected user mode from settings, got %q", fc.AuthMode)
	}

	// Absent settings → app mode.
	fc2, _ := parseFeishuConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{"app_id": "cli_x", "app_secret": "sec"},
	})
	if fc2.IsUserMode() {
		t.Fatalf("expected app mode by default")
	}
}

func TestAuthorizeURL(t *testing.T) {
	conn := NewConnector()
	cfg := &types.DataSourceConfig{
		Credentials: map[string]interface{}{"app_id": "cli_abc", "app_secret": "s"},
	}
	redirect := "https://wk.example.com/api/v1/datasource/oauth/callback"
	raw, err := conn.AuthorizeURL(cfg, redirect, "STATE123")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if u.Host != "accounts.feishu.cn" {
		t.Errorf("host = %q, want accounts.feishu.cn", u.Host)
	}
	q := u.Query()
	if q.Get("client_id") != "cli_abc" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("state") != "STATE123" {
		t.Errorf("state = %q", q.Get("state"))
	}
	if q.Get("redirect_uri") != redirect {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if !strings.Contains(q.Get("scope"), "offline_access") {
		t.Errorf("scope missing offline_access: %q", q.Get("scope"))
	}
}

func TestRefreshCredentials_AppMode_NoOp(t *testing.T) {
	conn := NewConnector()
	cfg := &types.DataSourceConfig{
		Credentials: map[string]interface{}{"app_id": "x", "app_secret": "y"},
	}
	creds, err := conn.RefreshCredentials(context.Background(), cfg)
	if err != nil || creds != nil {
		t.Fatalf("app mode should be a no-op, got creds=%v err=%v", creds, err)
	}
}

func TestRefreshCredentials_UserMode_ValidToken_NoOp(t *testing.T) {
	conn := NewConnector()
	cfg := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"app_id": "x", "app_secret": "y",
			"user_access_token": "tok",
			"refresh_token":     "ref",
			"token_expires_at":  float64(time.Now().Add(time.Hour).Unix()), // well beyond the margin
		},
		Settings: map[string]interface{}{"auth_mode": "user"},
	}
	creds, err := conn.RefreshCredentials(context.Background(), cfg)
	if err != nil || creds != nil {
		t.Fatalf("valid token should not refresh, got creds=%v err=%v", creds, err)
	}
}

func TestRefreshCredentials_UserMode_Expired_Refreshes(t *testing.T) {
	var got map[string]string
	srv := tokenServer(t, map[string]interface{}{
		"code":                     0,
		"access_token":             "new-access",
		"expires_in":               7200,
		"refresh_token":            "new-refresh",
		"refresh_token_expires_in": 2592000,
		"token_type":               "Bearer",
	}, &got)
	defer srv.Close()

	conn := NewConnector()
	cfg := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"app_id": "cli_x", "app_secret": "sec",
			"base_url":          srv.URL,
			"user_access_token": "old-access",
			"refresh_token":     "old-refresh",
			"token_expires_at":  float64(time.Now().Unix()), // within the refresh margin → refresh
		},
		Settings: map[string]interface{}{"auth_mode": "user"},
	}
	creds, err := conn.RefreshCredentials(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RefreshCredentials: %v", err)
	}
	if creds == nil {
		t.Fatal("expected refreshed credentials")
	}
	if got["grant_type"] != "refresh_token" || got["refresh_token"] != "old-refresh" {
		t.Errorf("unexpected refresh request: %v", got)
	}
	if creds["user_access_token"] != "new-access" {
		t.Errorf("user_access_token = %v, want new-access", creds["user_access_token"])
	}
	if creds["refresh_token"] != "new-refresh" {
		t.Errorf("refresh_token = %v, want new-refresh (rotated)", creds["refresh_token"])
	}
	// App credentials must be preserved in the merged map.
	if creds["app_id"] != "cli_x" || creds["app_secret"] != "sec" {
		t.Errorf("app credentials not preserved: %v", creds)
	}
}

func TestExchangeCode(t *testing.T) {
	var got map[string]string
	srv := tokenServer(t, map[string]interface{}{
		"code":                     0,
		"access_token":             "user-access",
		"expires_in":               7200,
		"refresh_token":            "user-refresh",
		"refresh_token_expires_in": 2592000,
		"token_type":               "Bearer",
	}, &got)
	defer srv.Close()

	conn := NewConnector()
	cfg := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"app_id": "cli_x", "app_secret": "sec", "base_url": srv.URL,
		},
	}
	creds, settings, err := conn.ExchangeCode(context.Background(), cfg, "the-code", "https://wk/cb")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if got["grant_type"] != "authorization_code" || got["code"] != "the-code" || got["redirect_uri"] != "https://wk/cb" {
		t.Errorf("unexpected exchange request: %v", got)
	}
	if creds["user_access_token"] != "user-access" || creds["refresh_token"] != "user-refresh" {
		t.Errorf("tokens not stored: %v", creds)
	}
	if creds["app_id"] != "cli_x" {
		t.Errorf("app credentials not preserved: %v", creds)
	}
	if settings["auth_mode"] != AuthModeUser {
		t.Errorf("settings auth_mode = %v, want user", settings["auth_mode"])
	}
}

func TestExchangeCode_ProviderError(t *testing.T) {
	srv := tokenServer(t, map[string]interface{}{
		"code":              20050,
		"error":             "invalid_grant",
		"error_description": "code expired",
	}, nil)
	defer srv.Close()

	conn := NewConnector()
	cfg := &types.DataSourceConfig{
		Credentials: map[string]interface{}{"app_id": "x", "app_secret": "y", "base_url": srv.URL},
	}
	_, _, err := conn.ExchangeCode(context.Background(), cfg, "bad", "https://wk/cb")
	if err == nil {
		t.Fatal("expected error on provider failure")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error should surface provider message, got: %v", err)
	}
}
