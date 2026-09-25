//go:build !bindings

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDesktopAPIProxyRewritesWailsHostToLoopback(t *testing.T) {
	var got string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Host
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	target, err := url.Parse(backend.URL)
	require.NoError(t, err)
	front := httptest.NewServer(desktopAPIProxy(target))
	defer front.Close()

	req, err := http.NewRequest(http.MethodPost, front.URL+"/api/v1/me/browser", nil)
	require.NoError(t, err)
	req.Host = "wails.localhost"
	resp, err := front.Client().Do(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, target.Host, got)
}

func TestDesktopAPIProxyKeepsHostForOtherRoutes(t *testing.T) {
	var got string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Host
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	target, err := url.Parse(backend.URL)
	require.NoError(t, err)
	front := httptest.NewServer(desktopAPIProxy(target))
	defer front.Close()

	req, err := http.NewRequest(http.MethodGet, front.URL+"/api/v1/auth/oidc/start", nil)
	require.NoError(t, err)
	req.Host = "wails.localhost"
	resp, err := front.Client().Do(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, "wails.localhost", got)
}
