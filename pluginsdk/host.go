package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Host calls back into WeKnora on behalf of one call: the tenant and scopes
// are those the call's token carries. It is only valid while the token lives
// (a few minutes), so do not keep it beyond the call.
type Host struct {
	base  string
	token string
	http  *http.Client
}

// Host returns the Host API client of this call, or nil when the plugin was
// granted no Host API scopes.
func (c *Call) Host() *Host {
	if c.Context.Host == nil || c.Context.Host.URL == "" || c.Context.Host.Token == "" {
		return nil
	}
	return &Host{
		base: strings.TrimSuffix(c.Context.Host.URL, "/"), token: c.Context.Host.Token,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

func (h *Host) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	u := h.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return pluginapi.Errorf(pluginapi.CodeUnavailable, "host api: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		var eb pluginapi.ErrorBody
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if json.Unmarshal(b, &eb) == nil && eb.Error.Code != "" {
			return &eb.Error
		}
		return pluginapi.Errorf(pluginapi.CodeInternal, "host api answered HTTP %d", resp.StatusCode)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// KVGet reads key into v. ok is false when the key does not exist.
func (h *Host) KVGet(ctx context.Context, key string, v any) (ok bool, err error) {
	var e pluginapi.KVEntry
	err = h.do(ctx, http.MethodGet, pluginapi.HostKVPath, url.Values{"key": {key}}, nil, &e)
	if pe, isPE := pluginapi.AsError(err); isPE && pe.Code == pluginapi.CodeNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(e.Value, v)
}

// KVPut stores v (as JSON) under key; ttl 0 keeps it until deleted.
func (h *Host) KVPut(ctx context.Context, key string, v any, ttl time.Duration) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	in := pluginapi.KVPut{Key: key, Value: raw, TTLSeconds: int(ttl / time.Second)}
	return h.do(ctx, http.MethodPut, pluginapi.HostKVPath, nil, in, nil)
}

// KVDelete removes key; a missing key is not an error.
func (h *Host) KVDelete(ctx context.Context, key string) error {
	return h.do(ctx, http.MethodDelete, pluginapi.HostKVPath, url.Values{"key": {key}}, nil, nil)
}

// KVList returns a page of keys with prefix after the given key.
func (h *Host) KVList(ctx context.Context, prefix, after string, limit int) (*pluginapi.KVList, error) {
	q := url.Values{"prefix": {prefix}, "after": {after}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out pluginapi.KVList
	return &out, h.do(ctx, http.MethodGet, pluginapi.HostKVListPath, q, nil, &out)
}

// DataSources lists the workspace's data sources of the plugin's
// connectors (scope "datasources").
func (h *Host) DataSources(ctx context.Context) ([]pluginapi.DataSourceInfo, error) {
	var out pluginapi.DataSourceList
	if err := h.do(ctx, http.MethodGet, pluginapi.HostDataSourcesPath, nil, nil, &out); err != nil {
		return nil, err
	}
	return out.DataSources, nil
}

// SyncDataSource starts an incremental sync of one of them, unless one is
// already running (scope "datasources").
func (h *Host) SyncDataSource(ctx context.Context, id string) (*pluginapi.SyncStarted, error) {
	var out pluginapi.SyncStarted
	return &out, h.do(ctx, http.MethodPost, pluginapi.HostDataSourceSyncPath(url.PathEscape(id)), nil, struct{}{}, &out)
}
