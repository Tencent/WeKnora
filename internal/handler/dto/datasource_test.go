package dto

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestDataSourceResponse_OmitsCredentials(t *testing.T) {
	cfg := types.DataSourceConfig{
		Type: "github",
		Credentials: map[string]interface{}{
			"token": "ghp-secret-do-not-leak",
		},
		ResourceIDs: []string{"repo-1"},
		Settings:    map[string]interface{}{"branch": "main"},
	}
	blob, _ := cfg.ToJSON()
	ds := &types.DataSource{
		ID:     "ds-1",
		Name:   "github-prod",
		Type:   "github",
		Config: blob,
	}
	body, err := json.Marshal(NewDataSourceResponse(ds))
	assert.NoError(t, err)
	s := string(body)
	assert.NotContains(t, s, "ghp-secret-do-not-leak")
	// The inner config object must not carry the credentials map (the
	// DataSourceConfigDTO type omits it structurally).
	var raw map[string]json.RawMessage
	assert.NoError(t, json.Unmarshal(body, &raw))
	if cfgRaw, ok := raw["config"]; ok {
		var inner map[string]json.RawMessage
		assert.NoError(t, json.Unmarshal(cfgRaw, &inner))
		_, hasCredsInConfig := inner["credentials"]
		assert.False(t, hasCredsInConfig,
			"credentials map must not appear inside the config DTO")
	}
	// Top-level credentials map is just the "configured?" indicator,
	// replaces the removed GET /credentials endpoint.
	assert.Contains(t, s, `"credentials":{"credentials":{"configured":true}}`)
	// Non-secret config fields pass through.
	assert.Contains(t, s, "repo-1")
	assert.Contains(t, s, "branch")
	assert.Contains(t, s, "main")
}

func TestDataSourceResponse_NilSafe(t *testing.T) {
	assert.Nil(t, NewDataSourceResponse(nil))
	assert.Equal(t, []*DataSourceResponse{}, NewDataSourceResponses(nil))
}

func TestDataSourceResponse_RSSFeedURLsFromCredentials(t *testing.T) {
	cfg := types.DataSourceConfig{
		Type: types.ConnectorTypeRSS,
		Credentials: map[string]interface{}{
			"feed_urls": "https://example.com/a.xml\nhttps://example.com/b.xml",
		},
		ResourceIDs: []string{"https://example.com/a.xml"},
	}
	blob, _ := cfg.ToJSON()
	ds := &types.DataSource{
		ID:     "ds-rss",
		Name:   "my-rss",
		Type:   types.ConnectorTypeRSS,
		Config: blob,
	}
	resp := NewDataSourceResponse(ds)
	assert.NotNil(t, resp.Config)
	assert.Equal(t, "https://example.com/a.xml\nhttps://example.com/b.xml", resp.Config.Settings["feed_urls"])
	assert.False(t, resp.Credentials["credentials"].Configured)
	body, err := json.Marshal(resp)
	assert.NoError(t, err)
	s := string(body)
	assert.Contains(t, s, "https://example.com/a.xml")
	assert.NotContains(t, s, "Bearer secret")
}

func TestDataSourceResponse_RSSAuthHeadersConfigured(t *testing.T) {
	cfg := types.DataSourceConfig{
		Type: types.ConnectorTypeRSS,
		Credentials: map[string]interface{}{
			"auth_headers": "Authorization: Bearer secret",
		},
		Settings: map[string]interface{}{
			"feed_urls": "https://example.com/feed.xml",
		},
	}
	blob, _ := cfg.ToJSON()
	ds := &types.DataSource{
		ID:     "ds-rss-auth",
		Name:   "my-rss",
		Type:   types.ConnectorTypeRSS,
		Config: blob,
	}
	resp := NewDataSourceResponse(ds)
	assert.True(t, resp.Credentials["credentials"].Configured)
	body, err := json.Marshal(resp)
	assert.NoError(t, err)
	assert.NotContains(t, string(body), "Bearer secret")
}

func TestDataSourceResponse_NoConfig(t *testing.T) {
	ds := &types.DataSource{ID: "x", Name: "x"}
	body, err := json.Marshal(NewDataSourceResponse(ds))
	assert.NoError(t, err)
	// No config jsonb stored → no config object in the response.
	assert.NotContains(t, string(body), `"config":`)
}
